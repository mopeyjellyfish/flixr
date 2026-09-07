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

	path          string
	rootKind      string
	fingerprint   string
	size          int64
	mtime         int64
	probeRevision int
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
	derivativeBytes   int64
	derivativeCount   int
	derivativeReady   bool
}

func (c *Catalog) ArtworkMaintenanceStatus() ArtworkMaintenanceStatus {
	c.artworkMu.Lock()
	defer c.artworkMu.Unlock()
	return c.maintenanceStatus
}

func New() *Catalog {
	return &Catalog{items: map[string]Item{}, series: map[string]Series{}, prober: newFFprobe(), fs: afero.NewOsFs()}
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
	c := &Catalog{db: db, items: map[string]Item{}, series: map[string]Series{}, prober: prober, provider: NewTMDB(nil), fs: fs}
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
		seriesFields(&x)
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
	c.scanning, c.cancel = false, nil
	close(c.done)
	c.done = nil
	c.mu.Unlock()
	c.saveStatus(status)
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
			files = append(files, scanFile{root: r.path, kind: r.kind, rel: filepath.ToSlash(rel), size: info.Size(), mtime: info.ModTime().UnixNano()})
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
			if old, ok := previousByPath[scanKey{f.kind, f.rel}]; ok && old.size == f.size && old.mtime == f.mtime && old.probeRevision == mediaProbeRevision {
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
	next := make(map[string]Item, len(files))
	failures := map[scanKey]string{}
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
		// Sorted discovery plus overwrite makes duplicate content deterministic.
		if old, duplicate := next[result.item.ID]; !duplicate || result.file.rel < old.path {
			next[result.item.ID] = result.item
		}
	}
	if err := <-wait; err != nil {
		return err
	}
	observations, err := c.enrich(ctx, next)
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
	return c.persist(next, failures, observations)
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
	properties, err := c.prober.Probe(ctx, file)
	if err != nil {
		return Item{}, err
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
	x := Item{ID: id(f.kind, fingerprint), Title: title(f.rel), Kind: f.kind, LocalOnly: true, AddedAt: time.Now().Unix(), Playable: true, MediaProperties: properties, path: f.rel, rootKind: f.kind, fingerprint: fingerprint, size: f.size, mtime: f.mtime, probeRevision: mediaProbeRevision}
	episodeFields(&x)
	seriesFields(&x)
	return x, nil
}

func (c *Catalog) enrich(ctx context.Context, next map[string]Item) ([]scanObservation, error) {
	c.mu.RLock()
	provider, token := c.provider, c.token
	previousSeries := make(map[string]Series, len(c.series))
	for id, series := range c.series {
		previousSeries[id] = series
	}
	previousItems := make(map[scanKey]Item, len(c.items))
	for _, item := range c.items {
		previousItems[scanKey{item.rootKind, item.path}] = item
	}
	c.mu.RUnlock()
	var observations []scanObservation
	for id, item := range next {
		if previous, ok := previousItems[scanKey{item.rootKind, item.path}]; ok {
			c.applyLockedFields("film", previous.ID, &item)
		}
		if previous, ok := previousItems[scanKey{item.rootKind, item.path}]; ok && (previous.OwnerMatch || previous.OwnerUnmatch) {
			item.ProviderID, item.Provider, item.Language, item.Region, item.Confidence, item.OwnerMatch, item.OwnerUnmatch, item.Year, item.Synopsis, item.Poster, item.Backdrop, item.LocalOnly = previous.ProviderID, previous.Provider, previous.Language, previous.Region, previous.Confidence, previous.OwnerMatch, previous.OwnerUnmatch, previous.Year, previous.Synopsis, previous.Poster, previous.Backdrop, previous.LocalOnly
			next[id] = item
		}
	}
	if provider != nil && token != "" {
		for id, item := range next {
			if item.Kind != "film" || item.ProviderID != "" || item.OwnerUnmatch {
				continue
			}
			enrichment, err := provider.Lookup(ctx, token, "film", item.Title)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				observations = append(observations, providerFailure("film:"+id, "provider_failed"))
				continue
			}
			if enrichment.ProviderID == "" {
				observations = append(observations, providerFailure("film:"+id, "unmatched"))
				continue
			}
			artworkFailed := c.cacheEnrichmentArtwork(ctx, id, &enrichment)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if artworkFailed {
				observations = append(observations, providerFailure("film:"+id, "provider_artwork_failed"))
			}
			item.LocalOnly = false
			item.ProviderID, item.Year, item.Synopsis, item.Poster, item.Backdrop = enrichment.ProviderID, enrichment.Year, enrichment.Synopsis, enrichment.Poster, enrichment.Backdrop
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
		if previous, ok := previousSeries[id]; ok && (previous.ProviderID != "" || previous.OwnerUnmatch) {
			value.ProviderID, value.Provider, value.Language, value.Region, value.Confidence, value.OwnerMatch, value.OwnerUnmatch, value.Year, value.Synopsis, value.Poster, value.Backdrop = previous.ProviderID, previous.Provider, previous.Language, previous.Region, previous.Confidence, previous.OwnerMatch, previous.OwnerUnmatch, previous.Year, previous.Synopsis, previous.Poster, previous.Backdrop
			value.LocalOnly = previous.LocalOnly
		} else if provider != nil && token != "" {
			enrichment, err := provider.Lookup(ctx, token, "series", value.Title)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				observations = append(observations, providerFailure("series:"+id, "provider_failed"))
			} else if enrichment.ProviderID == "" {
				observations = append(observations, providerFailure("series:"+id, "unmatched"))
			} else {
				artworkFailed := c.cacheEnrichmentArtwork(ctx, id, &enrichment)
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if artworkFailed {
					observations = append(observations, providerFailure("series:"+id, "provider_artwork_failed"))
				}
				value.LocalOnly = false
				value.ProviderID, value.Year, value.Synopsis, value.Poster, value.Backdrop = enrichment.ProviderID, enrichment.Year, enrichment.Synopsis, enrichment.Poster, enrichment.Backdrop
			}
		}
		series[id] = value
	}
	for id, value := range previousSeries {
		if value.Demo {
			series[id] = value
		}
	}
	c.mu.Lock()
	c.series = series
	c.mu.Unlock()
	return observations, nil
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

func (c *Catalog) persist(next map[string]Item, failures map[scanKey]string, observations []scanObservation) error {
	c.mu.RLock()
	previous := make(map[string]Item, len(c.items))
	for k, v := range c.items {
		previous[k] = v
	}
	status := c.status
	seriesState := make(map[string]Series, len(c.series))
	for id, series := range c.series {
		seriesState[id] = series
	}
	c.mu.RUnlock()
	for _, old := range previous {
		if old.Demo {
			next[old.ID] = old
			continue
		}
		if _, failed := failures[scanKey{old.rootKind, old.path}]; failed {
			next[old.ID] = old
		}
	}
	if c.db != nil {
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
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
			if _, err = tx.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,fingerprint,size_bytes,mtime_unix,container,duration_ms,video_codec,video_profile,primary_video_stream_index,video_width,video_height,video_bitrate,video_hdr,audio_json,subtitle_json,probe_revision,series_id,season_id,provider_id,year,synopsis,poster,backdrop,updated_at,genres_json,added_at,playable,demo) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0) ON CONFLICT(id) DO UPDATE SET title=excluded.title,relative_path=excluded.relative_path,local_only=excluded.local_only,size_bytes=excluded.size_bytes,mtime_unix=excluded.mtime_unix,container=excluded.container,duration_ms=excluded.duration_ms,video_codec=excluded.video_codec,video_profile=excluded.video_profile,primary_video_stream_index=excluded.primary_video_stream_index,video_width=excluded.video_width,video_height=excluded.video_height,video_bitrate=excluded.video_bitrate,video_hdr=excluded.video_hdr,audio_json=excluded.audio_json,subtitle_json=excluded.subtitle_json,probe_revision=excluded.probe_revision,series_id=excluded.series_id,season_id=excluded.season_id,provider_id=excluded.provider_id,year=excluded.year,synopsis=excluded.synopsis,poster=excluded.poster,backdrop=excluded.backdrop,updated_at=excluded.updated_at,genres_json=excluded.genres_json,playable=excluded.playable`, x.ID, x.Kind, x.Title, x.path, boolInt(x.LocalOnly), x.rootKind, x.fingerprint, x.size, x.mtime, x.Container, x.DurationMS, x.VideoCodec, x.VideoProfile, x.PrimaryVideoStreamIndex, x.Width, x.Height, x.Bitrate, x.HDR, string(audio), string(subtitles), x.probeRevision, x.SeriesID, seasonID, x.ProviderID, x.Year, x.Synopsis, x.Poster, x.Backdrop, time.Now().Unix(), string(genres), x.AddedAt, boolInt(x.Playable)); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE catalog_items SET metadata_provider=?,metadata_language=?,metadata_region=?,match_confidence=?,owner_matched=?,owner_unmatched=? WHERE id=?`, x.Provider, x.Language, x.Region, x.Confidence, boolInt(x.OwnerMatch), boolInt(x.OwnerUnmatch), x.ID); err != nil {
				return err
			}
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
		if _, err = tx.Exec(`DELETE FROM catalog_series WHERE NOT EXISTS (SELECT 1 FROM catalog_seasons WHERE catalog_seasons.series_id=catalog_series.id)`); err != nil {
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
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.items = next
	c.mu.Unlock()
	return nil
}

func (c *Catalog) saveStatus(status ScanStatus) {
	if c.db == nil {
		return
	}
	_, _ = c.db.Exec(`INSERT INTO scan_runs(id,started_at,finished_at,status,scanned,failed,unmatched,message) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET finished_at=excluded.finished_at,status=excluded.status,scanned=excluded.scanned,failed=excluded.failed,unmatched=excluded.unmatched,message=excluded.message`, status.ID, status.StartedAt, nullableTime(status.FinishedAt), status.Status, status.Scanned, status.Failed, status.Unmatched, status.Message)
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
	rows, err := c.db.Query(`SELECT id,kind,title,relative_path,local_only,root_kind,fingerprint,size_bytes,mtime_unix,container,duration_ms,video_codec,video_profile,primary_video_stream_index,video_width,video_height,video_bitrate,video_hdr,audio_json,subtitle_json,genres_json,added_at,playable,demo FROM catalog_items WHERE title LIKE '%' || ? || '%' ESCAPE '\' COLLATE NOCASE ORDER BY title COLLATE NOCASE,id LIMIT ? OFFSET ?`, likeLiteral(query), sqlLimit, offset)
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
