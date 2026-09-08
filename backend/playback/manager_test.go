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
	skipManifest bool
	onStart      func()
	inspectStart func([]string)
	startErr     error
}

type pauseSuccessfulManifestStatFS struct {
	afero.Fs
	once     sync.Once
	observed chan struct{}
	release  chan struct{}
}

func (f *pauseSuccessfulManifestStatFS) Stat(name string) (os.FileInfo, error) {
	info, err := f.Fs.Stat(name)
	if err == nil && filepath.Base(name) == "master.m3u8" {
		f.once.Do(func() {
			close(f.observed)
			<-f.release
		})
	}
	return info, err
}

func (e *fakeExecutor) Start(name string, args []string, _ io.Writer) (Process, error) {
	if name != "ffmpeg" {
		return nil, errors.New("unexpected executable")
	}
	if e.inspectStart != nil {
		e.inspectStart(args)
	}
	if e.onStart != nil {
		e.onStart()
	}
	process := newFakeProcess(e.ignoreSignal)
	e.mu.Lock()
	if e.startErr != nil {
		err := e.startErr
		e.startErr = nil
		e.mu.Unlock()
		return nil, err
	}
	e.processes = append(e.processes, process)
	e.commands = append(e.commands, append([]string(nil), args...))
	e.mu.Unlock()
	if !e.skipManifest {
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

func commandInputTokens(args []string) []string {
	var tokens []string
	for index, arg := range args {
		if arg == "-i" && index+1 < len(args) {
			tokens = append(tokens, filepath.Base(args[index+1]))
		}
	}
	return tokens
}

func TestManagerAuthorizesInternalInputsBeforeStartingFFmpeg(t *testing.T) {
	manager, executor := testManager(t, nil)
	inputTokens := make([]string, 0, 2)
	authorized := make([]bool, 0, 2)
	executor.inspectStart = func(args []string) {
		for index, inputToken := range commandInputTokens(args) {
			catalogID, sourceKey, audioStreamIndex, external, ok := manager.InputFile(inputToken)
			inputTokens = append(inputTokens, inputToken)
			wantAudioStreamIndex := -1
			if index == 1 {
				wantAudioStreamIndex = 2
			}
			authorized = append(authorized, ok && catalogID == "film-1" && sourceKey == "source-a" && audioStreamIndex == wantAudioStreamIndex && external == (index == 1))
		}
	}

	plan := Plan{Kind: Remux, SourceKey: "source-a", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000, AudioStreamIndex: 2, AudioSourceStreamIndex: 0, AudioExternal: true, AudioSelected: true}
	session, err := manager.Create("profile-a", "film-1", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(authorized) != 2 || !authorized[0] || !authorized[1] {
		t.Fatalf("internal inputs authorized at FFmpeg start = %v, want primary and external audio", authorized)
	}
	if !manager.Stop(session.ID, "profile-a") {
		t.Fatal("stop failed")
	}
	for _, inputToken := range inputTokens {
		if _, _, _, _, ok := manager.InputFile(inputToken); ok {
			t.Fatal("stopped generation retained internal input authority")
		}
	}
}

func TestManagerRevokesInternalInputsWhenFFmpegFailsToStart(t *testing.T) {
	manager, executor := testManager(t, nil)
	var inputTokens []string
	executor.inspectStart = func(args []string) {
		inputTokens = commandInputTokens(args)
	}
	executor.startErr = errors.New("synthetic startup failure")
	plan := Plan{Kind: Remux, SourceKey: "source-a", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000, AudioStreamIndex: 2, AudioSourceStreamIndex: 0, AudioExternal: true, AudioSelected: true}
	if _, err := manager.Create("profile-a", "film-1", plan, 0); err == nil {
		t.Fatal("create succeeded")
	}
	if len(inputTokens) != 2 {
		t.Fatalf("FFmpeg inputs = %d, want primary and external audio", len(inputTokens))
	}
	for _, inputToken := range inputTokens {
		if _, _, _, _, ok := manager.InputFile(inputToken); ok {
			t.Fatal("failed FFmpeg start retained internal input authority")
		}
	}
}

func TestManagerSharesGenerationUntilLastLeaseStops(t *testing.T) {
	manager, executor := testManager(t, nil)
	plan := Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}
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

func TestManagerStopsOnlyPlaybackOwnedByOneViewerSession(t *testing.T) {
	manager, executor := testManager(t, nil)
	plan := Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}
	first, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", plan, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CreateForViewer("viewer-b", "profile-a", "film-1", plan, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationID != second.GenerationID {
		t.Fatal("viewer sessions did not share the compatible generation")
	}

	manager.StopViewer("viewer-a")
	if _, ok := manager.LookupForViewer(first.ID, "viewer-a", "profile-a", false); ok {
		t.Fatal("stopped viewer retained playback authority")
	}
	if _, ok := manager.LookupForViewer(second.ID, "viewer-b", "profile-a", false); !ok {
		t.Fatal("stopping one viewer interrupted another viewer on the same profile")
	}
	if executor.processes[0].signaled.Load() {
		t.Fatal("shared FFmpeg process stopped while another viewer held a lease")
	}

	manager.StopViewer("viewer-b")
	if !executor.processes[0].signaled.Load() {
		t.Fatal("FFmpeg process remained after its final viewer lease stopped")
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("generation remained after its final viewer lease stopped")
	}
}

func TestManagerSupersedesOnlyOlderPlansForTheSameViewerAndTitle(t *testing.T) {
	manager, _ := testManager(t, nil)
	plan := Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}
	old, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", plan, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	otherDevice, err := manager.CreateForViewer("viewer-b", "profile-a", "film-1", plan, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", plan, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	later, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", plan, 0, 3)
	if err != nil {
		t.Fatal(err)
	}

	manager.StopSupersededPlans(current)

	if _, ok := manager.LookupForViewer(old.ID, old.ViewerID, old.ProfileID, false); ok {
		t.Fatal("older plan retained its lease")
	}
	if _, ok := manager.LookupForViewer(otherDevice.ID, otherDevice.ViewerID, otherDevice.ProfileID, false); !ok {
		t.Fatal("supersession interrupted another device")
	}
	if _, ok := manager.LookupForViewer(current.ID, current.ViewerID, current.ProfileID, false); !ok {
		t.Fatal("supersession stopped the admitted plan")
	}
	if _, ok := manager.LookupForViewer(later.ID, later.ViewerID, later.ProfileID, false); !ok {
		t.Fatal("an earlier response revoked later playback")
	}
}

func TestManagerIgnoresEmptyViewerTeardownIdentity(t *testing.T) {
	manager, _ := testManager(t, nil)
	session, err := manager.Create("profile-a", "film-1", Plan{Kind: Direct}, 0)
	if err != nil {
		t.Fatal(err)
	}

	manager.StopViewer("")
	if _, ok := manager.Lookup(session.ID, "profile-a", false); !ok {
		t.Fatal("an empty viewer identity stopped unrelated playback")
	}
}

func TestManagerDoesNotRetainARevocationFenceWithoutPendingCreates(t *testing.T) {
	manager, _ := testManager(t, nil)

	manager.StopViewer("viewer-a")

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.revokedViewers) != 0 {
		t.Fatalf("retained %d inactive viewer revocation fences", len(manager.revokedViewers))
	}
}

func TestManagerRejectsAViewerCreateThatFinishesAfterTeardown(t *testing.T) {
	manager, executor := testManager(t, nil)
	started := make(chan struct{})
	resume := make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	defer release()
	var inputToken string
	executor.inspectStart = func(args []string) {
		inputToken = commandInputTokens(args)[0]
	}
	executor.onStart = func() {
		close(started)
		<-resume
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 0)
		result <- err
	}()
	<-started

	manager.StopViewer("viewer-a")
	_, _, _, _, stillAuthorized := manager.InputFile(inputToken)
	release()

	if err := <-result; !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("late create error = %v, want session invalid", err)
	}
	if stillAuthorized {
		t.Fatal("viewer teardown left pending input authorized")
	}
	if len(executor.processes) != 1 || !executor.processes[0].signaled.Load() {
		t.Fatal("late FFmpeg candidate was not interrupted")
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("late create retained a generation")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.revokedViewers) != 0 {
		t.Fatal("viewer revocation fence remained after pending create finished")
	}
}

func TestManagerShutdownRevokesInputWhileFFmpegIsStarting(t *testing.T) {
	manager, executor := testManager(t, nil)
	started := make(chan struct{})
	resume := make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	defer release()
	var inputToken string
	executor.inspectStart = func(args []string) {
		inputToken = commandInputTokens(args)[0]
	}
	executor.onStart = func() {
		close(started)
		<-resume
	}
	createResult := make(chan error, 1)
	go func() {
		_, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", Plan{Kind: Transcode}, 0)
		createResult <- err
	}()
	<-started
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- manager.Shutdown(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		closed := manager.closed
		manager.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not close the manager")
		}
		time.Sleep(time.Millisecond)
	}
	_, _, _, _, stillAuthorized := manager.InputFile(inputToken)
	release()
	if err := <-createResult; !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("create error = %v, want session invalid", err)
	}
	if err := <-shutdownResult; err != nil {
		t.Fatal(err)
	}
	if stillAuthorized {
		t.Fatal("shutdown left pending input authorized")
	}
}

func TestManagerRejectsAViewerCreateRevokedWhileWaitingForManifest(t *testing.T) {
	manager, executor := testManager(t, nil)
	executor.skipManifest = true
	result := make(chan error, 1)
	go func() {
		_, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 0)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		registered := len(manager.sessions) == 1
		manager.mu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("create did not register its pending viewer session")
		}
		time.Sleep(time.Millisecond)
	}

	manager.StopViewer("viewer-a")

	if err := <-result; !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("late manifest create error = %v, want session invalid", err)
	}
	if len(executor.processes) != 1 || !executor.processes[0].signaled.Load() {
		t.Fatal("revoked manifest candidate was not interrupted")
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("revoked manifest candidate retained a generation")
	}
}

func TestManagerRejectsAViewerRevokedAfterSuccessfulManifestWait(t *testing.T) {
	settings := DefaultSettings(t.TempDir())
	settings.LeaseTTL = 3 * time.Second
	settings.HeartbeatInterval = time.Second
	settings.ProcessGrace = 10 * time.Millisecond
	settings.GenerationBytes = 1 << 20
	settings.GlobalBytes = 2 << 20
	settings.MaxGenerations = 2
	fs := &pauseSuccessfulManifestStatFS{Fs: afero.NewOsFs(), observed: make(chan struct{}), release: make(chan struct{})}
	executor := &fakeExecutor{}
	manager, err := NewManager(ManagerConfig{Settings: settings, FS: fs, InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: time.Second})
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
	result := make(chan error, 1)
	go func() {
		_, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 0)
		result <- err
	}()
	<-fs.observed

	manager.StopViewer("viewer-a")
	close(fs.release)

	if err := <-result; !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("create error = %v, want session invalid", err)
	}
	if len(executor.processes) != 1 || !executor.processes[0].signaled.Load() {
		t.Fatal("revoked successful manifest candidate was not interrupted")
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("revoked successful manifest candidate retained a generation")
	}
}

func TestManagerForcesKillAfterGracePeriod(t *testing.T) {
	manager, executor := testManager(t, func(settings *Settings) {
		settings.ProcessGrace = 5 * time.Millisecond
	})
	executor.ignoreSignal = true
	session, err := manager.Create("profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 0)
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
	first, err := manager.Create("profile-a", "film-1", plan, 0, 7)
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
	if replacement.ProgressGeneration != 7 {
		t.Fatalf("seek lost progress generation: %d", replacement.ProgressGeneration)
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
	session, err := manager.Create("profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 0)
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
	shared, err := manager.Create("profile-b", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 8_000)
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
	plan := Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}
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
	name, args, err := ffmpegCommand(Plan{Kind: Transcode}, "http://127.0.0.1:8787/api/v1/playback/input/server-token", "", 2, dir, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(args, " ")
	if name != "ffmpeg" || !strings.Contains(command, "libx264") || !strings.Contains(command, "-map 0:2") {
		t.Fatalf("unexpected command: %s %v", name, args)
	}
	joined := strings.Join(args, " ")
	for _, exact := range []string{"-profile:v high", "-level:v 4.0", "-pix_fmt yuv420p", "-r 30", "-b:v 5000000", "-maxrate 5000000", "-bufsize 10000000", "-c:a aac", "-ac 2", "-ar 48000", "-b:a 128000", "-master_pl_name master.m3u8"} {
		if !strings.Contains(joined, exact) {
			t.Fatalf("command %q lacks bounded rendition %q", joined, exact)
		}
	}
	if _, _, err := ffmpegCommand(Plan{Kind: Remux, VideoBitrate: 1}, "https://media.example/file", "", 1, dir, 0, time.Minute); err == nil {
		t.Fatal("accepted a non-loopback input")
	}
	_, externalArgs, err := ffmpegCommand(Plan{Kind: Remux, VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, "http://127.0.0.1:8787/api/v1/playback/input/video", "http://127.0.0.1:8787/api/v1/playback/input/audio", 0, dir, 23*time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	external := strings.Join(externalArgs, " ")
	if strings.Count(external, "-ss 23.000") != 2 || !strings.Contains(external, "-map 1:0") {
		t.Fatalf("external audio command is not source-relative: %v", externalArgs)
	}
}

func TestManagerAudioReplacementUsesDistinctJobAtGenerationLimit(t *testing.T) {
	manager, executor := testManager(t, func(settings *Settings) {
		settings.MaxGenerations = 1
		settings.GlobalBytes = settings.GenerationBytes
	})
	firstPlan := Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000, AudioStreamIndex: 1, AudioSourceStreamIndex: 1, AudioSelected: true}
	first, err := manager.Create("profile-a", "film-1", firstPlan, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan := firstPlan
	secondPlan.AudioStreamIndex = 2
	second, err := manager.Replace(first.ID, "profile-a", secondPlan, 12_345)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Lookup(first.ID, "profile-a", false); ok {
		t.Fatal("replacement kept old session")
	}
	if second.PositionMS != 12_345 || second.GenerationID == first.GenerationID || len(executor.processes) != 2 || !executor.processes[0].signaled.Load() {
		t.Fatalf("replacement = %#v, processes = %d", second, len(executor.processes))
	}
}

func TestManagerAudioReplacementKeepsOldSessionOnStartupFailure(t *testing.T) {
	manager, executor := testManager(t, nil)
	plan := Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000, AudioStreamIndex: 1, AudioSourceStreamIndex: 1, AudioSelected: true}
	first, err := manager.Create("profile-a", "film-1", plan, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	executor.mu.Lock()
	executor.startErr = errors.New("synthetic startup failure")
	executor.mu.Unlock()
	plan.AudioStreamIndex = 2
	if _, err := manager.Replace(first.ID, "profile-a", plan, 12_345); err == nil {
		t.Fatal("replacement succeeded")
	}
	if current, ok := manager.Lookup(first.ID, "profile-a", false); !ok || current.PositionMS != 0 || current.ProgressGeneration != 7 {
		t.Fatalf("failed replacement removed old session: %#v, %v", current, ok)
	}
	manager.mu.Lock()
	inputCount := len(manager.inputs)
	manager.mu.Unlock()
	if inputCount != 1 {
		t.Fatalf("failed replacement retained input authority: got %d, want original generation only", inputCount)
	}
	if len(executor.processes) != 1 || executor.processes[0].signaled.Load() {
		t.Fatal("failed replacement interrupted old generation")
	}
}

func TestSubtitleSelectionDoesNotReplaceMediaSession(t *testing.T) {
	manager, _ := testManager(t, nil)
	plan := Plan{Kind: Direct, SubtitleSources: []SubtitleSource{{Index: 2, SourceIndex: 2, SourceKey: "subtitle-key", Codec: "subrip"}}}
	initial, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", plan, 12_345, 7)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.SelectSubtitle(initial.ID, "viewer-a", "profile-a", 2, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != initial.ID || updated.GenerationID != initial.GenerationID || updated.PositionMS != initial.PositionMS || !updated.Plan.SubtitleSelected || updated.Plan.SubtitleSelectionIndex != 2 {
		t.Fatalf("subtitle update replaced media: before=%#v after=%#v", initial, updated)
	}
	if _, err := manager.SelectSubtitle(initial.ID, "viewer-a", "profile-a", 99, false, true); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("unknown subtitle error = %v", err)
	}
}

func TestSubtitleWorkIsCanceledWhenSessionStops(t *testing.T) {
	manager, _ := testManager(t, nil)
	initial, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", Plan{Kind: Direct}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, release, err := manager.SubtitleContext(context.Background(), initial.ID, "viewer-a", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !manager.StopForViewer(initial.ID, "viewer-a", "profile-a") {
		t.Fatal("session stop failed")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("subtitle work survived session revoke")
	}
	if _, _, err := manager.SubtitleContext(context.Background(), initial.ID, "viewer-a", "profile-a"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("stopped session subtitle context = %v", err)
	}
}

func TestSubtitleWorkIsCanceledWhenManagerShutsDown(t *testing.T) {
	manager := NewDirectManager()
	initial, err := manager.CreateForViewer("viewer-a", "profile-a", "film-1", Plan{Kind: Direct}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, release, err := manager.SubtitleContext(context.Background(), initial.ID, "viewer-a", "profile-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("subtitle work survived manager shutdown")
	}
}

func TestManagerExternalAudioAuthorityEndsWithGeneration(t *testing.T) {
	manager, executor := testManager(t, nil)
	plan := Plan{Kind: Remux, SourceKey: "source-a", VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000, AudioStreamIndex: 2, AudioSourceStreamIndex: 0, AudioExternal: true, AudioSelected: true}
	session, err := manager.Create("profile-a", "film-1", plan, 4_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("commands = %d", len(executor.commands))
	}
	var tokens []string
	for index, arg := range executor.commands[0] {
		if arg == "-i" && index+1 < len(executor.commands[0]) {
			tokens = append(tokens, filepath.Base(executor.commands[0][index+1]))
		}
	}
	if len(tokens) != 2 {
		t.Fatalf("input tokens = %v", tokens)
	}
	if catalogID, audioIndex, external, ok := manager.Input(tokens[1]); !ok || catalogID != "film-1" || audioIndex != 2 || !external {
		t.Fatalf("audio authority = %q %d %v %v", catalogID, audioIndex, external, ok)
	}
	if catalogID, sourceKey, ok := manager.InputSource(tokens[1]); !ok || catalogID != "film-1" || sourceKey != "source-a" {
		t.Fatalf("audio source authority = %q %q %v", catalogID, sourceKey, ok)
	}
	if !manager.Stop(session.ID, "profile-a") {
		t.Fatal("stop failed")
	}
	if _, _, _, ok := manager.Input(tokens[1]); ok {
		t.Fatal("external audio authority survived generation stop")
	}
}

func TestFFmpegCommandSuppliesRemuxBitrateEvidenceForTheMasterPlaylist(t *testing.T) {
	_, args, err := ffmpegCommand(Plan{Kind: Remux, VideoBitrate: 4_000_000, AudioCodec: "aac", AudioBitrate: 192_000}, "http://127.0.0.1:8787/api/v1/playback/input/server-token", "", -1, t.TempDir(), 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, exact := range []string{"-b:v 4000000", "-b:a 192000"} {
		if !strings.Contains(joined, exact) {
			t.Fatalf("remux command %q lacks master-playlist evidence %q", joined, exact)
		}
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
		manager, err := NewManager(ManagerConfig{Settings: settings, InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		session, err := manager.Create("profile-a", "film-1", Plan{Kind: Remux, VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, 0)
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
		if err := manager.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSourceVersionSeparatesGenerationsAndInputAuthority(t *testing.T) {
	manager, _ := testManager(t, nil)
	first, err := manager.Create("p", "film", Plan{Kind: Remux, SourceKey: "original", VideoBitrate: 1_000_000}, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("p", "film", Plan{Kind: Remux, SourceKey: "replacement", VideoBitrate: 1_000_000}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationID == second.GenerationID {
		t.Fatal("replacement shared original generation")
	}
	manager.mu.Lock()
	tokens := make(map[string]string)
	for token, authority := range manager.inputs {
		tokens[token] = authority.sourceKey
	}
	manager.mu.Unlock()
	if len(tokens) != 2 {
		t.Fatalf("input authorities %v", tokens)
	}
	for token, want := range tokens {
		id, key, ok := manager.InputSource(token)
		if !ok || id != "film" || key != want {
			t.Fatalf("input %s %s %v", id, key, ok)
		}
	}
}
