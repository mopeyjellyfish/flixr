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
	assetName         = regexp.MustCompile(`^(?:master\.m3u8|index\.m3u8|init\.mp4|segment-[0-9]+\.m4s)$`)
	generationDirName = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
)

type ManagerConfig struct {
	Settings  Settings
	FS        afero.Fs
	DB        *sqlite.DB
	InputBase string
	Executor  Executor
	// Deprecated: progress is persisted by authenticated observation handlers.
	// Teardown never writes cached positions. This callback is not invoked.
	SaveProgress func(profileID, catalogID string, positionMS int64) error
	ManifestWait time.Duration
}

type inputAuthority struct {
	catalogID        string
	sourceKey        string
	audioStreamIndex int
	external         bool
	// pendingViewerID is cleared when the generation is admitted and shareable.
	pendingViewerID string
	expiresAt       time.Time
}

type generation struct {
	id              string
	jobKey          string
	kind            Kind
	startMS         int64
	dir             string
	inputs          []string
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

const (
	maxPendingHandoffs   = 128
	maxHandoffsPerViewer = 4
	maxResolvedHandoffs  = 128
)

// Handoff is a prepared replacement whose predecessor remains usable until
// the handoff is resolved or its fixed deadline expires.
type Handoff struct {
	ID        string
	Session   Session
	ExpiresAt time.Time
}

type pendingHandoff struct {
	id                    string
	oldID                 string
	newID                 string
	viewerID              string
	profileID             string
	expiresAt             time.Time
	replacementGeneration string
}

type resolvedHandoff struct {
	viewerID  string
	profileID string
	attached  bool
	expiresAt time.Time
	stoppedID string
	keptID    string
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
	mu                sync.Mutex
	settings          Settings
	db                *sqlite.DB
	files             afero.Afero
	inputBase         string
	executor          Executor
	manifestWait      time.Duration
	sessions          map[string]Session
	generations       map[string]*generation
	inputs            map[string]inputAuthority
	revokedViewers    map[string]struct{}
	pendingViewers    map[string]int
	starting          int
	pendingJobs       map[string]struct{}
	replacements      map[string]string
	preparingHandoffs map[string]time.Time
	handoffs          map[string]pendingHandoff
	sessionHandoffs   map[string]string
	resolvedHandoffs  map[string]resolvedHandoff
	subtitleJobs      map[string]map[string]context.CancelFunc
	startWG           sync.WaitGroup
	cancel            context.CancelFunc
	janitorDone       chan struct{}
	closed            bool
}

// NewDirectManager creates a no-goroutine manager for direct-only tests and callers.
func NewDirectManager() *Manager {
	return &Manager{
		settings:          DefaultSettings(os.TempDir()),
		files:             afero.Afero{Fs: afero.NewOsFs()},
		sessions:          map[string]Session{},
		generations:       map[string]*generation{},
		inputs:            map[string]inputAuthority{},
		revokedViewers:    map[string]struct{}{},
		pendingViewers:    map[string]int{},
		pendingJobs:       map[string]struct{}{},
		replacements:      map[string]string{},
		preparingHandoffs: map[string]time.Time{},
		handoffs:          map[string]pendingHandoff{},
		sessionHandoffs:   map[string]string{},
		resolvedHandoffs:  map[string]resolvedHandoff{},
		subtitleJobs:      map[string]map[string]context.CancelFunc{},
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
		settings:          config.Settings,
		db:                config.DB,
		files:             files,
		inputBase:         base,
		executor:          config.Executor,
		manifestWait:      config.ManifestWait,
		sessions:          map[string]Session{},
		generations:       map[string]*generation{},
		inputs:            map[string]inputAuthority{},
		revokedViewers:    map[string]struct{}{},
		pendingViewers:    map[string]int{},
		pendingJobs:       map[string]struct{}{},
		replacements:      map[string]string{},
		preparingHandoffs: map[string]time.Time{},
		handoffs:          map[string]pendingHandoff{},
		sessionHandoffs:   map[string]string{},
		resolvedHandoffs:  map[string]resolvedHandoff{},
		subtitleJobs:      map[string]map[string]context.CancelFunc{},
		cancel:            cancel,
		janitorDone:       make(chan struct{}),
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

func (m *Manager) Create(profileID, catalogID string, plan Plan, positionMS int64, progressGeneration ...int64) (Session, error) {
	return m.create(context.Background(), "", profileID, catalogID, plan, positionMS, "", false, progressGeneration...)
}

// CreateForViewer binds playback authority to one authenticated viewer session.
func (m *Manager) CreateForViewer(viewerID, profileID, catalogID string, plan Plan, positionMS int64, progressGeneration ...int64) (Session, error) {
	return m.create(context.Background(), viewerID, profileID, catalogID, plan, positionMS, "", false, progressGeneration...)
}

func (m *Manager) create(ctx context.Context, viewerID, profileID, catalogID string, plan Plan, positionMS int64, replacingGeneration string, retainReplacementCredit bool, progressGeneration ...int64) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if profileID == "" || catalogID == "" {
		return Session{}, ErrSessionInvalid
	}
	if viewerID != "" {
		m.mu.Lock()
		_, viewerRevoked := m.revokedViewers[viewerID]
		if viewerRevoked {
			m.mu.Unlock()
			return Session{}, ErrSessionInvalid
		}
		m.pendingViewers[viewerID]++
		m.mu.Unlock()
		defer m.finishViewerCreate(viewerID)
	}
	if positionMS < 0 {
		positionMS = 0
	}
	progress := int64(0)
	if len(progressGeneration) > 0 {
		progress = progressGeneration[0]
	}
	now := time.Now()
	sessionID, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	if plan.Kind == Direct {
		session := Session{ProgressGeneration: progress, ViewerID: viewerID, ID: sessionID, ProfileID: profileID, CatalogID: catalogID, Plan: plan, PositionMS: positionMS, ExpiresAt: now.Add(directSessionTTL)}
		m.mu.Lock()
		defer m.mu.Unlock()
		_, viewerRevoked := m.revokedViewers[viewerID]
		if m.closed || (viewerID != "" && viewerRevoked) {
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
	_, viewerRevoked := m.revokedViewers[viewerID]
	if m.closed || (viewerID != "" && viewerRevoked) {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	audioSelectionIndex := -1
	if plan.AudioSelected {
		audioSelectionIndex = plan.AudioStreamIndex
	}
	jobKey := generationJobKey(catalogID, plan, audioSelectionIndex)
	if existing := m.shareableGenerationLocked(jobKey, positionMS); existing != nil {
		// Media timestamps stay relative to the generation start when the HLS
		// playlist slides. The retained start is only an admission boundary.
		session := Session{ProgressGeneration: progress, ViewerID: viewerID, ID: sessionID, ProfileID: profileID, CatalogID: catalogID, Plan: plan, PositionMS: positionMS, StreamOffsetMS: existing.startMS, GenerationID: existing.id, ExpiresAt: now.Add(m.settings.LeaseTTL)}
		existing.leases[session.ID] = session.ExpiresAt
		m.sessions[session.ID] = session
		m.renewInputsLocked(existing, session.ExpiresAt)
		m.mu.Unlock()
		return session, nil
	}
	if _, pending := m.pendingJobs[jobKey]; pending {
		m.mu.Unlock()
		return Session{}, ErrPreparing
	}
	replacementCredit := 0
	if gen := m.generations[replacingGeneration]; gen != nil && len(gen.leases) == 1 {
		if _, replacing := m.replacements[replacingGeneration]; !replacing {
			replacementCredit = 1
			m.replacements[replacingGeneration] = sessionID
		}
	}
	reserved := len(m.generations) + m.starting + 1 - replacementCredit
	if reserved > m.settings.MaxGenerations || int64(reserved)*m.settings.GenerationBytes > m.settings.GlobalBytes {
		if replacementCredit == 1 && m.replacements[replacingGeneration] == sessionID {
			delete(m.replacements, replacingGeneration)
		}
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
		if replacementCredit == 1 && m.replacements[replacingGeneration] == sessionID {
			delete(m.replacements, replacingGeneration)
		}
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
	inputTokens := []string{inputToken}
	audioInputURL := ""
	if plan.AudioExternal {
		audioToken, tokenErr := randomToken()
		if tokenErr != nil {
			releaseReservation(true)
			_ = m.files.RemoveAll(dir)
			return Session{}, tokenErr
		}
		inputTokens = append(inputTokens, audioToken)
		audioInputURL = m.inputBase + "/api/v1/playback/input/" + audioToken
	}
	audioSourceStreamIndex := -1
	if plan.AudioSelected {
		audioSourceStreamIndex = plan.AudioSourceStreamIndex
	}
	name, args, err := ffmpegCommand(plan, inputURL, audioInputURL, audioSourceStreamIndex, dir, time.Duration(positionMS)*time.Millisecond, settings.SegmentWindow)
	if err != nil {
		releaseReservation(true)
		_ = m.files.RemoveAll(dir)
		return Session{}, err
	}
	revokeInputs := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, token := range inputTokens {
			delete(m.inputs, token)
		}
	}
	m.mu.Lock()
	_, viewerRevoked = m.revokedViewers[viewerID]
	if m.closed || (viewerID != "" && viewerRevoked) {
		m.mu.Unlock()
		releaseReservation(true)
		_ = m.files.RemoveAll(dir)
		return Session{}, ErrSessionInvalid
	}
	m.inputs[inputToken] = inputAuthority{catalogID: catalogID, sourceKey: plan.SourceKey, audioStreamIndex: -1, pendingViewerID: viewerID, expiresAt: now.Add(settings.LeaseTTL)}
	if plan.AudioExternal {
		m.inputs[inputTokens[1]] = inputAuthority{catalogID: catalogID, sourceKey: plan.SourceKey, audioStreamIndex: plan.AudioStreamIndex, external: true, pendingViewerID: viewerID, expiresAt: now.Add(settings.LeaseTTL)}
	}
	m.mu.Unlock()
	log := &boundedLog{}
	process, err := m.executor.StartContext(ctx, name, args, log)
	if err != nil {
		revokeInputs()
		releaseReservation(true)
		_ = m.files.RemoveAll(dir)
		return Session{}, fmt.Errorf("start FFmpeg: %w", err)
	}
	session := Session{ProgressGeneration: progress, ViewerID: viewerID, ID: sessionID, ProfileID: profileID, CatalogID: catalogID, Plan: plan, PositionMS: positionMS, StreamOffsetMS: positionMS, GenerationID: generationID, ExpiresAt: now.Add(settings.LeaseTTL)}
	gen := &generation{
		id: generationID, jobKey: jobKey, kind: plan.Kind, startMS: positionMS, dir: dir,
		inputs: inputTokens, leases: map[string]time.Time{session.ID: session.ExpiresAt},
		process: process, done: make(chan struct{}), startedAt: now, log: log,
		ready:           m.manifestWait == 0,
		segmentDuration: map[uint64]int64{},
	}
	go m.waitGeneration(gen, process)
	m.mu.Lock()
	m.starting--
	if replacementCredit == 1 && !retainReplacementCredit && m.replacements[replacingGeneration] == sessionID {
		delete(m.replacements, replacingGeneration)
	}
	_, viewerRevoked = m.revokedViewers[viewerID]
	if m.closed || (viewerID != "" && viewerRevoked) {
		delete(m.pendingJobs, jobKey)
		if m.replacements[replacingGeneration] == sessionID {
			delete(m.replacements, replacingGeneration)
		}
		m.mu.Unlock()
		revokeInputs()
		m.retireGeneration(context.Background(), gen)
		return Session{}, ErrSessionInvalid
	}
	m.generations[generationID] = gen
	m.sessions[session.ID] = session
	for _, token := range inputTokens {
		if authority, ok := m.inputs[token]; ok {
			authority.pendingViewerID = ""
			m.inputs[token] = authority
		}
	}
	m.mu.Unlock()
	if m.manifestWait > 0 {
		if err := waitForFile(m.files.Fs, filepath.Join(dir, "master.m3u8"), gen.done, m.manifestWait); err != nil {
			_ = m.StopForViewer(session.ID, viewerID, profileID)
			m.mu.Lock()
			if m.replacements[replacingGeneration] == sessionID {
				delete(m.replacements, replacingGeneration)
			}
			_, viewerRevoked = m.revokedViewers[viewerID]
			delete(m.pendingJobs, jobKey)
			m.mu.Unlock()
			if viewerID != "" && viewerRevoked {
				return Session{}, ErrSessionInvalid
			}
			return Session{}, fmt.Errorf("prepare HLS manifest: %w; ffmpeg: %s", err, gen.log.String())
		}
	}
	m.mu.Lock()
	currentGeneration := m.generations[gen.id]
	currentSession, sessionAdmitted := m.sessions[session.ID]
	_, viewerRevoked = m.revokedViewers[viewerID]
	admitted := !m.closed && (viewerID == "" || !viewerRevoked) && currentGeneration == gen && sessionAdmitted && currentSession.GenerationID == gen.id && currentSession.ViewerID == viewerID && currentSession.ProfileID == profileID
	if admitted {
		gen.ready = true
	}
	delete(m.pendingJobs, jobKey)
	m.mu.Unlock()
	if !admitted {
		_ = m.StopForViewer(session.ID, viewerID, profileID)
		m.mu.Lock()
		if m.replacements[replacingGeneration] == sessionID {
			delete(m.replacements, replacingGeneration)
		}
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	return session, nil
}

func generationJobKey(catalogID string, plan Plan, audioSelectionIndex int) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s\x00%d\x00%d\x00%t\x00%t\x00%d\x00%s\x00%d\x00%d\x00%d",
		catalogID, plan.Kind, plan.Container, plan.VideoCodec, plan.VideoProfile, plan.VideoLevel,
		plan.Width, plan.Height, plan.VideoBitrate, plan.FrameRateMilli, plan.BitDepth, plan.HDR,
		plan.AudioCodec, plan.AudioProfile, plan.AudioChannels, plan.AudioSampleRate, plan.AudioBitrate,
		plan.SourceKey, audioSelectionIndex, plan.AudioSourceStreamIndex, plan.AudioExternal, plan.AudioSelected, plan.Bandwidth, plan.QualityMode,
		plan.QualityMaxVideoBitrate, plan.QualityMaxWidth, plan.QualityMaxHeight)
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
	return m.LookupForViewer(id, "", profileID, touch)
}

// LookupForViewer requires both the viewer session and profile authority.
func (m *Manager) LookupForViewer(id, viewerID, profileID string, touch bool) (Session, bool) {
	now := time.Now()
	m.expireHandoffForSession(id, now)
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || (viewerID != "" && session.ViewerID != viewerID) || session.ProfileID != profileID || !now.Before(session.ExpiresAt) {
		m.mu.Unlock()
		if ok && session.ProfileID == profileID {
			_ = m.StopForViewer(id, viewerID, profileID)
		}
		return Session{}, false
	}
	if touch {
		ttl := directSessionTTL
		if session.GenerationID != "" {
			ttl = m.settings.LeaseTTL
		}
		session.ExpiresAt = now.Add(ttl)
		if handoff, exists := m.handoffs[m.sessionHandoffs[id]]; exists && session.ExpiresAt.After(handoff.expiresAt) {
			session.ExpiresAt = handoff.expiresAt
		}
		if deadline, preparing := m.preparingHandoffs[id]; preparing {
			session.ExpiresAt = deadline
		}
		m.sessions[id] = session
		if gen := m.generations[session.GenerationID]; gen != nil {
			gen.leases[id] = session.ExpiresAt
			m.renewInputsLocked(gen, session.ExpiresAt)
		}
	}
	m.mu.Unlock()
	return session, true
}

// SelectSubtitle updates native text-track state without replacing video or FFmpeg work.
func (m *Manager) SelectSubtitle(id, viewerID, profileID string, index int, external, selected bool) (Session, error) {
	return m.SelectSubtitleWithCommit(id, viewerID, profileID, index, external, selected, nil)
}

// SelectSubtitleWithCommit serializes durable preference changes with the
// playback session mutation that accepts them.
func (m *Manager) SelectSubtitleWithCommit(id, viewerID, profileID string, index int, external, selected bool, commit func() error) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok || session.ViewerID != viewerID || session.ProfileID != profileID || !time.Now().Before(session.ExpiresAt) {
		return Session{}, ErrSessionInvalid
	}
	if m.handoffControlBlockedLocked(id) {
		return Session{}, ErrPreparing
	}
	if selected {
		found := false
		for _, source := range session.Plan.SubtitleSources {
			if source.Index == index && source.External == external {
				found = true
				break
			}
		}
		if !found {
			return Session{}, ErrSessionInvalid
		}
	}
	if commit != nil {
		if err := commit(); err != nil {
			return Session{}, err
		}
	}
	session.Plan.SubtitleSelectionIndex = index
	session.Plan.SubtitleExternal = external
	session.Plan.SubtitleSelected = selected
	m.sessions[id] = session
	return session, nil
}

// SubtitleContext binds bounded extraction work to the playback session lifetime.
func (m *Manager) SubtitleContext(parent context.Context, id, viewerID, profileID string) (context.Context, func(), error) {
	jobID, err := randomToken()
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || session.ViewerID != viewerID || session.ProfileID != profileID || !time.Now().Before(session.ExpiresAt) {
		m.mu.Unlock()
		return nil, nil, ErrSessionInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	if m.subtitleJobs[id] == nil {
		m.subtitleJobs[id] = map[string]context.CancelFunc{}
	}
	m.subtitleJobs[id][jobID] = cancel
	m.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			m.mu.Lock()
			delete(m.subtitleJobs[id], jobID)
			if len(m.subtitleJobs[id]) == 0 {
				delete(m.subtitleJobs, id)
			}
			m.mu.Unlock()
		})
	}
	return ctx, release, nil
}

func (m *Manager) cancelSubtitleJobsLocked(sessionID string) {
	for _, cancel := range m.subtitleJobs[sessionID] {
		cancel()
	}
	delete(m.subtitleJobs, sessionID)
}

func (m *Manager) Heartbeat(id, profileID string, positionMS int64) (Session, error) {
	return m.HeartbeatForViewer(id, "", profileID, positionMS)
}

func (m *Manager) HeartbeatForViewer(id, viewerID, profileID string, positionMS int64) (Session, error) {
	m.expireHandoffForSession(id, time.Now())
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || (viewerID != "" && session.ViewerID != viewerID) || session.ProfileID != profileID || !time.Now().Before(session.ExpiresAt) {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	if handoff, exists := m.handoffs[m.sessionHandoffs[id]]; exists && handoff.newID == id {
		m.mu.Unlock()
		return Session{}, ErrPreparing
	}
	if positionMS >= 0 {
		session.PositionMS = positionMS
	}
	ttl := directSessionTTL
	if session.GenerationID != "" {
		ttl = m.settings.LeaseTTL
	}
	session.ExpiresAt = time.Now().Add(ttl)
	if handoff, exists := m.handoffs[m.sessionHandoffs[id]]; exists && session.ExpiresAt.After(handoff.expiresAt) {
		session.ExpiresAt = handoff.expiresAt
	}
	if deadline, preparing := m.preparingHandoffs[id]; preparing {
		session.ExpiresAt = deadline
	}
	m.sessions[id] = session
	if gen := m.generations[session.GenerationID]; gen != nil {
		gen.leases[id] = session.ExpiresAt
		m.renewInputsLocked(gen, session.ExpiresAt)
	}
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) Seek(id, profileID string, positionMS int64) (Session, error) {
	return m.SeekForViewerContext(context.Background(), id, "", profileID, positionMS)
}

func (m *Manager) SeekForViewer(id, viewerID, profileID string, positionMS int64) (Session, error) {
	return m.SeekForViewerContext(context.Background(), id, viewerID, profileID, positionMS)
}

// SeekForViewerContext reuses retained media or prepares a replacement before
// releasing the authenticated viewer's current session.
func (m *Manager) SeekForViewerContext(ctx context.Context, id, viewerID, profileID string, positionMS int64) (Session, error) {
	return m.SeekForViewerContextWithCommit(ctx, id, viewerID, profileID, positionMS, nil)
}

// SeekForViewerContextWithCommit prepares a replacement, runs commit while the
// old session is still valid, and only then releases the old generation.
func (m *Manager) SeekForViewerContextWithCommit(ctx context.Context, id, viewerID, profileID string, positionMS int64, commit func() error) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || (viewerID != "" && session.ViewerID != viewerID) || session.ProfileID != profileID || session.GenerationID == "" {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	if m.handoffControlBlockedLocked(id) {
		m.mu.Unlock()
		return Session{}, ErrPreparing
	}
	gen := m.generations[session.GenerationID]
	start, end, retained := m.retainedRangeLocked(gen)
	if retained && positionMS >= start && positionMS <= end {
		if commit != nil {
			if err := commit(); err != nil {
				m.mu.Unlock()
				return Session{}, err
			}
		}
		session.PositionMS = max(0, positionMS)
		session.ExpiresAt = time.Now().Add(m.settings.LeaseTTL)
		m.sessions[id] = session
		gen.leases[id] = session.ExpiresAt
		m.mu.Unlock()
		return session, nil
	}
	plan, catalogID := session.Plan, session.CatalogID
	m.mu.Unlock()
	replacement, err := m.create(ctx, viewerID, profileID, catalogID, plan, positionMS, session.GenerationID, false, session.ProgressGeneration)
	if err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	current, oldValid := m.sessions[id]
	candidate, candidateValid := m.sessions[replacement.ID]
	_, viewerRevoked := m.revokedViewers[viewerID]
	oldValid = oldValid && current.ViewerID == viewerID && current.ProfileID == profileID && current.GenerationID == session.GenerationID && current.ProgressGeneration == session.ProgressGeneration && time.Now().Before(current.ExpiresAt)
	candidateValid = candidateValid && candidate.ViewerID == viewerID && candidate.ProfileID == profileID && candidate.GenerationID == replacement.GenerationID
	if ctx.Err() != nil || !oldValid || !candidateValid || (viewerID != "" && viewerRevoked) {
		contextErr := ctx.Err()
		m.mu.Unlock()
		_ = m.StopForViewer(replacement.ID, viewerID, profileID)
		if contextErr != nil {
			return Session{}, contextErr
		}
		return Session{}, ErrSessionInvalid
	}
	if commit != nil {
		if err := commit(); err != nil {
			m.mu.Unlock()
			_ = m.StopForViewer(replacement.ID, viewerID, profileID)
			return Session{}, err
		}
	}
	delete(m.sessions, id)
	m.cancelSubtitleJobsLocked(id)
	var retire *generation
	if oldGeneration := m.generations[current.GenerationID]; oldGeneration != nil {
		delete(oldGeneration.leases, id)
		if len(oldGeneration.leases) == 0 {
			retire = m.detachGenerationLocked(oldGeneration.id)
		}
	}
	m.mu.Unlock()
	if retire != nil {
		m.retireGeneration(context.Background(), retire)
	}
	return replacement, nil
}

// Replace creates a new rendition before releasing the existing session.
func (m *Manager) Replace(id, profileID string, plan Plan, positionMS int64) (Session, error) {
	return m.ReplaceContext(context.Background(), id, profileID, plan, positionMS)
}

// PrepareHandoffContext creates a replacement while retaining the current
// session behind a fixed, non-renewable handoff deadline.
func (m *Manager) PrepareHandoffContext(ctx context.Context, id, profileID string, plan Plan, positionMS int64) (Handoff, error) {
	return m.PrepareHandoffContextWithCommit(ctx, id, profileID, plan, positionMS, nil)
}

// PrepareHandoffContextWithCommit reserves the source and commits related
// durable state at one ordering point before slow candidate preparation.
func (m *Manager) PrepareHandoffContextWithCommit(ctx context.Context, id, profileID string, plan Plan, positionMS int64, commit func() error) (Handoff, error) {
	if err := ctx.Err(); err != nil {
		return Handoff{}, err
	}
	handoffID, err := randomToken()
	if err != nil {
		return Handoff{}, err
	}
	m.mu.Lock()
	old, ok := m.sessions[id]
	if !ok || old.ProfileID != profileID || !time.Now().Before(old.ExpiresAt) {
		m.mu.Unlock()
		return Handoff{}, ErrSessionInvalid
	}
	if _, pending := m.preparingHandoffs[id]; pending || m.sessionHandoffs[id] != "" {
		m.mu.Unlock()
		return Handoff{}, ErrPreparing
	}
	viewerPending := 0
	for _, item := range m.handoffs {
		if item.viewerID == old.ViewerID {
			viewerPending++
		}
	}
	for sessionID := range m.preparingHandoffs {
		if preparing, exists := m.sessions[sessionID]; exists && preparing.ViewerID == old.ViewerID {
			viewerPending++
		}
	}
	if len(m.handoffs)+len(m.preparingHandoffs) >= maxPendingHandoffs || viewerPending >= maxHandoffsPerViewer {
		m.mu.Unlock()
		return Handoff{}, ErrCapacity
	}
	if commit != nil {
		if err := commit(); err != nil {
			m.mu.Unlock()
			return Handoff{}, err
		}
	}
	leaseTTL := m.settings.LeaseTTL
	preparationDeadline := time.Now().Add(leaseTTL + max(0, m.manifestWait))
	if old.ExpiresAt.Before(preparationDeadline) {
		old.ExpiresAt = preparationDeadline
		m.sessions[id] = old
		if gen := m.generations[old.GenerationID]; gen != nil {
			gen.leases[id] = old.ExpiresAt
			m.renewInputsLocked(gen, old.ExpiresAt)
		}
	}
	m.preparingHandoffs[id] = preparationDeadline
	m.mu.Unlock()

	activated := false
	replacementID := ""
	defer func() {
		m.mu.Lock()
		delete(m.preparingHandoffs, id)
		if !activated {
			if old.GenerationID != "" && m.replacements[old.GenerationID] == replacementID {
				delete(m.replacements, old.GenerationID)
			}
			if current, ok := m.sessions[id]; ok {
				ttl := directSessionTTL
				if current.GenerationID != "" {
					ttl = m.settings.LeaseTTL
				}
				current.ExpiresAt = time.Now().Add(ttl)
				m.sessions[id] = current
				if gen := m.generations[current.GenerationID]; gen != nil {
					gen.leases[id] = current.ExpiresAt
					m.renewInputsLocked(gen, current.ExpiresAt)
				}
			}
		}
		m.mu.Unlock()
	}()
	prepareCtx, cancelPrepare := context.WithDeadline(ctx, preparationDeadline)
	defer cancelPrepare()
	replacement, err := m.create(prepareCtx, old.ViewerID, profileID, old.CatalogID, plan, positionMS, old.GenerationID, true, old.ProgressGeneration)
	if err != nil {
		return Handoff{}, err
	}
	replacementID = replacement.ID

	now := time.Now()
	deadline := now.Add(leaseTTL)
	m.mu.Lock()
	current, oldValid := m.sessions[id]
	candidate, candidateValid := m.sessions[replacement.ID]
	_, viewerRevoked := m.revokedViewers[old.ViewerID]
	oldValid = oldValid && current.ViewerID == old.ViewerID && current.ProfileID == profileID && current.GenerationID == old.GenerationID && current.ProgressGeneration == old.ProgressGeneration && now.Before(current.ExpiresAt)
	candidateValid = candidateValid && candidate.ViewerID == old.ViewerID && candidate.ProfileID == profileID && candidate.GenerationID == replacement.GenerationID
	if ctx.Err() != nil || !oldValid || !candidateValid || m.sessionHandoffs[id] != "" || (old.ViewerID != "" && viewerRevoked) {
		contextErr := ctx.Err()
		m.mu.Unlock()
		_ = m.StopForViewer(replacement.ID, old.ViewerID, profileID)
		if contextErr != nil {
			return Handoff{}, contextErr
		}
		return Handoff{}, ErrSessionInvalid
	}
	current.ExpiresAt = deadline
	candidate.ExpiresAt = deadline
	m.sessions[id] = current
	m.sessions[replacement.ID] = candidate
	if gen := m.generations[current.GenerationID]; gen != nil {
		gen.leases[id] = deadline
		m.renewInputsLocked(gen, deadline)
	}
	if gen := m.generations[candidate.GenerationID]; gen != nil {
		gen.leases[candidate.ID] = deadline
		m.renewInputsLocked(gen, deadline)
	}
	replacementGeneration := ""
	if m.replacements[old.GenerationID] == candidate.ID {
		replacementGeneration = old.GenerationID
	}
	record := pendingHandoff{id: handoffID, oldID: id, newID: candidate.ID, viewerID: old.ViewerID, profileID: profileID, expiresAt: deadline, replacementGeneration: replacementGeneration}
	m.handoffs[handoffID] = record
	m.sessionHandoffs[id] = handoffID
	m.sessionHandoffs[candidate.ID] = handoffID
	activated = true
	m.mu.Unlock()
	return Handoff{ID: handoffID, Session: candidate, ExpiresAt: deadline}, nil
}

// IsHandoffCandidate reports whether a session may fetch media for attachment
// but must not yet become playback/progress authority.
func (m *Manager) IsHandoffCandidate(id, viewerID, profileID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	handoffID := m.sessionHandoffs[id]
	record, ok := m.handoffs[handoffID]
	return ok && record.newID == id && record.viewerID == viewerID && record.profileID == profileID
}

// IsHandoffControlBlocked reports whether source-changing or track-changing
// controls must wait for a preparation or active handoff to resolve.
func (m *Manager) IsHandoffControlBlocked(id, viewerID, profileID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return ok && session.ViewerID == viewerID && session.ProfileID == profileID && m.handoffControlBlockedLocked(id)
}

func (m *Manager) handoffControlBlockedLocked(id string) bool {
	_, preparing := m.preparingHandoffs[id]
	return preparing || m.sessionHandoffs[id] != ""
}

// ResolveHandoff promotes an attached candidate or restores its predecessor.
// Repeating the same outcome is idempotent until the handoff deadline.
func (m *Manager) ResolveHandoff(id, viewerID, profileID string, attached bool) error {
	var retire []*generation
	requestedAttached := attached
	m.mu.Lock()
	now := time.Now()
	m.pruneResolvedHandoffsLocked(now)
	record, ok := m.handoffs[id]
	if !ok {
		resolved, exists := m.resolvedHandoffs[id]
		m.mu.Unlock()
		if !exists || resolved.viewerID != viewerID || resolved.profileID != profileID {
			return ErrSessionInvalid
		}
		if resolved.attached != attached {
			return ErrHandoffConflict
		}
		return nil
	}
	if record.viewerID != viewerID || record.profileID != profileID {
		m.mu.Unlock()
		return ErrSessionInvalid
	}
	if !now.Before(record.expiresAt) {
		attached = false
	}
	retire = m.resolveHandoffLocked(record, attached, now)
	m.mu.Unlock()
	for _, gen := range retire {
		m.retireGeneration(context.Background(), gen)
	}
	if !now.Before(record.expiresAt) && requestedAttached {
		return ErrHandoffConflict
	}
	return nil
}

func (m *Manager) resolveHandoffLocked(record pendingHandoff, attached bool, now time.Time) []*generation {
	delete(m.handoffs, record.id)
	delete(m.sessionHandoffs, record.oldID)
	delete(m.sessionHandoffs, record.newID)
	if record.replacementGeneration != "" {
		if m.replacements[record.replacementGeneration] == record.newID {
			delete(m.replacements, record.replacementGeneration)
		}
	}
	keepID, stopID := record.newID, record.oldID
	if !attached {
		keepID, stopID = record.oldID, record.newID
	}
	if keep, ok := m.sessions[keepID]; ok {
		ttl := directSessionTTL
		if keep.GenerationID != "" {
			ttl = m.settings.LeaseTTL
		}
		keep.ExpiresAt = now.Add(ttl)
		m.sessions[keepID] = keep
		if gen := m.generations[keep.GenerationID]; gen != nil {
			gen.leases[keepID] = keep.ExpiresAt
			m.renewInputsLocked(gen, keep.ExpiresAt)
		}
	}
	retire := m.stopSessionLocked(stopID)
	m.resolvedHandoffs[record.id] = resolvedHandoff{viewerID: record.viewerID, profileID: record.profileID, attached: attached, expiresAt: now.Add(m.settings.LeaseTTL), stoppedID: stopID, keptID: keepID}
	m.pruneResolvedHandoffsLocked(now)
	if retire == nil {
		return nil
	}
	return []*generation{retire}
}

func (m *Manager) expireHandoffForSession(id string, now time.Time) {
	var retire []*generation
	m.mu.Lock()
	handoffID := m.sessionHandoffs[id]
	record, ok := m.handoffs[handoffID]
	if ok && !now.Before(record.expiresAt) {
		retire = m.resolveHandoffLocked(record, false, now)
	}
	m.mu.Unlock()
	for _, gen := range retire {
		m.retireGeneration(context.Background(), gen)
	}
}

func (m *Manager) pruneResolvedHandoffsLocked(now time.Time) {
	for id, record := range m.resolvedHandoffs {
		if !now.Before(record.expiresAt) {
			delete(m.resolvedHandoffs, id)
		}
	}
	for len(m.resolvedHandoffs) > maxResolvedHandoffs {
		m.deleteEarliestResolvedHandoffLocked("")
	}
	counts := make(map[string]int)
	for _, record := range m.resolvedHandoffs {
		counts[record.viewerID]++
	}
	for viewerID, count := range counts {
		for count > maxHandoffsPerViewer {
			m.deleteEarliestResolvedHandoffLocked(viewerID)
			count--
		}
	}
}

func (m *Manager) deleteEarliestResolvedHandoffLocked(viewerID string) {
	earliestID := ""
	var earliest time.Time
	for id, record := range m.resolvedHandoffs {
		if viewerID != "" && record.viewerID != viewerID {
			continue
		}
		if earliestID == "" || record.expiresAt.Before(earliest) {
			earliestID, earliest = id, record.expiresAt
		}
	}
	delete(m.resolvedHandoffs, earliestID)
}

// ReplaceContext creates a new rendition and preserves the existing session if
// preparation fails or the caller leaves before the replacement is committed.
func (m *Manager) ReplaceContext(ctx context.Context, id, profileID string, plan Plan, positionMS int64) (Session, error) {
	return m.ReplaceContextWithCommit(ctx, id, profileID, plan, positionMS, nil)
}

// ReplaceContextWithCommit reserves the source and commits related durable
// state before preparing a replacement.
func (m *Manager) ReplaceContextWithCommit(ctx context.Context, id, profileID string, plan Plan, positionMS int64, commit func() error) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok || session.ProfileID != profileID || !time.Now().Before(session.ExpiresAt) {
		m.mu.Unlock()
		return Session{}, ErrSessionInvalid
	}
	if m.handoffControlBlockedLocked(id) {
		m.mu.Unlock()
		return Session{}, ErrPreparing
	}
	if commit != nil {
		if err := commit(); err != nil {
			m.mu.Unlock()
			return Session{}, err
		}
	}
	leaseTTL := m.settings.LeaseTTL
	preparationDeadline := time.Now().Add(leaseTTL + max(0, m.manifestWait))
	if session.ExpiresAt.Before(preparationDeadline) {
		session.ExpiresAt = preparationDeadline
		m.sessions[id] = session
		if gen := m.generations[session.GenerationID]; gen != nil {
			gen.leases[id] = session.ExpiresAt
			m.renewInputsLocked(gen, session.ExpiresAt)
		}
	}
	m.preparingHandoffs[id] = preparationDeadline
	m.mu.Unlock()
	accepted := false
	defer func() {
		m.mu.Lock()
		delete(m.preparingHandoffs, id)
		if !accepted {
			if current, ok := m.sessions[id]; ok {
				ttl := directSessionTTL
				if current.GenerationID != "" {
					ttl = m.settings.LeaseTTL
				}
				current.ExpiresAt = time.Now().Add(ttl)
				m.sessions[id] = current
				if gen := m.generations[current.GenerationID]; gen != nil {
					gen.leases[id] = current.ExpiresAt
					m.renewInputsLocked(gen, current.ExpiresAt)
				}
			}
		}
		m.mu.Unlock()
	}()
	prepareCtx, cancelPrepare := context.WithDeadline(ctx, preparationDeadline)
	defer cancelPrepare()
	replacement, err := m.create(prepareCtx, session.ViewerID, profileID, session.CatalogID, plan, positionMS, session.GenerationID, false, session.ProgressGeneration)
	if err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	current, oldValid := m.sessions[id]
	candidate, candidateValid := m.sessions[replacement.ID]
	oldValid = oldValid && current.ViewerID == session.ViewerID && current.ProfileID == profileID && current.GenerationID == session.GenerationID && current.ProgressGeneration == session.ProgressGeneration
	candidateValid = candidateValid && candidate.ViewerID == session.ViewerID && candidate.ProfileID == profileID && candidate.GenerationID == replacement.GenerationID
	if ctx.Err() != nil || !oldValid || !candidateValid {
		contextErr := ctx.Err()
		m.mu.Unlock()
		_ = m.StopForViewer(replacement.ID, session.ViewerID, profileID)
		if contextErr != nil {
			return Session{}, contextErr
		}
		return Session{}, ErrSessionInvalid
	}
	delete(m.preparingHandoffs, id)
	retire := m.stopSessionLocked(id)
	accepted = true
	m.mu.Unlock()
	if retire != nil {
		m.retireGeneration(context.Background(), retire)
	}
	return candidate, nil
}

func (m *Manager) Stop(id, profileID string) bool {
	return m.StopForViewer(id, "", profileID)
}

func (m *Manager) StopForViewer(id, viewerID, profileID string) bool {
	var retire []*generation
	m.mu.Lock()
	m.pruneResolvedHandoffsLocked(time.Now())
	session, ok := m.sessions[id]
	if !ok {
		for _, resolved := range m.resolvedHandoffs {
			if resolved.stoppedID == id && resolved.profileID == profileID && (viewerID == "" || resolved.viewerID == viewerID) {
				id = resolved.keptID
				session, ok = m.sessions[id]
				break
			}
		}
	}
	if !ok || (viewerID != "" && session.ViewerID != viewerID) || session.ProfileID != profileID {
		m.mu.Unlock()
		return false
	}
	ids := []string{id}
	if handoffID := m.sessionHandoffs[id]; handoffID != "" {
		if record, exists := m.handoffs[handoffID]; exists {
			ids = []string{record.oldID, record.newID}
			delete(m.handoffs, handoffID)
			delete(m.sessionHandoffs, record.oldID)
			delete(m.sessionHandoffs, record.newID)
			if record.replacementGeneration != "" {
				if m.replacements[record.replacementGeneration] == record.newID {
					delete(m.replacements, record.replacementGeneration)
				}
			}
		}
	}
	seen := make(map[string]struct{})
	for _, sessionID := range ids {
		if gen := m.stopSessionLocked(sessionID); gen != nil {
			if _, exists := seen[gen.id]; !exists {
				retire = append(retire, gen)
				seen[gen.id] = struct{}{}
			}
		}
	}
	m.mu.Unlock()
	for _, gen := range retire {
		m.retireGeneration(context.Background(), gen)
	}
	return true
}

// StopViewer releases every playback lease owned by one authenticated viewer.
func (m *Manager) StopViewer(viewerID string) {
	if viewerID == "" {
		return
	}
	type ownedSession struct{ id, profileID string }
	m.mu.Lock()
	if m.pendingViewers[viewerID] > 0 {
		m.revokedViewers[viewerID] = struct{}{}
	}
	for token, authority := range m.inputs {
		if authority.pendingViewerID == viewerID {
			delete(m.inputs, token)
		}
	}
	owned := make([]ownedSession, 0)
	for _, session := range m.sessions {
		if session.ViewerID == viewerID {
			owned = append(owned, ownedSession{id: session.ID, profileID: session.ProfileID})
		}
	}
	m.mu.Unlock()
	for _, session := range owned {
		m.StopForViewer(session.id, viewerID, session.profileID)
	}
}

func (m *Manager) finishViewerCreate(viewerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	remaining := m.pendingViewers[viewerID] - 1
	if remaining > 0 {
		m.pendingViewers[viewerID] = remaining
		return
	}
	delete(m.pendingViewers, viewerID)
	delete(m.revokedViewers, viewerID)
}

// StopSupersededPlans releases older plan responses for the same viewer and
// title. A later admitted generation is left alone when responses complete out
// of order, and playback owned by another bearer session is never selected.
func (m *Manager) StopSupersededPlans(current Session) {
	if current.ViewerID == "" {
		return
	}
	type supersededSession struct{ id, profileID string }
	m.mu.Lock()
	admitted, ok := m.sessions[current.ID]
	if !ok || admitted.ViewerID != current.ViewerID || admitted.ProfileID != current.ProfileID || admitted.CatalogID != current.CatalogID {
		m.mu.Unlock()
		return
	}
	superseded := make([]supersededSession, 0)
	for _, session := range m.sessions {
		if session.ID != current.ID && session.ViewerID == current.ViewerID && session.ProfileID == current.ProfileID && session.CatalogID == current.CatalogID && session.ProgressGeneration <= current.ProgressGeneration {
			superseded = append(superseded, supersededSession{id: session.ID, profileID: session.ProfileID})
		}
	}
	m.mu.Unlock()
	for _, session := range superseded {
		m.StopForViewer(session.id, current.ViewerID, session.profileID)
	}
}

func (m *Manager) detachGenerationLocked(id string) *generation {
	gen := m.generations[id]
	if gen == nil {
		return nil
	}
	delete(m.generations, id)
	delete(m.replacements, id)
	for _, token := range gen.inputs {
		delete(m.inputs, token)
	}
	for sessionID := range gen.leases {
		delete(m.sessions, sessionID)
		m.cancelSubtitleJobsLocked(sessionID)
	}
	return gen
}

func (m *Manager) stopSessionLocked(id string) *generation {
	session, ok := m.sessions[id]
	if !ok {
		return nil
	}
	delete(m.sessions, id)
	m.cancelSubtitleJobsLocked(id)
	if gen := m.generations[session.GenerationID]; gen != nil {
		delete(gen.leases, id)
		if len(gen.leases) == 0 {
			return m.detachGenerationLocked(gen.id)
		}
	}
	return nil
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
	id, _, _, _, ok := m.InputFile(token)
	return id, ok
}

func (m *Manager) InputSource(token string) (string, string, bool) {
	id, sourceKey, _, _, ok := m.InputFile(token)
	return id, sourceKey, ok
}

func (m *Manager) Input(token string) (catalogID string, audioStreamIndex int, external, ok bool) {
	id, _, index, external, ok := m.InputFile(token)
	return id, index, external, ok
}

func (m *Manager) InputFile(token string) (catalogID, sourceKey string, audioStreamIndex int, external, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	authority, ok := m.inputs[token]
	if !ok || !time.Now().Before(authority.expiresAt) {
		delete(m.inputs, token)
		return "", "", 0, false, false
	}
	return authority.catalogID, authority.sourceKey, authority.audioStreamIndex, authority.external, true
}

func (m *Manager) renewInputsLocked(gen *generation, expiresAt time.Time) {
	for _, token := range gen.inputs {
		authority, exists := m.inputs[token]
		if !exists {
			continue
		}
		authority.expiresAt = expiresAt
		m.inputs[token] = authority
	}
}

func (m *Manager) OpenAsset(sessionID, profileID, name string) (afero.File, error) {
	return m.OpenAssetForViewer(sessionID, "", profileID, name)
}

func (m *Manager) OpenAssetForViewer(sessionID, viewerID, profileID, name string) (afero.File, error) {
	if !assetName.MatchString(name) || filepath.Base(name) != name {
		return nil, os.ErrNotExist
	}
	session, ok := m.LookupForViewer(sessionID, viewerID, profileID, true)
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
	m.pruneResolvedHandoffsLocked(now)
	for _, handoff := range m.handoffs {
		if !now.Before(handoff.expiresAt) {
			retire = append(retire, m.resolveHandoffLocked(handoff, false, now)...)
		}
	}
	for id, session := range m.sessions {
		if !now.Before(session.ExpiresAt) {
			expired = append(expired, session)
			delete(m.sessions, id)
			m.cancelSubtitleJobsLocked(id)
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
	// Progress is durably recorded by ordered heartbeat/seek requests. Cleanup must
	// not manufacture a newer observation and overwrite an explicit watched action.
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
	for id := range m.sessions {
		m.cancelSubtitleJobsLocked(id)
	}
	generations := make([]*generation, 0, len(m.generations))
	for id := range m.generations {
		if gen := m.detachGenerationLocked(id); gen != nil {
			generations = append(generations, gen)
		}
	}
	m.sessions = map[string]Session{}
	m.inputs = map[string]inputAuthority{}
	m.mu.Unlock()
	// See Sweep: session teardown has no observation timestamp and cannot safely
	// supersede a durable explicit action.
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
