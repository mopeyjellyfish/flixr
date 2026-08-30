package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

var (
	assetName         = regexp.MustCompile(`^(?:index\.m3u8|init\.mp4|segment-[0-9]+\.m4s)$`)
	generationDirName = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
)

type ManagerConfig struct {
	Settings     Settings
	FS           afero.Fs
	DB           *sqlite.DB
	InputBase    string
	Executor     Executor
	SaveProgress func(profileID, catalogID string, positionMS int64) error
	ManifestWait time.Duration
}

type inputAuthority struct {
	catalogID string
	expiresAt time.Time
}

type generation struct {
	id              string
	jobKey          string
	kind            Kind
	startMS         int64
	dir             string
	input           string
	leases          map[string]time.Time
	process         Process
	done            chan struct{}
	completed       bool
	ready           bool
	startedAt       time.Time
	log             *boundedLog
	mediaSequence   uint64
	mediaOffsetMS   int64
	segmentDuration map[uint64]int64
}

type boundedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *boundedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	const limit = 64 << 10
	original := len(p)
	if l.buf.Len() < limit {
		remaining := limit - l.buf.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = l.buf.Write(p)
	}
	return original, nil
}

func (l *boundedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// GenerationStatus is the owner-visible, path-free process and lease summary.
type GenerationStatus struct {
	ID        string `json:"id"`
	CatalogID string `json:"catalog_id"`
	Kind      Kind   `json:"kind"`
	StartMS   int64  `json:"start_ms"`
	Leases    int    `json:"leases"`
	Bytes     int64  `json:"bytes"`
	Running   bool   `json:"running"`
	StartedAt int64  `json:"started_at"`
}

type Status struct {
	Settings    Settings           `json:"settings"`
	Generations []GenerationStatus `json:"generations"`
}

// Manager owns profile-bound sessions, FFmpeg generations, leases and temporary output.
type Manager struct {
	mu           sync.Mutex
	settings     Settings
	db           *sqlite.DB
	files        afero.Afero
	inputBase    string
	saveProgress func(profileID, catalogID string, positionMS int64) error
	executor     Executor
	manifestWait time.Duration
	sessions     map[string]Session
	generations  map[string]*generation
	inputs       map[string]inputAuthority
	starting     int
	pendingJobs  map[string]struct{}
	startWG      sync.WaitGroup
	cancel       context.CancelFunc
	janitorDone  chan struct{}
	closed       bool
}

// NewDirectManager creates a no-goroutine manager for direct-only tests and callers.
func NewDirectManager() *Manager {
	return &Manager{
		settings:    DefaultSettings(os.TempDir()),
		files:       afero.Afero{Fs: afero.NewOsFs()},
		sessions:    map[string]Session{},
		generations: map[string]*generation{},
		inputs:      map[string]inputAuthority{},
		pendingJobs: map[string]struct{}{},
	}
}

// NewManager performs orphan cleanup and starts the bounded lease janitor.
// The caller must hold the segment-directory lock before calling it.
func NewManager(config ManagerConfig) (*Manager, error) {
	if err := config.Settings.Validate(); err != nil {
		return nil, err
	}
	if config.Executor == nil {
		return nil, errors.New("playback executor is nil")
	}
	base, err := normalizeInputBase(config.InputBase)
	if err != nil {
		return nil, err
	}
	fs := config.FS
	if fs == nil {
		fs = afero.NewOsFs()
	}
	files := afero.Afero{Fs: fs}
	if err := claimSegmentDir(fs, config.Settings.SegmentDir); err != nil {
		return nil, err
	}
	if err := CleanupOrphans(fs, config.Settings.SegmentDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		settings:     config.Settings,
		db:           config.DB,
		files:        files,
		inputBase:    base,
		executor:     config.Executor,
		manifestWait: config.ManifestWait,
		sessions:     map[string]Session{},
		saveProgress: config.SaveProgress,
		generations:  map[string]*generation{},
		inputs:       map[string]inputAuthority{},
		pendingJobs:  map[string]struct{}{},
		cancel:       cancel,
		janitorDone:  make(chan struct{}),
	}
	go manager.janitor(ctx)
	return manager, nil
}

func normalizeInputBase(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("playback input base must be an HTTP loopback origin")
	}
	return value, nil
}

func CleanupOrphans(fs afero.Fs, segmentDir string) error {
	if err := claimSegmentDir(fs, segmentDir); err != nil {
		return err
	}
	files := afero.Afero{Fs: fs}
	entries, err := files.ReadDir(segmentDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read segment directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !generationDirName.MatchString(entry.Name()) {
			continue
		}
		if err := files.RemoveAll(filepath.Join(segmentDir, entry.Name())); err != nil {
			return fmt.Errorf("remove orphan generation %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (m *Manager) Create(profileID, catalogID string, plan Plan, positionMS int64) (Session, error) {
	if profileID == "" || catalogID == "" {
		return Session{}, ErrSessionInvalid
	}
	if positionMS < 0 {
		positionMS = 0
	}
	now := time.Now()
	sessionID, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	if plan.Kind == Direct {
		session := Session{ID: sessionID, ProfileID: profileID, CatalogID: catalogID, Plan: plan, PositionMS: positionMS, ExpiresAt: now.Add(directSessionTTL)}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			return Session{}, ErrSessionInvalid
		}
		m.sessions[session.ID] = session
		return session, nil
	}
	if plan.Kind != Remux && plan.Kind != Transcode {
		return Session{}, ErrUnsupported
	}
	if m.executor == nil {
		return Session{}, ErrFFmpegUnavailable
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	jobKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s", catalogID, plan.Kind, plan.VideoCodec, plan.AudioCodec)
	if existing := m.shareableGenerationLocked(jobKey, positionMS); existing != nil {
		// Media timestamps stay relative to the generation start when the HLS
		// playlist slides. The retained start is only an admission boundary.
		session := Session{ID: sessionID, ProfileID: profileID, CatalogID: catalogID, Plan: plan, PositionMS: positionMS, StreamOffsetMS: existing.startMS, GenerationID: existing.id, ExpiresAt: now.Add(m.settings.LeaseTTL)}
		existing.leases[session.ID] = session.ExpiresAt
		m.sessions[session.ID] = session
		if authority, ok := m.inputs[existing.input]; ok {
			authority.expiresAt = session.ExpiresAt
			m.inputs[existing.input] = authority
		}
		m.mu.Unlock()
		return session, nil
	}
	if _, pending := m.pendingJobs[jobKey]; pending {
		m.mu.Unlock()
		return Session{}, ErrPreparing
	}
	reserved := len(m.generations) + m.starting + 1
	if reserved > m.settings.MaxGenerations || int64(reserved)*m.settings.GenerationBytes > m.settings.GlobalBytes {
		m.mu.Unlock()
		return Session{}, ErrCapacity
	}
	settings := m.settings
	m.starting++
	m.pendingJobs[jobKey] = struct{}{}
	m.startWG.Add(1)
	m.mu.Unlock()
	defer m.startWG.Done()

	releaseReservation := func(clearPending bool) {
		m.mu.Lock()
		m.starting--
		if clearPending {
			delete(m.pendingJobs, jobKey)
		}
		m.mu.Unlock()
	}
	generationID, err := randomToken()
	if err != nil {
		releaseReservation(true)
		return Session{}, err
	}
	inputToken, err := randomToken()
	if err != nil {
		releaseReservation(true)
		return Session{}, err
	}
	dir := filepath.Join(settings.SegmentDir, generationID)
	if err := m.files.Mkdir(dir, 0o700); err != nil {
		releaseReservation(true)
		return Session{}, fmt.Errorf("create generation directory: %w", err)
	}
	inputURL := m.inputBase + "/api/v1/playback/input/" + inputToken
	name, args, err := ffmpegCommand(plan.Kind, inputURL, dir, time.Duration(positionMS)*time.Millisecond, settings.SegmentWindow)
	if err != nil {
		releaseReservation(true)
		_ = m.files.RemoveAll(dir)
		return Session{}, err
	}
	log := &boundedLog{}
	process, err := m.executor.Start(name, args, log)
	if err != nil {
		releaseReservation(true)
		_ = m.files.RemoveAll(dir)
		return Session{}, fmt.Errorf("start FFmpeg: %w", err)
	}
	session := Session{ID: sessionID, ProfileID: profileID, CatalogID: catalogID, Plan: plan, PositionMS: positionMS, StreamOffsetMS: positionMS, GenerationID: generationID, ExpiresAt: now.Add(settings.LeaseTTL)}
	gen := &generation{
		id: generationID, jobKey: jobKey, kind: plan.Kind, startMS: positionMS, dir: dir,
		input: inputToken, leases: map[string]time.Time{session.ID: session.ExpiresAt},
		process: process, done: make(chan struct{}), startedAt: now, log: log,
		ready:           m.manifestWait == 0,
		segmentDuration: map[uint64]int64{},
	}
	go m.waitGeneration(gen, process)
	m.mu.Lock()
	m.starting--
	if m.closed {
		delete(m.pendingJobs, jobKey)
		m.mu.Unlock()
		m.retireGeneration(context.Background(), gen)
		return Session{}, ErrSessionInvalid
	}
	m.generations[generationID] = gen
	m.sessions[session.ID] = session
	m.inputs[inputToken] = inputAuthority{catalogID: catalogID, expiresAt: now.Add(settings.LeaseTTL)}
	m.mu.Unlock()
	if m.manifestWait > 0 {
		if err := waitForFile(m.files.Fs, filepath.Join(dir, "index.m3u8"), gen.done, m.manifestWait); err != nil {
			_ = m.Stop(session.ID, profileID)
			m.mu.Lock()
			delete(m.pendingJobs, jobKey)
			m.mu.Unlock()
			return Session{}, fmt.Errorf("prepare HLS manifest: %w; ffmpeg: %s", err, gen.log.String())
		}
	}
	m.mu.Lock()
	if current := m.generations[gen.id]; current == gen {
		gen.ready = true
	}
	delete(m.pendingJobs, jobKey)
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) shareableGenerationLocked(jobKey string, positionMS int64) *generation {
	for _, gen := range m.generations {
		start, end, ok := m.retainedRangeLocked(gen)
		if gen.jobKey == jobKey && ok && positionMS >= start && positionMS <= end {
			return gen
		}
	}
	return nil
}

func (m *Manager) retainedRangeLocked(gen *generation) (int64, int64, bool) {
	if gen == nil || !gen.ready {
		return 0, 0, false
	}
	sequence, durations, ok := readRetainedManifest(m.files.Fs, filepath.Join(gen.dir, "index.m3u8"))
	if !ok {
		return 0, 0, false
	}
	return applyRetainedRange(gen, sequence, durations)
}

func readRetainedManifest(fs afero.Fs, path string) (uint64, []int64, bool) {
	manifest, err := afero.ReadFile(fs, path)
	if err != nil {
		return 0, nil, false
	}
	var sequence uint64
	var foundSequence bool
	var durations []int64
	for _, line := range strings.Split(string(manifest), "\n") {
		if value, found := strings.CutPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"); found {
			sequence, err = strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			foundSequence = err == nil
		}
		if value, found := strings.CutPrefix(line, "#EXTINF:"); found {
			seconds, parseErr := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), ","), 64)
			if parseErr != nil || seconds <= 0 {
				return 0, nil, false
			}
			durations = append(durations, int64(math.Round(seconds*1000)))
		}
	}
	return sequence, durations, foundSequence && len(durations) > 0
}

func applyRetainedRange(gen *generation, sequence uint64, durations []int64) (int64, int64, bool) {
	if sequence < gen.mediaSequence {
		return 0, 0, false
	}
	if gen.segmentDuration == nil {
		gen.segmentDuration = map[uint64]int64{}
	}
	for index, duration := range durations {
		gen.segmentDuration[sequence+uint64(index)] = duration
	}
	for gen.mediaSequence < sequence {
		duration, ok := gen.segmentDuration[gen.mediaSequence]
		if !ok {
			return 0, 0, false
		}
		gen.mediaOffsetMS += duration
		delete(gen.segmentDuration, gen.mediaSequence)
		gen.mediaSequence++
	}
	start := gen.startMS + gen.mediaOffsetMS
	end := start
	for _, duration := range durations {
		end += duration
	}
	return start, end, true
}

func waitForFile(fs afero.Fs, path string, processDone <-chan struct{}, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := fs.Stat(path); err == nil && info.Size() > 0 {
			return nil
		}
		select {
		case <-processDone:
			if info, err := fs.Stat(path); err == nil && info.Size() > 0 {
				return nil
			}
			return errors.New("FFmpeg exited before writing a manifest")
		case <-deadline.C:
			return errors.New("timed out waiting for FFmpeg")
		case <-ticker.C:
		}
	}
}

func (m *Manager) waitGeneration(gen *generation, process Process) {
	_ = process.Wait()
	m.mu.Lock()
	gen.completed = true
	select {
	case <-gen.done:
	default:
		close(gen.done)
	}
	m.mu.Unlock()
}

func (m *Manager) Lookup(id, profileID string, touch bool) (Session, bool) {
	now := time.Now()
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || session.ProfileID != profileID || !now.Before(session.ExpiresAt) {
		m.mu.Unlock()
		if ok && session.ProfileID == profileID {
			_ = m.Stop(id, profileID)
		}
		return Session{}, false
	}
	if touch {
		ttl := directSessionTTL
		if session.GenerationID != "" {
			ttl = m.settings.LeaseTTL
		}
		session.ExpiresAt = now.Add(ttl)
		m.sessions[id] = session
		if gen := m.generations[session.GenerationID]; gen != nil {
			gen.leases[id] = session.ExpiresAt
			if authority, exists := m.inputs[gen.input]; exists {
				authority.expiresAt = session.ExpiresAt
				m.inputs[gen.input] = authority
			}
		}
	}
	m.mu.Unlock()
	return session, true
}

func (m *Manager) Heartbeat(id, profileID string, positionMS int64) (Session, error) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || session.ProfileID != profileID || !time.Now().Before(session.ExpiresAt) {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	if positionMS >= 0 {
		session.PositionMS = positionMS
	}
	ttl := directSessionTTL
	if session.GenerationID != "" {
		ttl = m.settings.LeaseTTL
	}
	session.ExpiresAt = time.Now().Add(ttl)
	m.sessions[id] = session
	if gen := m.generations[session.GenerationID]; gen != nil {
		gen.leases[id] = session.ExpiresAt
		if authority, exists := m.inputs[gen.input]; exists {
			authority.expiresAt = session.ExpiresAt
			m.inputs[gen.input] = authority
		}
	}
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) Seek(id, profileID string, positionMS int64) (Session, error) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || session.ProfileID != profileID || session.GenerationID == "" {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	gen := m.generations[session.GenerationID]
	start, end, retained := m.retainedRangeLocked(gen)
	if retained && positionMS >= start && positionMS <= end {
		session.PositionMS = max(0, positionMS)
		session.ExpiresAt = time.Now().Add(m.settings.LeaseTTL)
		m.sessions[id] = session
		gen.leases[id] = session.ExpiresAt
		m.mu.Unlock()
		return session, nil
	}
	plan, catalogID := session.Plan, session.CatalogID
	m.mu.Unlock()
	replacement, err := m.Create(profileID, catalogID, plan, positionMS)
	if err != nil {
		return Session{}, err
	}
	if !m.Stop(id, profileID) {
		_ = m.Stop(replacement.ID, profileID)
		return Session{}, ErrSessionInvalid
	}
	return replacement, nil
}

func (m *Manager) Stop(id, profileID string) bool {
	var retire *generation
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || session.ProfileID != profileID {
		m.mu.Unlock()
		return false
	}
	delete(m.sessions, id)
	if gen := m.generations[session.GenerationID]; gen != nil {
		delete(gen.leases, id)
		if len(gen.leases) == 0 {
			retire = m.detachGenerationLocked(gen.id)
		}
	}
	m.mu.Unlock()
	if retire != nil {
		m.retireGeneration(context.Background(), retire)
	}
	return true
}

func (m *Manager) detachGenerationLocked(id string) *generation {
	gen := m.generations[id]
	if gen == nil {
		return nil
	}
	delete(m.generations, id)
	delete(m.inputs, gen.input)
	for sessionID := range gen.leases {
		delete(m.sessions, sessionID)
	}
	return gen
}

func (m *Manager) retireGeneration(ctx context.Context, gen *generation) {
	if gen.process != nil {
		_ = gen.process.Signal(os.Interrupt)
		select {
		case <-gen.done:
		case <-time.After(m.settings.ProcessGrace):
			_ = gen.process.Kill()
			select {
			case <-gen.done:
			case <-time.After(m.settings.ProcessGrace):
			}
		case <-ctx.Done():
			_ = gen.process.Kill()
			select {
			case <-gen.done:
			case <-time.After(m.settings.ProcessGrace):
			}
		}
	}
	_ = m.files.RemoveAll(gen.dir)
}

func (m *Manager) InputCatalog(token string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	authority, ok := m.inputs[token]
	if !ok || !time.Now().Before(authority.expiresAt) {
		delete(m.inputs, token)
		return "", false
	}
	return authority.catalogID, true
}

func (m *Manager) OpenAsset(sessionID, profileID, name string) (afero.File, error) {
	if !assetName.MatchString(name) || filepath.Base(name) != name {
		return nil, os.ErrNotExist
	}
	session, ok := m.Lookup(sessionID, profileID, true)
	if !ok || session.GenerationID == "" {
		return nil, ErrSessionInvalid
	}
	m.mu.Lock()
	gen := m.generations[session.GenerationID]
	m.mu.Unlock()
	if gen == nil {
		return nil, ErrSessionInvalid
	}
	return m.files.Open(filepath.Join(gen.dir, name))
}

func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *Manager) UpdateSettings(settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	if len(m.generations) > 0 || m.starting > 0 {
		m.mu.Unlock()
		return ErrCapacity
	}
	restart := filepath.Clean(settings.SegmentDir) != filepath.Clean(m.settings.SegmentDir)
	m.mu.Unlock()
	if restart {
		if err := validateSegmentDirOwnership(m.files.Fs, settings.SegmentDir); err != nil {
			return err
		}
	}
	if err := saveSettings(m.db, m.files.Fs, settings); err != nil {
		return err
	}
	if restart {
		return ErrRestartRequired
	}
	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()
	return nil
}

func (m *Manager) Status() Status {
	type snapshot struct {
		status GenerationStatus
		dir    string
	}
	m.mu.Lock()
	settings := m.settings
	generations := make([]snapshot, 0, len(m.generations))
	for _, gen := range m.generations {
		catalogID := gen.jobKey
		if end := strings.IndexByte(catalogID, 0); end >= 0 {
			catalogID = catalogID[:end]
		}
		generations = append(generations, snapshot{
			status: GenerationStatus{ID: gen.id, CatalogID: catalogID, Kind: gen.kind, StartMS: gen.startMS, Leases: len(gen.leases), Running: !gen.completed, StartedAt: gen.startedAt.Unix()},
			dir:    gen.dir,
		})
	}
	m.mu.Unlock()
	out := Status{Settings: settings, Generations: make([]GenerationStatus, 0, len(generations))}
	for _, gen := range generations {
		gen.status.Bytes, _ = directoryBytes(m.files.Fs, gen.dir)
		out.Generations = append(out.Generations, gen.status)
	}
	sort.Slice(out.Generations, func(i, j int) bool { return out.Generations[i].StartedAt < out.Generations[j].StartedAt })
	return out
}

func directoryBytes(fs afero.Fs, dir string) (int64, error) {
	var total int64
	err := afero.Walk(fs, dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (m *Manager) janitor(ctx context.Context) {
	defer close(m.janitorDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			m.Sweep(now)
		case <-ctx.Done():
			return
		}
	}
}

// Sweep expires abandoned leases and enforces per-generation byte bounds.
func (m *Manager) Sweep(now time.Time) {
	type measurement struct {
		id         string
		gen        *generation
		sequence   uint64
		durations  []int64
		manifestOK bool
		bytes      int64
		err        error
	}
	var retire []*generation
	var expired []Session
	var measured []measurement
	m.mu.Lock()
	for id, session := range m.sessions {
		if !now.Before(session.ExpiresAt) {
			expired = append(expired, session)
			delete(m.sessions, id)
			if gen := m.generations[session.GenerationID]; gen != nil {
				delete(gen.leases, id)
			}
		}
	}
	for id, gen := range m.generations {
		measured = append(measured, measurement{id: id, gen: gen})
	}
	for token, authority := range m.inputs {
		if !now.Before(authority.expiresAt) {
			delete(m.inputs, token)
		}
	}
	generationBytes := m.settings.GenerationBytes
	m.mu.Unlock()

	for index := range measured {
		item := &measured[index]
		item.sequence, item.durations, item.manifestOK = readRetainedManifest(m.files.Fs, filepath.Join(item.gen.dir, "index.m3u8"))
		item.bytes, item.err = directoryBytes(m.files.Fs, item.gen.dir)
	}

	m.mu.Lock()
	for _, item := range measured {
		gen := m.generations[item.id]
		if gen != item.gen {
			continue
		}
		if item.manifestOK {
			_, _, _ = applyRetainedRange(gen, item.sequence, item.durations)
		}
		if len(gen.leases) == 0 || item.err != nil || item.bytes > generationBytes {
			for sessionID := range gen.leases {
				if session, ok := m.sessions[sessionID]; ok {
					expired = append(expired, session)
				}
			}
			if detached := m.detachGenerationLocked(item.id); detached != nil {
				retire = append(retire, detached)
			}
		}
	}
	m.mu.Unlock()
	if m.saveProgress != nil {
		for _, session := range expired {
			_ = m.saveProgress(session.ProfileID, session.CatalogID, session.PositionMS)
		}
	}
	for _, gen := range retire {
		m.retireGeneration(context.Background(), gen)
	}
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	sessions := make([]Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	generations := make([]*generation, 0, len(m.generations))
	for id := range m.generations {
		if gen := m.detachGenerationLocked(id); gen != nil {
			generations = append(generations, gen)
		}
	}
	m.sessions = map[string]Session{}
	m.mu.Unlock()
	if m.saveProgress != nil {
		for _, session := range sessions {
			_ = m.saveProgress(session.ProfileID, session.CatalogID, session.PositionMS)
		}
	}
	startsDone := make(chan struct{})
	go func() {
		m.startWG.Wait()
		close(startsDone)
	}()
	select {
	case <-startsDone:
	case <-ctx.Done():
		for _, gen := range generations {
			m.retireGeneration(ctx, gen)
		}
		return ctx.Err()
	}
	if m.janitorDone != nil {
		select {
		case <-m.janitorDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, gen := range generations {
		m.retireGeneration(ctx, gen)
	}
	return ctx.Err()
}

var _ io.Writer = (*boundedLog)(nil)
