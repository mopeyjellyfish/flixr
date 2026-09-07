// Package catalog indexes film and episodic TV files beneath owner-configured roots.
package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

var (
	ErrOutsideRoot = errors.New("path outside configured roots")
	ErrScanActive  = errors.New("scan already active")
)

const scanHistoryLimit = 32

// MediaProperties is the path-free inspection result persisted with a catalog item.
type MediaProperties struct {
	Container               string          `json:"container,omitempty"`
	DurationMS              int64           `json:"duration_ms,omitempty"`
	VideoProfile            string          `json:"video_profile,omitempty"`
	VideoCodec              string          `json:"video_codec,omitempty"`
	PrimaryVideoStreamIndex int             `json:"primary_video_stream_index"`
	Width                   int             `json:"width,omitempty"`
	Height                  int             `json:"height,omitempty"`
	Bitrate                 int64           `json:"bitrate,omitempty"`
	HDR                     string          `json:"hdr,omitempty"`
	Audio                   []AudioTrack    `json:"audio,omitempty"`
	Subtitles               []SubtitleTrack `json:"subtitles,omitempty"`
}

type AudioTrack struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels,omitempty"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
}

// UnmarshalJSON treats indexes absent from pre-011 track JSON as unknown. Zero
// is a real ffprobe stream index, so it must remain distinct from unknown.
func (t *AudioTrack) UnmarshalJSON(data []byte) error {
	var raw struct {
		Index    *int   `json:"index"`
		Codec    string `json:"codec"`
		Channels int    `json:"channels"`
		Language string `json:"language"`
		Title    string `json:"title"`
		Default  bool   `json:"default"`
		Forced   bool   `json:"forced"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*t = AudioTrack{Index: normalizedIndex(raw.Index), Codec: raw.Codec, Channels: nonNegative(raw.Channels), Language: raw.Language, Title: raw.Title, Default: raw.Default, Forced: raw.Forced}
	return nil
}

type SubtitleTrack struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
}

func (t *SubtitleTrack) UnmarshalJSON(data []byte) error {
	var raw struct {
		Index    *int   `json:"index"`
		Codec    string `json:"codec"`
		Language string `json:"language"`
		Title    string `json:"title"`
		Default  bool   `json:"default"`
		Forced   bool   `json:"forced"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*t = SubtitleTrack{Index: normalizedIndex(raw.Index), Codec: raw.Codec, Language: raw.Language, Title: raw.Title, Default: raw.Default, Forced: raw.Forced}
	return nil
}

func normalizedIndex(value *int) int {
	if value == nil || *value < 0 {
		return -1
	}
	return *value
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

type Item struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Kind         string   `json:"kind"`
	Season       int      `json:"season,omitempty"`
	Episode      int      `json:"episode,omitempty"`
	SeriesID     string   `json:"series_id,omitempty"`
	LocalOnly    bool     `json:"local_only"`
	ProviderID   string   `json:"provider_id,omitempty"`
	Provider     string   `json:"metadata_provider,omitempty"`
	Language     string   `json:"metadata_language,omitempty"`
	Region       string   `json:"metadata_region,omitempty"`
	Confidence   float64  `json:"match_confidence,omitempty"`
	OwnerMatch   bool     `json:"owner_matched,omitempty"`
	OwnerUnmatch bool     `json:"owner_unmatched,omitempty"`
	Year         int      `json:"year,omitempty"`
	Synopsis     string   `json:"synopsis,omitempty"`
	Poster       string   `json:"poster,omitempty"`
	Backdrop     string   `json:"backdrop,omitempty"`
	Genres       []string `json:"genres"`
	AddedAt      int64    `json:"added_at"`
	Playable     bool     `json:"playable"`
	Demo         bool     `json:"demo"`
	MediaProperties

	metadataVersion uint64
	path            string
	rootKind        string
	digest          string
	changeToken     string
	sourceRoot      string
	sourceSeriesID  string
	fingerprint     string
	size            int64
	mtime           int64
	probeRevision   int
}

const mediaProbeRevision = 1

// Season is an ordered group of playable episode records.
type Season struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Episodes []Item `json:"episodes"`
}

// Series is a catalog entity, distinct from its playable episode records.
type Series struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Kind         string   `json:"kind"`
	LocalOnly    bool     `json:"local_only"`
	ProviderID   string   `json:"provider_id,omitempty"`
	Provider     string   `json:"metadata_provider,omitempty"`
	Language     string   `json:"metadata_language,omitempty"`
	Region       string   `json:"metadata_region,omitempty"`
	Confidence   float64  `json:"match_confidence,omitempty"`
	OwnerMatch   bool     `json:"owner_matched,omitempty"`
	OwnerUnmatch bool     `json:"owner_unmatched,omitempty"`
	Year         int      `json:"year,omitempty"`
	Synopsis     string   `json:"synopsis,omitempty"`
	Poster       string   `json:"poster,omitempty"`
	Backdrop     string   `json:"backdrop,omitempty"`
	Genres       []string `json:"genres"`
	AddedAt      int64    `json:"added_at"`
	Playable     bool     `json:"playable"`
	Demo         bool     `json:"demo"`
	Seasons      []Season `json:"seasons,omitempty"`
}

type ScanStatus struct {
	ID         string `json:"id"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	Status     string `json:"status"`
	Scanned    int    `json:"scanned"`
	Failed     int    `json:"failed"`
	Unmatched  int    `json:"unmatched"`
	Message    string `json:"message,omitempty"`
}

type ArtworkMaintenanceStatus struct {
	LastRun time.Time
	Outcome string
}

type Catalog struct {
	demo              bool
	demoSource        string
	mu                sync.RWMutex
	db                *sqlite.DB
	fs                afero.Fs
	film, tv          string
	items             map[string]Item
	series            map[string]Series
	prober            Prober
	provider          MetadataProvider
	token             string
	scanning          bool
	cancel            context.CancelFunc
	done              chan struct{}
	status            ScanStatus
	artworkMu         sync.Mutex
	artworkGroup      singleflight.Group
	maintenanceCancel context.CancelFunc
	maintenanceDone   chan struct{}
	maintenanceStatus ArtworkMaintenanceStatus
	maintenanceDir    afero.File
	artworkObjectsDir afero.File
	derivativeBytes   int64
	derivativeCount   int
	derivativeReady   bool
	refreshPreviews   map[string]refreshPreview
	metadataVersions  map[string]uint64
}

func (c *Catalog) ArtworkMaintenanceStatus() ArtworkMaintenanceStatus {
	c.artworkMu.Lock()
	defer c.artworkMu.Unlock()
	return c.maintenanceStatus
}

func New() *Catalog {
	return &Catalog{items: map[string]Item{}, series: map[string]Series{}, refreshPreviews: map[string]refreshPreview{}, prober: newFFprobe(), fs: afero.NewOsFs()}
}

// Open uses the production ffprobe prober and OS-backed Afero filesystem.
func Open(db *sqlite.DB) (*Catalog, error) {
	return OpenWithFilesystem(db, newFFprobe(), afero.NewOsFs())
}

func OpenWithProber(db *sqlite.DB, prober Prober) (*Catalog, error) {
	return OpenWithFilesystem(db, prober, afero.NewOsFs())
}

func OpenWithFilesystem(db *sqlite.DB, prober Prober, fs afero.Fs) (*Catalog, error) {
	if prober == nil || fs == nil {
		return nil, errors.New("catalog prober and filesystem are required")
	}
	c := &Catalog{db: db, items: map[string]Item{}, series: map[string]Series{}, refreshPreviews: map[string]refreshPreview{}, prober: prober, provider: NewTMDB(nil), fs: fs}
	if db == nil {
		return c, nil
	}
	rows, err := db.Query(`SELECT id, kind, title, relative_path, local_only, root_kind, fingerprint, size_bytes, mtime_unix, container, duration_ms, video_codec, video_profile, primary_video_stream_index, video_width, video_height, video_bitrate, video_hdr, audio_json, subtitle_json, probe_revision, series_id, provider_id, year, synopsis, poster, backdrop, genres_json, added_at, playable, demo FROM catalog_items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Item
		var local, playable, demo int
		var audio, subtitles, genres string
		if err := rows.Scan(&x.ID, &x.Kind, &x.Title, &x.path, &local, &x.rootKind, &x.fingerprint, &x.size, &x.mtime, &x.Container, &x.DurationMS, &x.VideoCodec, &x.VideoProfile, &x.PrimaryVideoStreamIndex, &x.Width, &x.Height, &x.Bitrate, &x.HDR, &audio, &subtitles, &x.probeRevision, &x.SeriesID, &x.ProviderID, &x.Year, &x.Synopsis, &x.Poster, &x.Backdrop, &genres, &x.AddedAt, &playable, &demo); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(audio), &x.Audio); err != nil {
			return nil, fmt.Errorf("decode audio tracks: %w", err)
		}
		if err := json.Unmarshal([]byte(subtitles), &x.Subtitles); err != nil {
			return nil, fmt.Errorf("decode subtitle tracks: %w", err)
		}
		if err := json.Unmarshal([]byte(genres), &x.Genres); err != nil {
			return nil, fmt.Errorf("decode genres: %w", err)
		}
		x.LocalOnly, x.Playable, x.Demo = local != 0, playable != 0, demo != 0
		episodeFields(&x)
		if x.SeriesID == "" {
			seriesFields(&x)
		}
		c.items[x.ID] = x
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := c.loadMatchState("catalog_items", func(id, provider, language, region string, confidence float64, owner, unmatched int) {
		x := c.items[id]
		x.Provider, x.Language, x.Region, x.Confidence, x.OwnerMatch, x.OwnerUnmatch = provider, language, region, confidence, owner != 0, unmatched != 0
		c.items[id] = x
	}); err != nil {
		return nil, err
	}
	seriesRows, err := db.Query(`SELECT id,title,local_only,provider_id,year,synopsis,poster,backdrop,genres_json,added_at,playable,demo FROM catalog_series`)
	if err != nil {
		return nil, err
	}
	defer seriesRows.Close()
	for seriesRows.Next() {
		var x Series
		var local, playable, demo int
		var genres string
		if err := seriesRows.Scan(&x.ID, &x.Title, &local, &x.ProviderID, &x.Year, &x.Synopsis, &x.Poster, &x.Backdrop, &genres, &x.AddedAt, &playable, &demo); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(genres), &x.Genres); err != nil {
			return nil, fmt.Errorf("decode genres: %w", err)
		}
		x.Kind, x.LocalOnly, x.Playable, x.Demo = "series", local != 0, playable != 0, demo != 0
		c.series[x.ID] = x
	}
	if err := seriesRows.Err(); err != nil {
		return nil, err
	}
	if err := c.loadMatchState("catalog_series", func(id, provider, language, region string, confidence float64, owner, unmatched int) {
		x := c.series[id]
		x.Provider, x.Language, x.Region, x.Confidence, x.OwnerMatch, x.OwnerUnmatch = provider, language, region, confidence, owner != 0, unmatched != 0
		c.series[id] = x
	}); err != nil {
		return nil, err
	}
	_ = db.QueryRow("SELECT value FROM settings WHERE key='film_root'").Scan(&c.film)
	_ = db.QueryRow("SELECT value FROM settings WHERE key='tv_root'").Scan(&c.tv)
	_ = db.QueryRow("SELECT value FROM settings WHERE key='tmdb_token'").Scan(&c.token)
	c.status = c.lastStatus()
	if err := c.loadSourceProof(); err != nil {
		return nil, err
	}
	c.startArtworkMaintenance()
	return c, nil
}

func (c *Catalog) loadMatchState(table string, apply func(string, string, string, string, float64, int, int)) error {
	rows, err := c.db.Query(`SELECT id,metadata_provider,metadata_language,metadata_region,match_confidence,owner_matched,owner_unmatched FROM ` + table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, provider, language, region string
		var confidence float64
		var owner, unmatched int
		if err := rows.Scan(&id, &provider, &language, &region, &confidence, &owner, &unmatched); err != nil {
			return err
		}
		apply(id, provider, language, region, confidence, owner, unmatched)
	}
	return rows.Err()
}

func id(kind, fingerprint string) string {
	s := sha256.Sum256([]byte(kind + "\000" + fingerprint))
	return hex.EncodeToString(s[:16])
}
func title(path string) string {
	n := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	n = regexp.MustCompile(`(?i)[. _-]?(s\d{1,2}e\d{1,4}|\d{1,2}x\d{1,4})$`).ReplaceAllString(n, "")
	n = strings.NewReplacer(".", " ", "_", " ").Replace(n)
	return strings.TrimSpace(n)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var episodeRE = regexp.MustCompile(`(?i)(?:s(\d{1,2})e(\d{1,4})|(\d{1,2})x(\d{1,4}))`)

func episodeFields(x *Item) {
	m := episodeRE.FindStringSubmatch(filepath.Base(x.path))
	if len(m) == 0 {
		return
	}
	if m[1] != "" {
		fmt.Sscanf(m[1], "%d", &x.Season)
		fmt.Sscanf(m[2], "%d", &x.Episode)
	} else {
		fmt.Sscanf(m[3], "%d", &x.Season)
		fmt.Sscanf(m[4], "%d", &x.Episode)
	}
}

func seriesFields(x *Item) {
	if x.rootKind != "episode" {
		return
	}
	parts := strings.Split(filepath.ToSlash(x.path), "/")
	name := ""
	if len(parts) > 1 {
		name = strings.TrimSpace(parts[0])
	}
	if name == "" {
		name = x.Title
	}
	x.SeriesID = id("series", strings.ToLower(name))
}

func seriesTitle(path, fallback string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) > 1 && strings.TrimSpace(parts[0]) != "" {
		return strings.NewReplacer(".", " ", "_", " ").Replace(strings.TrimSpace(parts[0]))
	}
	return fallback
}

func (c *Catalog) SetRoots(film, tv string) error {
	if err := c.ValidateRoots(film, tv); err != nil {
		return err
	}
	nextFilm, nextTV := film, tv
	var err error
	if nextFilm != "" {
		nextFilm, err = filepath.Abs(nextFilm)
		if err != nil {
			return err
		}
	}
	if nextTV != "" {
		nextTV, err = filepath.Abs(nextTV)
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, setting := range []struct{ key, value string }{{"film_root", nextFilm}, {"tv_root", nextTV}} {
			if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", setting.key, setting.value); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	c.film, c.tv = nextFilm, nextTV
	return nil
}

// ValidateRoots applies the same filesystem checks as SetRoots without saving.
func (c *Catalog) ValidateRoots(film, tv string) error {
	for _, p := range []string{film, tv} {
		if p == "" {
			continue
		}
		a, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		info, err := c.fs.Stat(a)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("root %q: %w", p, ErrOutsideRoot)
		}
	}
	return nil
}

// Roots returns the persisted owner library roots without exposing media paths to household routes.
func (c *Catalog) Roots() (film, tv string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.film, c.tv
}

func media(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mkv", ".webm", ".mov", ".avi":
		return true
	}
	return false
}

type scanFile struct {
	changeToken     string
	root, kind, rel string
	size, mtime     int64
}
type scanKey struct{ kind, rel string }
type scanResult struct {
	item Item
	file scanFile
	err  error
}

type scanObservation struct {
	identifier, outcome, message string
}

var readRandom = rand.Read

func randomScanID() (string, error) {
	b := make([]byte, 32)
	if _, err := readRandom(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// StartScan begins one bounded asynchronous scan. Its current and last outcome is available through ScanStatus.
func (c *Catalog) StartScan(ctx context.Context, workers int) error {
	if workers < 1 {
		workers = 1
	}
	scanID, err := randomScanID()
	if err != nil {
		return fmt.Errorf("generate scan ID: %w", err)
	}
	c.mu.Lock()
	if c.scanning {
		c.mu.Unlock()
		return ErrScanActive
	}
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	c.scanning = true
	c.status = ScanStatus{ID: scanID, StartedAt: time.Now().Unix(), Status: "running"}
	status := c.status
	c.mu.Unlock()
	c.saveStatus(status)
	go c.runScan(ctx, workers)
	return nil
}

// Scan waits for one scan, retaining the synchronous API for command and test callers.
func (c *Catalog) Scan(ctx context.Context, workers int) error {
	if err := c.StartScan(ctx, workers); err != nil {
		return err
	}
	c.mu.RLock()
	done, active := c.done, c.scanning
	c.mu.RUnlock()
	if !active {
		return c.scanError()
	}
	select {
	case <-done:
		return c.scanError()
	case <-ctx.Done():
		c.Cancel()
		<-done
		return ctx.Err()
	}
}

func (c *Catalog) runScan(ctx context.Context, workers int) {
	err := c.scan(ctx, workers)
	c.mu.Lock()
	if err != nil {
		c.status.Status = "failed"
		c.status.Message = err.Error()
	} else if c.status.Failed > 0 {
		c.status.Status = "partial"
	} else {
		c.status.Status = "complete"
	}
	c.status.FinishedAt = time.Now().Unix()
	status := c.status
	c.mu.Unlock()

	// Keep the scan active until its terminal report is persisted. Otherwise a
	// new scan can publish its running report first and the older scan's
	// retention transaction can prune that active row.
	c.saveStatus(status)

	c.mu.Lock()
	c.scanning, c.cancel = false, nil
	close(c.done)
	c.done = nil
	c.mu.Unlock()
}

func (c *Catalog) scanError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.status.Status == "failed" {
		return errors.New(c.status.Message)
	}
	return nil
}

func (c *Catalog) scan(ctx context.Context, workers int) error {
	c.mu.RLock()
	roots := []struct{ path, kind string }{{c.film, "film"}, {c.tv, "episode"}}
	previousByPath := make(map[scanKey]Item, len(c.items))
	for _, item := range c.items {
		previousByPath[scanKey{item.rootKind, item.path}] = item
	}
	c.mu.RUnlock()
	physicalProof, err := c.loadPhysicalSources(previousByPath)
	if err != nil {
		return err
	}

	var files []scanFile
	for _, r := range roots {
		if r.path == "" {
			continue
		}
		err := afero.Walk(c.fs, r.path, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				// A disconnected or unreadable mount is not an empty library.
				return fmt.Errorf("read media directory: %w", walkErr)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !media(path) {
				return nil
			}
			rel, err := filepath.Rel(r.path, path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return ErrOutsideRoot
			}
			files = append(files, scanFile{root: r.path, kind: r.kind, rel: filepath.ToSlash(rel), size: info.Size(), mtime: info.ModTime().UnixNano(), changeToken: fileChangeToken(info)})
			return nil
		})
		if err != nil {
			return err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].kind+"/"+files[i].rel < files[j].kind+"/"+files[j].rel })
	results := make(chan scanResult, len(files))
	g, groupCtx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for _, f := range files {
		g.Go(func() error {
			// A stable path, size and mtime never opens, hashes, or probes the file again.
			if old, ok := previousByPath[scanKey{f.kind, f.rel}]; ok && old.size == f.size && old.mtime == f.mtime && old.changeToken == f.changeToken && old.probeRevision == mediaProbeRevision && old.digest != "" {
				select {
				case results <- scanResult{item: old, file: f}:
					return nil
				case <-groupCtx.Done():
					return groupCtx.Err()
				}
			}
			x, err := c.inspect(groupCtx, f)
			if err != nil && groupCtx.Err() != nil {
				return groupCtx.Err()
			}
			select {
			case results <- scanResult{item: x, file: f, err: err}:
				return nil
			case <-groupCtx.Done():
				return groupCtx.Err()
			}
		})
	}
	wait := make(chan error, 1)
	go func() { wait <- g.Wait(); close(results) }()
	failures := map[scanKey]string{}
	var inspected []scanResult
	for result := range results {
		c.mu.Lock()
		c.status.Scanned++
		if result.err != nil {
			c.status.Failed++
		}
		c.mu.Unlock()
		if result.err != nil {
			failures[scanKey{result.file.kind, result.file.rel}] = result.err.Error()
			continue
		}
		inspected = append(inspected, result)
	}
	if err := <-wait; err != nil {
		return err
	}
	sort.Slice(inspected, func(i, j int) bool {
		return inspected[i].file.kind+"/"+inspected[i].file.rel < inspected[j].file.kind+"/"+inspected[j].file.rel
	})
	next, sources, conflicts, err := c.reconcileIdentity(ctx, inspected, previousByPath, physicalProof)
	if err != nil {
		return err
	}
	observations, seriesState, err := c.enrich(ctx, next)
	if err != nil {
		return err
	}
	c.mu.Lock()
	for _, observation := range observations {
		if observation.outcome == "unmatched" {
			c.status.Unmatched++
		} else {
			c.status.Failed++
		}
	}
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.persist(ctx, next, sources, failures, observations, conflicts, seriesState)
}

func digestFile(ctx context.Context, file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if _, err := hash.Write(buffer[:n]); err != nil {
				return "", err
			}
		}
		if readErr == io.EOF {
			return hex.EncodeToString(hash.Sum(nil)), nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}

func (c *Catalog) inspect(ctx context.Context, f scanFile) (Item, error) {
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return Item{}, err
	}
	defer root.Close()
	file, err := root.Open(f.rel) // os.Root enforces containment at open time, including nested symlinks.
	if err != nil {
		return Item{}, fmt.Errorf("open indexed file: %w", err)
	}
	defer file.Close()
	fingerprint, err := contentFingerprint(file)
	if err != nil {
		return Item{}, fmt.Errorf("fingerprint indexed file: %w", err)
	}
	digest, err := digestFile(ctx, file)
	if err != nil {
		return Item{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Item{}, err
	}
	properties, err := c.prober.Probe(ctx, file)
	if err != nil {
		return Item{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return Item{}, err
	}
	if info.Size() != f.size || info.ModTime().UnixNano() != f.mtime || fileChangeToken(info) != f.changeToken {
		return Item{}, errors.New("media changed during inspection")
	}
	if properties.VideoCodec == "" {
		properties.PrimaryVideoStreamIndex = -1
	}
	switch strings.ToLower(filepath.Ext(f.rel)) {
	case ".mkv":
		properties.Container = "matroska"
	case ".webm":
		properties.Container = "webm"
	}
	x := Item{ID: id(f.kind, fingerprint), Title: title(f.rel), Kind: f.kind, LocalOnly: true, AddedAt: time.Now().Unix(), Playable: true, MediaProperties: properties, path: f.rel, rootKind: f.kind, fingerprint: fingerprint, digest: digest, changeToken: f.changeToken, size: f.size, mtime: f.mtime, probeRevision: mediaProbeRevision}
	episodeFields(&x)
	seriesFields(&x)
	x.sourceSeriesID = x.SeriesID
	return x, nil
}

func (c *Catalog) enrich(ctx context.Context, next map[string]Item) ([]scanObservation, map[string]Series, error) {
	c.mu.RLock()
	provider, token := c.provider, c.token
	previousSeries := make(map[string]Series, len(c.series))
	for id, series := range c.series {
		previousSeries[id] = series
	}
	previousItems := make(map[scanKey]Item, len(c.items))
	for _, item := range next {
		if previous, ok := c.items[item.ID]; ok {
			previousItems[scanKey{item.rootKind, item.path}] = previous
		}
	}
	c.mu.RUnlock()
	var observations []scanObservation
	for id, item := range next {
		if previous, ok := previousItems[scanKey{item.rootKind, item.path}]; ok {
			c.applyLockedFields("film", previous.ID, &item)
			if previous.ProviderID != "" || previous.OwnerMatch || previous.OwnerUnmatch {
				item.ProviderID, item.Provider, item.Language, item.Region, item.Confidence, item.OwnerMatch, item.OwnerUnmatch, item.Year, item.Synopsis, item.Poster, item.Backdrop, item.LocalOnly = previous.ProviderID, previous.Provider, previous.Language, previous.Region, previous.Confidence, previous.OwnerMatch, previous.OwnerUnmatch, previous.Year, previous.Synopsis, previous.Poster, previous.Backdrop, previous.LocalOnly
			}
			next[id] = item
		}
	}
	if provider != nil && token != "" {
		for id, item := range next {
			if item.Kind != "film" || item.OwnerUnmatch {
				continue
			}
			var enrichment Enrichment
			var err error
			legacyReconciliation := false
			identity := artworkIdentity{catalogKind: "film", catalogID: id, providerID: item.ProviderID}
			if item.ProviderID != "" {
				enrichment, err = c.pendingArtwork(identity)
				if err == nil && enrichment.Poster == "" && enrichment.Backdrop == "" {
					legacyReconciliation, err = c.pendingLegacyArtworkReconciliation(identity)
					if err == nil && !legacyReconciliation {
						continue
					}
					if err == nil {
						if exact, ok := provider.(CandidateProvider); ok {
							enrichment, err = exact.ByID(ctx, token, "film", item.ProviderID, item.Language, item.Region)
						} else {
							err = ErrProviderUnavailable
						}
					}
				}
			} else {
				enrichment, err = provider.Lookup(ctx, token, "film", item.Title)
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				observations = append(observations, providerFailure("film:"+id, "provider_failed"))
				continue
			}
			if legacyReconciliation && enrichment.ProviderID != item.ProviderID {
				if err := c.completeLegacyArtworkReconciliation(identity); err != nil {
					return nil, nil, err
				}
				observations = append(observations, providerFailure("film:"+id, "unmatched"))
				continue
			}
			if enrichment.ProviderID == "" {
				observations = append(observations, providerFailure("film:"+id, "unmatched"))
				continue
			}
			identity = artworkIdentity{catalogKind: "film", catalogID: id, providerID: enrichment.ProviderID}
			if legacyReconciliation {
				enrichment = c.unlockedArtwork("film", id, enrichment)
				enrichment = missingArtwork(enrichment, item.Poster, item.Backdrop)
				if err := c.stageLegacyArtworkRetries(identity, enrichment); err != nil {
					return nil, nil, err
				}
			}
			artworkFailed, err := c.cacheEnrichmentArtwork(ctx, identity, &enrichment, item.Poster, item.Backdrop)
			if err != nil {
				return nil, nil, err
			}
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if artworkFailed {
				observations = append(observations, providerFailure("film:"+id, "provider_artwork_failed"))
			}
			if item.ProviderID == "" {
				item.LocalOnly = false
				item.Provider = "tmdb"
				item.ProviderID, item.Year, item.Synopsis = enrichment.ProviderID, enrichment.Year, enrichment.Synopsis
				if enrichment.Title != "" {
					item.Title = enrichment.Title
				}
			}
			item.Poster, item.Backdrop = enrichment.Poster, enrichment.Backdrop
			if previous, ok := previousItems[scanKey{item.rootKind, item.path}]; ok {
				c.applyLockedFields("film", previous.ID, &item)
			}
			next[id] = item
		}
	}
	series := map[string]Series{}
	for _, item := range next {
		if item.SeriesID == "" {
			continue
		}
		if existing, ok := series[item.SeriesID]; ok {
			existing.LocalOnly = existing.LocalOnly && item.LocalOnly
			series[item.SeriesID] = existing
			continue
		}
		series[item.SeriesID] = Series{ID: item.SeriesID, Title: seriesTitle(item.path, item.Title), Kind: "series", LocalOnly: item.LocalOnly, Playable: true}
	}
	for id, value := range series {
		previous, retained := previousSeries[id]
		var enrichment Enrichment
		var enrichmentErr error
		attempted := false
		legacyReconciliation := false
		identity := artworkIdentity{catalogKind: "series", catalogID: id, providerID: previous.ProviderID}
		if retained && (previous.ProviderID != "" || previous.OwnerUnmatch) {
			value.Title = previous.Title
			value.ProviderID, value.Provider, value.Language, value.Region, value.Confidence, value.OwnerMatch, value.OwnerUnmatch, value.Year, value.Synopsis, value.Poster, value.Backdrop = previous.ProviderID, previous.Provider, previous.Language, previous.Region, previous.Confidence, previous.OwnerMatch, previous.OwnerUnmatch, previous.Year, previous.Synopsis, previous.Poster, previous.Backdrop
			value.LocalOnly = previous.LocalOnly
			if !previous.OwnerUnmatch && provider != nil && token != "" {
				enrichment, enrichmentErr = c.pendingArtwork(identity)
				attempted = enrichmentErr != nil || enrichment.Poster != "" || enrichment.Backdrop != ""
				if !attempted {
					legacyReconciliation, enrichmentErr = c.pendingLegacyArtworkReconciliation(identity)
					attempted = enrichmentErr != nil || legacyReconciliation
					if enrichmentErr == nil && legacyReconciliation {
						if exact, ok := provider.(CandidateProvider); ok {
							enrichment, enrichmentErr = exact.ByID(ctx, token, "series", previous.ProviderID, previous.Language, previous.Region)
						} else {
							enrichmentErr = ErrProviderUnavailable
						}
					}
				}
			}
		} else if provider != nil && token != "" {
			enrichment, enrichmentErr = provider.Lookup(ctx, token, "series", value.Title)
			attempted = true
		}
		if attempted {
			if enrichmentErr != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				observations = append(observations, providerFailure("series:"+id, "provider_failed"))
			} else if legacyReconciliation && enrichment.ProviderID != previous.ProviderID {
				if err := c.completeLegacyArtworkReconciliation(identity); err != nil {
					return nil, nil, err
				}
				observations = append(observations, providerFailure("series:"+id, "unmatched"))
			} else if enrichment.ProviderID == "" {
				observations = append(observations, providerFailure("series:"+id, "unmatched"))
			} else {
				identity = artworkIdentity{catalogKind: "series", catalogID: id, providerID: enrichment.ProviderID}
				if legacyReconciliation {
					enrichment = c.unlockedArtwork("series", id, enrichment)
					enrichment = missingArtwork(enrichment, value.Poster, value.Backdrop)
					if err := c.stageLegacyArtworkRetries(identity, enrichment); err != nil {
						return nil, nil, err
					}
				}
				artworkFailed, err := c.cacheEnrichmentArtwork(ctx, identity, &enrichment, value.Poster, value.Backdrop)
				if err != nil {
					return nil, nil, err
				}
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				if artworkFailed {
					observations = append(observations, providerFailure("series:"+id, "provider_artwork_failed"))
				}
				if value.ProviderID == "" {
					value.LocalOnly = false
					value.Provider = "tmdb"
					value.ProviderID, value.Year, value.Synopsis = enrichment.ProviderID, enrichment.Year, enrichment.Synopsis
					if enrichment.Title != "" {
						value.Title = enrichment.Title
					}
				}
				value.Poster, value.Backdrop = enrichment.Poster, enrichment.Backdrop
			}
		}
		if previous, ok := previousSeries[id]; ok {
			item := Item{ID: value.ID, Title: value.Title, Synopsis: value.Synopsis, Year: value.Year, Poster: value.Poster, Backdrop: value.Backdrop}
			c.applyLockedFields("series", previous.ID, &item)
			value.Title, value.Synopsis, value.Year, value.Poster, value.Backdrop = item.Title, item.Synopsis, item.Year, item.Poster, item.Backdrop
		}
		series[id] = value
	}
	if episodeProvider, ok := provider.(EpisodeProvider); ok && token != "" {
		for id, item := range next {
			parent, found := series[item.SeriesID]
			if item.Kind != "episode" || !found || parent.ProviderID == "" {
				continue
			}
			var enrichment Enrichment
			var err error
			legacyReconciliation := false
			identity := artworkIdentity{catalogKind: "episode", catalogID: id, providerID: item.ProviderID, parentCatalogID: item.SeriesID, parentProviderID: parent.ProviderID}
			if item.ProviderID != "" {
				enrichment, err = c.pendingArtwork(identity)
				if err == nil && enrichment.Poster == "" && enrichment.Backdrop == "" {
					legacyReconciliation, err = c.pendingLegacyArtworkReconciliation(identity)
					if err == nil && !legacyReconciliation {
						continue
					}
					if err == nil {
						enrichment, err = episodeProvider.LookupEpisode(ctx, token, parent.ProviderID, item.Season, item.Episode)
					}
				}
			} else {
				enrichment, err = episodeProvider.LookupEpisode(ctx, token, parent.ProviderID, item.Season, item.Episode)
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				observations = append(observations, providerFailure("episode:"+id, "provider_failed"))
				continue
			}
			if legacyReconciliation && enrichment.ProviderID != item.ProviderID {
				if err := c.completeLegacyArtworkReconciliation(identity); err != nil {
					return nil, nil, err
				}
				observations = append(observations, providerFailure("episode:"+id, "unmatched"))
				continue
			}
			if enrichment.ProviderID == "" {
				observations = append(observations, providerFailure("episode:"+id, "unmatched"))
				continue
			}
			identity = artworkIdentity{catalogKind: "episode", catalogID: id, providerID: enrichment.ProviderID, parentCatalogID: item.SeriesID, parentProviderID: parent.ProviderID}
			if legacyReconciliation {
				enrichment = missingArtwork(enrichment, item.Poster, item.Backdrop)
				if err := c.stageLegacyArtworkRetries(identity, enrichment); err != nil {
					return nil, nil, err
				}
			}
			artworkFailed, err := c.cacheEnrichmentArtwork(ctx, identity, &enrichment, item.Poster, item.Backdrop)
			if err != nil {
				return nil, nil, err
			}
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if artworkFailed {
				observations = append(observations, providerFailure("episode:"+id, "provider_artwork_failed"))
			}
			if item.ProviderID == "" {
				item.LocalOnly = false
				item.Provider = "tmdb"
				item.ProviderID, item.Year, item.Synopsis = enrichment.ProviderID, enrichment.Year, enrichment.Synopsis
				if enrichment.Title != "" {
					item.Title = enrichment.Title
				}
			}
			item.Poster, item.Backdrop = enrichment.Poster, enrichment.Backdrop
			next[id] = item
		}
	}
	for id, value := range previousSeries {
		if _, present := series[id]; !present || value.Demo {
			series[id] = value
		}
	}
	return observations, series, nil
}

func providerFailure(identifier, outcome string) scanObservation {
	message := "metadata provider lookup failed"
	if outcome == "unmatched" {
		message = "metadata provider returned no match"
	} else if outcome == "provider_artwork_failed" {
		message = "metadata provider artwork failed"
	}
	return scanObservation{identifier: identifier, outcome: outcome, message: message}
}

func (c *Catalog) persist(ctx context.Context, next map[string]Item, sources map[scanKey]Item, failures map[scanKey]string, observations []scanObservation, conflicts []identityPair, seriesState map[string]Series) error {
	c.mu.RLock()
	previous := make(map[string]Item, len(c.items))
	for k, v := range c.items {
		previous[k] = v
	}
	status := c.status
	c.mu.RUnlock()
	unavailable := make([]Item, 0)
	for _, old := range previous {
		if old.Demo {
			next[old.ID] = old
			continue
		}
		if _, failed := failures[scanKey{old.rootKind, old.path}]; failed {
			next[old.ID] = old
			continue
		}
		if _, present := next[old.ID]; !present {
			// A clean scan that no longer sees a file must not cascade-delete the
			// logical title and its profile state. Keep the anchor unavailable
			// until a later reconciliation attaches a physical source again.
			old.Playable = false
			next[old.ID] = old
			unavailable = append(unavailable, old)
		}
	}
	if c.db != nil {
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, old := range unavailable {
			if _, err = tx.Exec("UPDATE catalog_physical_files SET present=0,selected=0 WHERE catalog_id=?", old.ID); err != nil {
				return err
			}
			if _, err = tx.Exec("UPDATE catalog_items SET available=0 WHERE id=?", old.ID); err != nil {
				return err
			}
		}
		series := map[string]string{}
		for _, x := range next {
			if x.SeriesID != "" {
				series[x.SeriesID] = seriesTitle(x.path, x.Title)
			}
		}
		for seriesID, seriesTitle := range series {
			value, ok := seriesState[seriesID]
			if !ok {
				value = Series{ID: seriesID, Title: seriesTitle, Kind: "series", LocalOnly: true, AddedAt: time.Now().Unix(), Playable: true}
			}
			if value.Demo {
				continue
			}
			genres, err := json.Marshal(value.Genres)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO catalog_series(id,title,local_only,provider_id,year,synopsis,poster,backdrop,updated_at,genres_json,added_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0) ON CONFLICT(id) DO UPDATE SET title=excluded.title,local_only=excluded.local_only,provider_id=excluded.provider_id,year=excluded.year,synopsis=excluded.synopsis,poster=excluded.poster,backdrop=excluded.backdrop,updated_at=excluded.updated_at,genres_json=excluded.genres_json,playable=excluded.playable`, value.ID, value.Title, boolInt(value.LocalOnly), value.ProviderID, value.Year, value.Synopsis, value.Poster, value.Backdrop, time.Now().Unix(), string(genres), value.AddedAt, boolInt(value.Playable)); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE catalog_series SET metadata_provider=?,metadata_language=?,metadata_region=?,match_confidence=?,owner_matched=?,owner_unmatched=? WHERE id=?`, value.Provider, value.Language, value.Region, value.Confidence, boolInt(value.OwnerMatch), boolInt(value.OwnerUnmatch), value.ID); err != nil {
				return err
			}
		}
		for _, x := range next {
			if x.Demo {
				continue
			}
			genres, err := json.Marshal(x.Genres)
			if err != nil {
				return err
			}
			audio, err := json.Marshal(x.Audio)
			if err != nil {
				return err
			}
			subtitles, err := json.Marshal(x.Subtitles)
			if err != nil {
				return err
			}
			seasonID := ""
			if x.SeriesID != "" {
				seasonID = id("season", x.SeriesID+fmt.Sprintf("/%d", x.Season))
				if _, err = tx.Exec(`INSERT INTO catalog_seasons(id,series_id,number) VALUES(?,?,?) ON CONFLICT(series_id,number) DO NOTHING`, seasonID, x.SeriesID, x.Season); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,fingerprint,size_bytes,mtime_unix,container,duration_ms,video_codec,video_profile,primary_video_stream_index,video_width,video_height,video_bitrate,video_hdr,audio_json,subtitle_json,probe_revision,series_id,season_id,provider_id,year,synopsis,poster,backdrop,updated_at,genres_json,added_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0) ON CONFLICT(id) DO UPDATE SET title=excluded.title,fingerprint=excluded.fingerprint,root_kind=excluded.root_kind,relative_path=excluded.relative_path,local_only=excluded.local_only,size_bytes=excluded.size_bytes,mtime_unix=excluded.mtime_unix,container=excluded.container,duration_ms=excluded.duration_ms,video_codec=excluded.video_codec,video_profile=excluded.video_profile,primary_video_stream_index=excluded.primary_video_stream_index,video_width=excluded.video_width,video_height=excluded.video_height,video_bitrate=excluded.video_bitrate,video_hdr=excluded.video_hdr,audio_json=excluded.audio_json,subtitle_json=excluded.subtitle_json,probe_revision=excluded.probe_revision,series_id=excluded.series_id,season_id=excluded.season_id,provider_id=excluded.provider_id,year=excluded.year,synopsis=excluded.synopsis,poster=excluded.poster,backdrop=excluded.backdrop,updated_at=excluded.updated_at,genres_json=excluded.genres_json,playable=excluded.playable`, x.ID, x.Kind, x.Title, x.path, boolInt(x.LocalOnly), x.rootKind, x.fingerprint, x.size, x.mtime, x.Container, x.DurationMS, x.VideoCodec, x.VideoProfile, x.PrimaryVideoStreamIndex, x.Width, x.Height, x.Bitrate, x.HDR, string(audio), string(subtitles), x.probeRevision, x.SeriesID, seasonID, x.ProviderID, x.Year, x.Synopsis, x.Poster, x.Backdrop, time.Now().Unix(), string(genres), x.AddedAt, boolInt(x.Playable)); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE catalog_items SET metadata_provider=?,metadata_language=?,metadata_region=?,match_confidence=?,owner_matched=?,owner_unmatched=? WHERE id=?`, x.Provider, x.Language, x.Region, x.Confidence, boolInt(x.OwnerMatch), boolInt(x.OwnerUnmatch), x.ID); err != nil {
				return err
			}
		}
		presentSources := map[string]bool{}
		for _, source := range sources {
			if source.Playable {
				presentSources[source.ID] = true
			}
		}
		for catalogID := range presentSources {
			if _, err = tx.Exec("UPDATE catalog_physical_files SET present=0,selected=0 WHERE catalog_id=?", catalogID); err != nil {
				return err
			}
		}
		for _, source := range sources {
			if !source.Playable {
				continue
			}
			logical, ok := next[source.ID]
			if !ok {
				return fmt.Errorf("physical source has no logical title: %s", source.ID)
			}
			selected := logical.Playable && logical.rootKind == source.rootKind && logical.path == source.path
			physicalID := id("physical", source.rootKind+"\x00"+source.path+"\x00"+source.digest+"\x00"+source.changeToken)
			if _, err = tx.Exec(`INSERT INTO catalog_physical_files(id,catalog_id,root_kind,relative_path,fingerprint,size_bytes,mtime_unix,full_digest,change_token,source_series_id,last_seen,present,selected) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET catalog_id=excluded.catalog_id,fingerprint=excluded.fingerprint,size_bytes=excluded.size_bytes,mtime_unix=excluded.mtime_unix,full_digest=excluded.full_digest,change_token=excluded.change_token,source_series_id=excluded.source_series_id,last_seen=excluded.last_seen,present=1,selected=excluded.selected`, physicalID, source.ID, source.rootKind, source.path, source.fingerprint, source.size, source.mtime, source.digest, source.changeToken, source.sourceSeriesID, time.Now().UnixNano(), 1, boolInt(selected)); err != nil {
				return err
			}
			if selected {
				if _, err = tx.Exec("UPDATE catalog_items SET primary_file_id=?,available=1 WHERE id=?", physicalID, source.ID); err != nil {
					return err
				}
			}
		}
		if err := persistIdentityConflicts(tx, conflicts); err != nil {
			return err
		}
		if err := detectProviderConflicts(tx); err != nil {
			return err
		}
		for oldID := range previous {
			if _, keep := next[oldID]; !keep {
				if _, err = tx.Exec("DELETE FROM catalog_items WHERE id=?", oldID); err != nil {
					return err
				}
			}
		}
		if _, err = tx.Exec(`DELETE FROM catalog_seasons WHERE NOT EXISTS (SELECT 1 FROM catalog_items WHERE catalog_items.season_id=catalog_seasons.id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind IN ('film','episode') AND NOT EXISTS (SELECT 1 FROM catalog_items WHERE catalog_items.id=catalog_artwork_retries.catalog_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind='series' AND NOT EXISTS (SELECT 1 FROM catalog_series WHERE catalog_series.id=catalog_artwork_retries.catalog_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind IN ('film','episode') AND NOT EXISTS (SELECT 1 FROM catalog_items WHERE catalog_items.id=catalog_artwork_retries.catalog_id AND catalog_items.provider_id=catalog_artwork_retries.provider_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind='series' AND NOT EXISTS (SELECT 1 FROM catalog_series WHERE catalog_series.id=catalog_artwork_retries.catalog_id AND catalog_series.provider_id=catalog_artwork_retries.provider_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_retries WHERE catalog_kind='episode' AND NOT EXISTS (SELECT 1 FROM catalog_items JOIN catalog_series ON catalog_series.id=catalog_items.series_id WHERE catalog_items.id=catalog_artwork_retries.catalog_id AND catalog_items.series_id=catalog_artwork_retries.parent_catalog_id AND catalog_series.provider_id=catalog_artwork_retries.parent_provider_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind IN ('film','episode') AND NOT EXISTS (SELECT 1 FROM catalog_items WHERE catalog_items.id=catalog_artwork_reconciliations.catalog_id AND catalog_items.provider_id=catalog_artwork_reconciliations.provider_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind='series' AND NOT EXISTS (SELECT 1 FROM catalog_series WHERE catalog_series.id=catalog_artwork_reconciliations.catalog_id AND catalog_series.provider_id=catalog_artwork_reconciliations.provider_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM catalog_artwork_reconciliations WHERE catalog_kind='episode' AND NOT EXISTS (SELECT 1 FROM catalog_items JOIN catalog_series ON catalog_series.id=catalog_items.series_id WHERE catalog_items.id=catalog_artwork_reconciliations.catalog_id AND catalog_items.series_id=catalog_artwork_reconciliations.parent_catalog_id AND catalog_series.provider_id=catalog_artwork_reconciliations.parent_provider_id)`); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM scan_files WHERE scan_id=?", status.ID); err != nil {
			return err
		}
		for key, message := range failures {
			if _, err = tx.Exec("INSERT INTO scan_files(scan_id,relative_path,outcome,message) VALUES(?,?,?,?)", status.ID, key.kind+":"+key.rel, "failed", message); err != nil {
				return err
			}
		}
		for _, observation := range observations {
			if _, err = tx.Exec("INSERT INTO scan_files(scan_id,relative_path,outcome,message) VALUES(?,?,?,?)", status.ID, observation.identifier, observation.outcome, observation.message); err != nil {
				return err
			}
		}
		if err := applyIdentityMappings(tx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.items = next
	c.series = seriesState
	c.refreshSeriesAvailability()
	if c.db != nil {
		rows, err := c.db.Query("SELECT id,series_id,playable FROM catalog_items")
		if err == nil {
			for rows.Next() {
				var id, series string
				var playable bool
				if rows.Scan(&id, &series, &playable) == nil {
					item := c.items[id]
					item.SeriesID = series
					item.Playable = playable
					c.items[id] = item
				}
			}
			rows.Close()
		}
	}
	c.mu.Unlock()
	return nil
}

func (c *Catalog) saveStatus(status ScanStatus) {
	if c.db == nil {
		return
	}
	tx, err := c.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO scan_runs(id,started_at,finished_at,status,scanned,failed,unmatched,message) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET finished_at=excluded.finished_at,status=excluded.status,scanned=excluded.scanned,failed=excluded.failed,unmatched=excluded.unmatched,message=excluded.message`, status.ID, status.StartedAt, nullableTime(status.FinishedAt), status.Status, status.Scanned, status.Failed, status.Unmatched, status.Message); err != nil {
		return
	}
	if _, err := tx.Exec(`DELETE FROM scan_runs
		WHERE id<>?
		AND id NOT IN (
			SELECT id FROM scan_runs WHERE id<>?
			ORDER BY started_at DESC,id DESC LIMIT ?
		)`, status.ID, status.ID, scanHistoryLimit-1); err != nil {
		return
	}
	_ = tx.Commit()
}
func nullableTime(t int64) any {
	if t == 0 {
		return nil
	}
	return t
}
func (c *Catalog) lastStatus() ScanStatus {
	var s ScanStatus
	_ = c.db.QueryRow(`SELECT id,started_at,COALESCE(finished_at,0),status,scanned,failed,unmatched,message FROM scan_runs ORDER BY started_at DESC LIMIT 1`).Scan(&s.ID, &s.StartedAt, &s.FinishedAt, &s.Status, &s.Scanned, &s.Failed, &s.Unmatched, &s.Message)
	return s
}
func (c *Catalog) ScanStatus() ScanStatus { c.mu.RLock(); defer c.mu.RUnlock(); return c.status }
func (c *Catalog) Cancel() {
	c.mu.RLock()
	cancel := c.cancel
	c.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}
func (c *Catalog) Shutdown(ctx context.Context) error {
	c.mu.RLock()
	cancel, done, maintenanceCancel, maintenanceDone := c.cancel, c.done, c.maintenanceCancel, c.maintenanceDone
	c.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if maintenanceCancel != nil {
		maintenanceCancel()
	}
	for _, wait := range []chan struct{}{done, maintenanceDone} {
		if wait == nil {
			continue
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *Catalog) List(query string, offset, limit int) ([]Item, error) {
	if offset < 0 {
		return []Item{}, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.db == nil {
		return c.listMemory(query, offset, limit), nil
	}
	sqlLimit := limit
	if sqlLimit <= 0 {
		sqlLimit = -1
	}
	rows, err := c.db.Query(`SELECT id,kind,title,relative_path,local_only,root_kind,fingerprint,size_bytes,mtime_unix,container,duration_ms,video_codec,video_profile,primary_video_stream_index,video_width,video_height,video_bitrate,video_hdr,audio_json,subtitle_json,genres_json,added_at,playable,demo FROM catalog_items WHERE merged_into='' AND title LIKE '%' || ? || '%' ESCAPE '\' COLLATE NOCASE ORDER BY title COLLATE NOCASE,id LIMIT ? OFFSET ?`, likeLiteral(query), sqlLimit, offset)
	if err != nil {
		return nil, fmt.Errorf("list catalog: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var x Item
		var local, playable, demo int
		var audio, subtitles, genres string
		if err := rows.Scan(&x.ID, &x.Kind, &x.Title, &x.path, &local, &x.rootKind, &x.fingerprint, &x.size, &x.mtime, &x.Container, &x.DurationMS, &x.VideoCodec, &x.VideoProfile, &x.PrimaryVideoStreamIndex, &x.Width, &x.Height, &x.Bitrate, &x.HDR, &audio, &subtitles, &genres, &x.AddedAt, &playable, &demo); err != nil {
			return nil, fmt.Errorf("scan catalog item: %w", err)
		}
		if json.Unmarshal([]byte(audio), &x.Audio) != nil || json.Unmarshal([]byte(subtitles), &x.Subtitles) != nil || json.Unmarshal([]byte(genres), &x.Genres) != nil {
			return nil, errors.New("decode catalog media properties")
		}
		x.LocalOnly, x.Playable, x.Demo = local != 0, playable != 0, demo != 0
		episodeFields(&x)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog: %w", err)
	}
	return out, nil
}

func (c *Catalog) listMemory(query string, offset, limit int) []Item {
	out := []Item{}
	for _, v := range c.items {
		if query == "" || strings.Contains(strings.ToLower(v.Title), strings.ToLower(query)) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title == out[j].Title {
			return out[i].ID < out[j].ID
		}
		return out[i].Title < out[j].Title
	})
	if offset >= len(out) {
		return []Item{}
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
func (c *Catalog) Item(itemID string) (Item, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if item, ok := c.items[itemID]; ok {
		return item, true
	}
	series, ok := c.series[itemID]
	if !ok {
		return Item{}, false
	}
	return Item{ID: series.ID, Title: series.Title, Kind: "series", LocalOnly: series.LocalOnly, ProviderID: series.ProviderID, Year: series.Year, Synopsis: series.Synopsis, Poster: series.Poster, Backdrop: series.Backdrop, Genres: series.Genres, AddedAt: series.AddedAt, Playable: series.Playable, Demo: series.Demo}, true
}
func (c *Catalog) Open(id string) (*os.File, error) {
	c.mu.RLock()
	x, ok := c.items[id]
	root := c.film
	if x.rootKind == "episode" {
		root = c.tv
	}
	c.mu.RUnlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Open(x.path)
}
