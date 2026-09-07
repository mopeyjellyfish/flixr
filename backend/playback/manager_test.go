package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

type fakeProcess struct {
	once         sync.Once
	done         chan struct{}
	signaled     atomic.Bool
	killed       atomic.Bool
	ignoreSignal bool
}

func newFakeProcess(ignoreSignal bool) *fakeProcess {
	return &fakeProcess{done: make(chan struct{}), ignoreSignal: ignoreSignal}
}
func (p *fakeProcess) Signal(os.Signal) error {
	p.signaled.Store(true)
	if !p.ignoreSignal {
		p.once.Do(func() { close(p.done) })
	}
	return nil
}
func (p *fakeProcess) Kill() error {
	p.killed.Store(true)
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *fakeProcess) Wait() error { <-p.done; return nil }

type fakeExecutor struct {
	mu           sync.Mutex
	processes    []*fakeProcess
	commands     [][]string
	ignoreSignal bool
}

func (e *fakeExecutor) Start(name string, args []string, _ io.Writer) (Process, error) {
	if name != "ffmpeg" {
		return nil, errors.New("unexpected executable")
	}
	process := newFakeProcess(e.ignoreSignal)
	e.mu.Lock()
	e.processes = append(e.processes, process)
	e.commands = append(e.commands, append([]string(nil), args...))
	e.mu.Unlock()
	manifest := args[len(args)-1]
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n")
	for index := range 15 {
		fmt.Fprintf(&playlist, "#EXTINF:4.0,\nsegment-%06d.m4s\n", index)
	}
	if err := os.WriteFile(manifest, []byte(playlist.String()), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifest), "master.m3u8"), []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=5000000,CODECS=\"avc1.640028,mp4a.40.2\"\nindex.m3u8\n"), 0o600); err != nil {
		return nil, err
	}
	return process, nil
}

func testManager(t *testing.T, mutate func(*Settings)) (*Manager, *fakeExecutor) {
	t.Helper()
	settings := DefaultSettings(t.TempDir())
	settings.LeaseTTL = 3 * time.Second
	settings.HeartbeatInterval = time.Second
	settings.SegmentWindow = 60 * time.Second
	settings.ProcessGrace = 10 * time.Millisecond
	settings.GenerationBytes = 1 << 20
	settings.GlobalBytes = 2 << 20
	settings.MaxGenerations = 2
	if mutate != nil {
		mutate(&settings)
	}
	executor := &fakeExecutor{}
	manager, err := NewManager(ManagerConfig{Settings: settings, InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return manager, executor
}

func TestManagerSharesGenerationUntilLastLeaseStops(t *testing.T) {
	manager, executor := testManager(t, nil)
	plan := Plan{Kind: Remux, VideoCodec: "h264", AudioCodec: "aac"}
	first, err := manager.Create("profile-a", "film-1", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("profile-b", "film-1", plan, 30_000)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationID != second.GenerationID {
		t.Fatalf("generations differ: %q and %q", first.GenerationID, second.GenerationID)
	}
	if len(executor.processes) != 1 {
		t.Fatalf("started %d processes, want 1", len(executor.processes))
	}
	if !manager.Stop(first.ID, "profile-a") {
		t.Fatal("first stop failed")
	}
	if executor.processes[0].signaled.Load() {
		t.Fatal("shared process stopped before its last lease")
	}
	if !manager.Stop(second.ID, "profile-b") {
		t.Fatal("second stop failed")
	}
	if !executor.processes[0].signaled.Load() {
		t.Fatal("process was not interrupted after its last lease")
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("generation remains after its final lease")
	}
}

func TestManagerForcesKillAfterGracePeriod(t *testing.T) {
	manager, executor := testManager(t, func(settings *Settings) {
		settings.ProcessGrace = 5 * time.Millisecond
	})
	executor.ignoreSignal = true
	session, err := manager.Create("profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", AudioCodec: "aac"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Stop(session.ID, "profile-a") {
		t.Fatal("stop failed")
	}
	if !executor.processes[0].signaled.Load() || !executor.processes[0].killed.Load() {
		t.Fatal("process did not receive interrupt followed by forced kill")
	}
}

func TestManagerEnforcesConcurrencyAndSeekWindow(t *testing.T) {
	manager, executor := testManager(t, func(settings *Settings) {
		settings.MaxGenerations = 1
		settings.GlobalBytes = settings.GenerationBytes
	})
	plan := Plan{Kind: Transcode, VideoCodec: "h264", AudioCodec: "aac"}
	session, err := manager.Create("profile-a", "film-1", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("profile-b", "film-2", plan, 0); !errors.Is(err, ErrCapacity) {
		t.Fatalf("second create error = %v, want capacity", err)
	}
	inside, err := manager.Seek(session.ID, "profile-a", 15_000)
	if err != nil {
		t.Fatal(err)
	}
	if inside.GenerationID != session.GenerationID || len(executor.processes) != 1 {
		t.Fatal("in-window seek did not reuse its generation")
	}
	if _, err := manager.Seek(inside.ID, "profile-a", 90_000); !errors.Is(err, ErrCapacity) {
		t.Fatalf("out-of-window seek error = %v, want capacity", err)
	}
	if _, ok := manager.Lookup(inside.ID, "profile-a", false); !ok {
		t.Fatal("failed replacement destroyed the existing session")
	}
}

func TestOutOfWindowSeekReplacesOneLeaseAndKeepsSharedGeneration(t *testing.T) {
	manager, executor := testManager(t, nil)
	plan := Plan{Kind: Transcode, VideoCodec: "h264", AudioCodec: "aac"}
	first, err := manager.Create("profile-a", "film-1", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := manager.Create("profile-b", "film-1", plan, 30_000)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := manager.Seek(first.ID, "profile-a", 90_000)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.GenerationID == first.GenerationID || len(executor.processes) != 2 {
		t.Fatal("out-of-window seek did not create a replacement generation")
	}
	if _, ok := manager.Lookup(shared.ID, "profile-b", false); !ok {
		t.Fatal("replacement interrupted the shared generation")
	}
}

func TestRetainedRangeUsesObservedSegmentDurations(t *testing.T) {
	manager, _ := testManager(t, nil)
	session, err := manager.Create("profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", AudioCodec: "aac"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	gen := manager.generations[session.GenerationID]
	manager.mu.Unlock()
	manifest := filepath.Join(gen.dir, "index.m3u8")
	if err := os.WriteFile(manifest, []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:2.5,\nsegment-000000.m4s\n#EXTINF:5.0,\nsegment-000001.m4s\n#EXTINF:3.0,\nsegment-000002.m4s\n#EXTINF:6.0,\nsegment-000003.m4s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	start, end, ok := manager.retainedRangeLocked(gen)
	manager.mu.Unlock()
	if !ok || start != 0 || end != 16_500 {
		t.Fatalf("initial retained range = %d..%d, %v", start, end, ok)
	}
	if err := os.WriteFile(manifest, []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:2\n#EXTINF:3.0,\nsegment-000002.m4s\n#EXTINF:6.0,\nsegment-000003.m4s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	start, end, ok = manager.retainedRangeLocked(gen)
	manager.mu.Unlock()
	if !ok || start != 7_500 || end != 16_500 {
		t.Fatalf("slid retained range = %d..%d, %v", start, end, ok)
	}
	shared, err := manager.Create("profile-b", "film-1", Plan{Kind: Remux, VideoCodec: "h264", AudioCodec: "aac"}, 8_000)
	if err != nil {
		t.Fatal(err)
	}
	if shared.GenerationID != session.GenerationID || shared.StreamOffsetMS != 0 {
		t.Fatalf("shared generation = %q offset %d", shared.GenerationID, shared.StreamOffsetMS)
	}
}

func TestDirectRangeTouchRenewsSession(t *testing.T) {
	manager := NewDirectManager()
	session, err := manager.Create("profile-a", "film-1", Plan{Kind: Direct}, 0)
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	session.ExpiresAt = time.Now().Add(time.Second)
	manager.sessions[session.ID] = session
	manager.mu.Unlock()
	touched, ok := manager.Lookup(session.ID, "profile-a", true)
	if !ok || time.Until(touched.ExpiresAt) < directSessionTTL-time.Minute {
		t.Fatalf("direct touch expiry = %v, ok=%v", touched.ExpiresAt, ok)
	}
}

func TestManagerExpiryAndByteLimitReclaimGenerations(t *testing.T) {
	manager, executor := testManager(t, func(settings *Settings) {
		settings.GenerationBytes = 8
		settings.GlobalBytes = 16
	})
	plan := Plan{Kind: Remux, VideoCodec: "h264", AudioCodec: "aac"}
	session, err := manager.Create("profile-a", "film-1", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if len(status.Generations) != 1 {
		t.Fatalf("generation count = %d", len(status.Generations))
	}
	manager.mu.Lock()
	dir := manager.generations[session.GenerationID].dir
	manager.mu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "segment-000001.m4s"), []byte("more than eight bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.Sweep(time.Now())
	if len(manager.Status().Generations) != 0 || !executor.processes[0].signaled.Load() {
		t.Fatal("oversize generation was not reclaimed")
	}

	second, err := manager.Create("profile-a", "film-1", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	manager.Sweep(second.ExpiresAt.Add(time.Millisecond))
	if _, ok := manager.Lookup(second.ID, "profile-a", false); ok {
		t.Fatal("expired session remains valid")
	}
}

func TestCleanupOrphansRemovesOnlyGenerationDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := claimSegmentDir(afero.NewOsFs(), dir); err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("a", 32)
	if err := os.Mkdir(filepath.Join(dir, orphan), 0o700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(dir, "not-a-generation")
	if err := os.Mkdir(unknown, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CleanupOrphans(afero.NewOsFs(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, orphan)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan remains: %v", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatalf("unknown directory was removed: %v", err)
	}
}

func TestCleanupOrphansRefusesUnownedNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, strings.Repeat("a", 32)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CleanupOrphans(afero.NewOsFs(), dir); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("cleanup error = %v, want invalid settings", err)
	}
}

func TestFFmpegCommandUsesNoShellAndRejectsNonLoopbackInput(t *testing.T) {
	dir := t.TempDir()
	name, args, err := ffmpegCommand(Transcode, "http://127.0.0.1:8787/api/v1/playback/input/server-token", dir, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if name != "ffmpeg" || !strings.Contains(strings.Join(args, " "), "libx264") {
		t.Fatalf("unexpected command: %s %v", name, args)
	}
	joined := strings.Join(args, " ")
	for _, exact := range []string{"-profile:v high", "-level:v 4.0", "-pix_fmt yuv420p", "-r 30", "-b:v 5000000", "-maxrate 5000000", "-bufsize 10000000", "-c:a aac", "-ac 2", "-ar 48000", "-b:a 128000", "-master_pl_name master.m3u8"} {
		if !strings.Contains(joined, exact) {
			t.Fatalf("command %q lacks bounded rendition %q", joined, exact)
		}
	}
	if _, _, err := ffmpegCommand(Remux, "https://media.example/file", dir, 0, time.Minute); err == nil {
		t.Fatal("accepted a non-loopback input")
	}
}

func TestPlaybackSettingsPersistLimitsAndPreSQLiteSegmentDirectory(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := DefaultSettings(dataDir)
	executor := &fakeExecutor{}
	manager, err := NewManager(ManagerConfig{Settings: settings, DB: db, FS: afero.NewOsFs(), InputBase: "http://127.0.0.1:8787", Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	settings.GenerationBytes = 64 << 20
	settings.GlobalBytes = 192 << 20
	settings.MaxGenerations = 3
	if err := manager.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GenerationBytes != settings.GenerationBytes || loaded.MaxGenerations != 3 {
		t.Fatalf("loaded settings = %+v", loaded)
	}
	nextDir := filepath.Join(t.TempDir(), "external-segments")
	settings.SegmentDir = nextDir
	if err := manager.UpdateSettings(settings); !errors.Is(err, ErrRestartRequired) {
		t.Fatalf("directory update = %v, want restart", err)
	}
	beforeSQLite, err := LoadSegmentDir(afero.NewOsFs(), dataDir)
	if err != nil || beforeSQLite != nextDir {
		t.Fatalf("pre-SQLite segment directory = %q, %v", beforeSQLite, err)
	}
}

func TestLeaseJanitorExpiresAbandonedGenerationWithSyntheticTime(t *testing.T) {
	segmentDir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		settings := DefaultSettings(segmentDir)
		settings.LeaseTTL = 3 * time.Second
		settings.HeartbeatInterval = time.Second
		settings.ProcessGrace = 100 * time.Millisecond
		executor := &fakeExecutor{}
		var saved atomic.Int64
		manager, err := NewManager(ManagerConfig{Settings: settings, InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: time.Second, SaveProgress: func(_, _ string, positionMS int64) error { saved.Store(positionMS); return nil }})
		if err != nil {
			t.Fatal(err)
		}
		session, err := manager.Create("profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", AudioCodec: "aac"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Heartbeat(session.ID, "profile-a", 222); err != nil {
			t.Fatal(err)
		}
		time.Sleep(settings.LeaseTTL + time.Second)
		synctest.Wait()
		if _, ok := manager.Lookup(session.ID, "profile-a", false); ok {
			t.Fatal("abandoned session remained after its synthetic-time lease")
		}
		if !executor.processes[0].signaled.Load() {
			t.Fatal("abandoned generation process was not interrupted")
		}
		if got := saved.Load(); got != 222 {
			t.Fatalf("saved progress = %d, want 222", got)
		}
		if err := manager.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
