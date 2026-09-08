package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

func TestPlaybackPlannerReceivesExactPrimaryStreamProperties(t *testing.T) {
	item := catalog.Item{MediaProperties: catalog.MediaProperties{Container: "matroska", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 12, Width: 320, Height: 180, Bitrate: 157945, FrameRateMilli: 24000, BitDepth: 8, Audio: []catalog.AudioTrack{{Codec: "aac", Profile: "LC", Channels: 1, SampleRate: 48000, Bitrate: 157945}}}}
	got := mediaProperties(item, item.Audio[0], true)
	if got.VideoLevel != 12 || got.VideoBitrate != 157945 || got.AudioProfile != "LC" || got.AudioSampleRate != 48000 || got.AudioBitrate != 157945 {
		t.Fatalf("planner media = %#v", got)
	}
}

type webFakeProcess struct {
	once     sync.Once
	done     chan struct{}
	signaled bool
}

func (p *webFakeProcess) Signal(os.Signal) error {
	p.signaled = true
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *webFakeProcess) Kill() error { p.once.Do(func() { close(p.done) }); return nil }
func (p *webFakeProcess) Wait() error { <-p.done; return nil }

type webFakeExecutor struct {
	process  *webFakeProcess
	inputURL string
	onStart  func()
	startErr error
}

func TestPreferredAudioAvoidsCommentaryAndFallsBackToSourceDefault(t *testing.T) {
	tracks := []catalog.AudioTrack{
		{Index: 1, Language: "eng", Title: "English"},
		{Index: 2, Language: "eng", Title: "Director Commentary", Default: true},
		{Index: 3, Language: "fra", Title: "French"},
	}
	selected, ok := selectedAudio(tracks, nil, false, "eng")
	if !ok || selected.Index != 1 {
		t.Fatalf("preferred English = %#v, %v", selected, ok)
	}
	selected, ok = selectedAudio(tracks, nil, false, "deu")
	if !ok || selected.Index != 2 {
		t.Fatalf("missing preference fallback = %#v, %v", selected, ok)
	}
}

func TestSubtitleSelectionPreservesExplicitOffAndForcedLanguagePolicy(t *testing.T) {
	tracks := []catalog.SubtitleTrack{
		{Index: 2, Codec: "subrip", Language: "eng", Title: "English", Default: true},
		{Index: 3, Codec: "subrip", Language: "fra", Title: "French signs", Forced: true},
		{Index: 4, Codec: "subrip", Language: "eng", Title: "English SDH", SDH: true},
	}

	if selected, ok := selectedSubtitle(tracks, nil, false, household.SubtitlePreference{Mode: household.SubtitleOff}); ok {
		t.Fatalf("explicit off selected %#v", selected)
	}
	selected, ok := selectedSubtitle(tracks, nil, false, household.SubtitlePreference{Mode: household.SubtitleAutomatic, Language: "fra"})
	if !ok || selected.Index != 3 || !selected.Forced {
		t.Fatalf("forced French = %#v, %v", selected, ok)
	}
	selected, ok = selectedSubtitle(tracks, nil, false, household.SubtitlePreference{Mode: household.SubtitleAutomatic, Language: "eng", PreferSDH: true})
	if !ok || selected.Index != 4 || !selected.SDH {
		t.Fatalf("English SDH = %#v, %v", selected, ok)
	}
	requested := 2
	selected, ok = selectedSubtitle(tracks, &requested, false, household.SubtitlePreference{Mode: household.SubtitleOff})
	if !ok || selected.Index != 2 {
		t.Fatalf("explicit track over off = %#v, %v", selected, ok)
	}
}

func TestSubtitleSelectionFallsBackToSourceDefault(t *testing.T) {
	tracks := []catalog.SubtitleTrack{{Index: 2, Codec: "subrip", Language: "eng"}, {Index: 3, Codec: "subrip", Language: "deu", Default: true}}
	selected, ok := selectedSubtitle(tracks, nil, false, household.SubtitlePreference{Mode: household.SubtitleAutomatic, Language: "fra"})
	if !ok || selected.Index != 3 {
		t.Fatalf("source default = %#v, %v", selected, ok)
	}
}

func (e *webFakeExecutor) Start(_ string, args []string, _ io.Writer) (playback.Process, error) {
	if e.onStart != nil {
		e.onStart()
	}
	if e.startErr != nil {
		return nil, e.startErr
	}
	e.process = &webFakeProcess{done: make(chan struct{})}
	for index, arg := range args {
		if arg == "-i" && index+1 < len(args) {
			e.inputURL = args[index+1]
		}
	}
	dir := filepath.Dir(args[len(args)-1])
	files := afero.Afero{Fs: afero.NewOsFs()}
	if err := files.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4,\nsegment-000001.m4s\n"), 0o600); err != nil {
		return nil, err
	}
	if err := files.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=320x180,CODECS=\"avc1.640028,mp4a.40.2\"\nindex.m3u8\n"), 0o600); err != nil {
		return nil, err
	}
	if err := files.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600); err != nil {
		return nil, err
	}
	if err := files.WriteFile(filepath.Join(dir, "segment-000001.m4s"), []byte("segment"), 0o600); err != nil {
		return nil, err
	}
	return e.process, nil
}

func TestHLSPlaybackLeaseInputAndProgressAreProfileBound(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	media := []byte("loopback media")
	if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(root, "film.mkv"), media, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,container,video_codec,video_profile,video_level,video_width,video_height,video_bitrate,video_frame_rate_milli,video_bit_depth,audio_json,subtitle_json,updated_at) VALUES('film','film','Film','film.mkv',1,'film','matroska,webm','h264','High',12,320,180,157945,24000,8,'[{"codec":"aac","profile":"LC","channels":2,"sample_rate":48000,"bitrate":157945}]','[]',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('film_root',?)`, root); err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := house.CreateProfile("One", "")
	two, _ := house.CreateProfile("Two", "")
	oneToken, _ := house.Select(one.ID, "")
	twoToken, _ := house.Select(two.ID, "")
	library, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	settings := playback.DefaultSettings(t.TempDir())
	settings.GenerationBytes = 1 << 20
	settings.GlobalBytes = 1 << 20
	settings.MaxGenerations = 1
	executor := &webFakeExecutor{}
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, FS: afero.NewOsFs(), InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	server := NewServerWithPlayback(house, library, manager)
	server.readyMu.Lock()
	server.readiness.FFmpeg = true
	server.readyMu.Unlock()
	handler := server.Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_remux":true,"max_width":320,"max_height":180,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("plan = %d: %s", response.Code, response.Body.String())
	}
	var plan struct {
		SessionID string `json:"session_id"`
		MediaURL  string `json:"media_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.SessionID == "" || filepath.Ext(plan.MediaURL) != ".m3u8" {
		t.Fatalf("unexpected plan: %+v", plan)
	}

	request = httptest.NewRequest(http.MethodGet, plan.MediaURL, nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || !bytes.Contains(response.Body.Bytes(), []byte(`CODECS="avc1.640028,mp4a.40.2"`)) || !bytes.Contains(response.Body.Bytes(), []byte("index.m3u8")) {
		t.Fatalf("manifest = %d: %s", response.Code, response.Body.String())
	}
	for _, asset := range []struct{ name, contentType string }{{"index.m3u8", "application/vnd.apple.mpegurl"}, {"init.mp4", "video/mp4"}, {"segment-000001.m4s", "video/mp4"}} {
		request = httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/"+plan.SessionID+"/"+asset.name, nil)
		request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != asset.contentType {
			t.Fatalf("%s = %d, content type %q", asset.name, response.Code, response.Header().Get("Content-Type"))
		}
	}
	request = httptest.NewRequest(http.MethodGet, plan.MediaURL, nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: twoToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-profile manifest = %d", response.Code)
	}

	input, err := url.Parse(executor.inputURL)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, input.Path, nil)
	request.RemoteAddr = "127.0.0.1:4567"
	request.Header.Set("Range", "bytes=2-5")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != string(media[2:6]) {
		t.Fatalf("loopback range = %d %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, input.Path, nil)
	request.RemoteAddr = "192.0.2.10:4567"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote input = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/heartbeat", bytes.NewBufferString(`{"position_ms":1234}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d: %s", response.Code, response.Body.String())
	}
	if position, err := house.Position(oneToken, "film"); err != nil || position != 1234 {
		t.Fatalf("position = %d, %v", position, err)
	}
	// A different plan cannot fit beside the active HLS generation. Admission
	// failure must leave the original session's progress authority intact.
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"film","capabilities":{"video_codecs":["h264"],"audio_codecs":["aac"],"video_profiles":["Baseline"],"supports_fmp4_hls":true,"supports_transcode":true}}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("competing plan=%d %s", response.Code, response.Body)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/heartbeat", bytes.NewBufferString(`{"position_ms":2000,"observation":2}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if position, err := house.Position(oneToken, "film"); err != nil || position != 2000 {
		t.Fatalf("failed plan fenced active session: position=%d %v", position, err)
	}
	// A delayed seek must not move either durable progress or the live HLS lease.
	for _, event := range []struct{ route, body string }{{"heartbeat", `{"position_ms":3000,"observation":3}`}, {"seek", `{"position_ms":1000,"observation":2}`}} {
		request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/"+event.route, bytes.NewBufferString(event.body))
		request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("ordered %s: %d %s", event.route, response.Code, response.Body)
		}
	}
	if position, err := house.Position(oneToken, "film"); err != nil || position != 3000 {
		t.Fatalf("delayed seek position=%d %v", position, err)
	}
	if current, ok := manager.Lookup(plan.SessionID, one.ID, false); !ok || current.PositionMS != 3000 {
		t.Fatalf("delayed seek changed lease: %+v", current)
	}
	if err := house.SetWatched(one.ID, []string{"film"}, true); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/seek", bytes.NewBufferString(`{"position_ms":1000,"observation":4}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("stale manual seek: %d", response.Code)
	}
	var completed int
	if err := db.QueryRow(`SELECT completed FROM progress WHERE profile_id=? AND catalog_id='film'`, one.ID).Scan(&completed); err != nil || completed != 1 {
		t.Fatalf("seek undid manual watched: %d %v", completed, err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/stop", nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !executor.process.signaled {
		t.Fatalf("stop = %d, signaled=%v", response.Code, executor.process.signaled)
	}
	// A manual reset while FFmpeg prepares must defeat the candidate's CAS.
	executor.onStart = func() {
		if err := house.SetWatched(one.ID, []string{"film"}, false); err != nil {
			t.Fatal(err)
		}
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_remux":true,"max_width":320,"max_height":180,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("manual raced preparation: %d %s", response.Code, response.Body)
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("conflicted candidate leaked a generation")
	}
	if position, err := house.Position(oneToken, "film"); err != nil || position != 0 {
		t.Fatalf("candidate undid manual reset=%d %v", position, err)
	}

	// Cancellation while preparing has no durable effect and retires the candidate.
	if err := library.SetContinueWatchingDismissed(one.ID, "film", "film", true); err != nil {
		t.Fatal(err)
	}
	_, beforeGeneration, err := house.ProgressState(oneToken, "film")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor.onStart = cancel
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"film","continue_watching_intent":"user","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_remux":true,"max_width":320,"max_height":180,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}}`)).WithContext(ctx)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if _, afterGeneration, err := house.ProgressState(oneToken, "film"); err != nil || afterGeneration != beforeGeneration {
		t.Fatalf("canceled plan advanced generation: %d -> %d (%v)", beforeGeneration, afterGeneration, err)
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("canceled plan leaked a generation")
	}
	var dismissals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM profile_continue_watching_dismissals WHERE profile_id=? AND catalog_kind='film' AND catalog_id='film'`, one.ID).Scan(&dismissals); err != nil || dismissals != 1 {
		t.Fatalf("canceled plan restored Continue Watching dismissal: count=%d, err=%v", dismissals, err)
	}

}

func TestAudioSelectionPersistsLanguageAndPreservesPosition(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	for _, name := range []string{"one.mp4", "two.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	audio := `[{"index":1,"codec":"aac","profile":"LC","channels":2,"sample_rate":48000,"bitrate":128000,"language":"eng","title":"English","default":true},{"index":2,"codec":"aac","profile":"LC","channels":2,"sample_rate":48000,"bitrate":128000,"language":"fra","title":"French"},{"index":3,"codec":"aac","profile":"LC","channels":2,"sample_rate":48000,"bitrate":128000,"language":"und","title":"Director Commentary"}]`
	for _, row := range []struct{ id, path string }{{"one", "one.mp4"}, {"two", "two.mp4"}} {
		if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,container,video_codec,video_profile,video_level,video_width,video_height,video_bitrate,video_frame_rate_milli,video_bit_depth,audio_json,subtitle_json,updated_at) VALUES(?,?,?,?,1,'film','mp4','h264','High',12,320,180,1000000,24000,8,?,'[]',0)`, row.id, "film", row.id, row.path, audio); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('film_root',?)`, root); err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := house.CreateProfile("One", "")
	two, _ := house.CreateProfile("Two", "")
	oneToken, _ := house.Select(one.ID, "")
	twoToken, _ := house.Select(two.ID, "")
	library, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	settings := playback.DefaultSettings(t.TempDir())
	settings.GenerationBytes = 1 << 20
	settings.GlobalBytes = 4 << 20
	settings.MaxGenerations = 4
	executor := &webFakeExecutor{}
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, FS: afero.NewOsFs(), InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	server := NewServerWithPlayback(house, library, manager)
	server.readyMu.Lock()
	server.readiness.FFmpeg = true
	server.readyMu.Unlock()
	handler := server.Handler()
	capabilities := `"capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_direct":true,"supports_remux":true,"supports_transcode":true,"max_width":320,"max_height":180,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}`
	plan := func(token, catalogID, extra string) *httptest.ResponseRecorder {
		body := `{"catalog_id":"` + catalogID + `",` + capabilities + extra + `}`
		request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(body))
		request.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	initial := plan(oneToken, "one", "")
	if initial.Code != http.StatusCreated {
		t.Fatalf("initial plan = %d: %s", initial.Code, initial.Body.String())
	}
	var first struct {
		SessionID          string `json:"session_id"`
		ProgressGeneration int64  `json:"progress_generation"`
		Plan               struct {
			Kind             playback.Kind `json:"kind"`
			AudioStreamIndex int           `json:"audio_stream_index"`
		} `json:"plan"`
		AudioTracks []catalog.AudioTrack `json:"audio_tracks"`
	}
	if err := json.NewDecoder(initial.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.Plan.Kind != playback.Direct || first.Plan.AudioStreamIndex != 1 || len(first.AudioTracks) != 3 || first.AudioTracks[2].Title != "Director Commentary" {
		t.Fatalf("initial selection = %#v", first)
	}

	executor.startErr = errors.New("synthetic startup failure")
	failedBody := `{"audio_stream_index":2,"position_ms":1234,"observation":1,` + capabilities + `}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+first.SessionID+"/audio", bytes.NewBufferString(failedBody))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failed audio switch = %d: %s", response.Code, response.Body.String())
	}
	if language, err := house.AudioLanguage(one.ID); err != nil || language != "" {
		t.Fatalf("failed switch saved language = %q, %v", language, err)
	}
	if _, ok := manager.Lookup(first.SessionID, one.ID, false); !ok {
		t.Fatal("failed switch revoked active session")
	}
	executor.startErr = nil

	switchBody := `{"audio_stream_index":2,"position_ms":4321,"observation":2,` + capabilities + `}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+first.SessionID+"/audio", bytes.NewBufferString(switchBody))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("audio switch = %d: %s", response.Code, response.Body.String())
	}
	var french struct {
		SessionID          string `json:"session_id"`
		ProgressGeneration int64  `json:"progress_generation"`
		ResumeMS           int64  `json:"resume_ms"`
		Plan               struct {
			Kind             playback.Kind `json:"kind"`
			AudioStreamIndex int           `json:"audio_stream_index"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&french); err != nil {
		t.Fatal(err)
	}
	if french.ResumeMS != 4321 || french.Plan.Kind != playback.Remux || french.Plan.AudioStreamIndex != 2 || french.SessionID == first.SessionID || french.ProgressGeneration != first.ProgressGeneration {
		t.Fatalf("French replacement = %#v", french)
	}
	if language, err := house.AudioLanguage(one.ID); err != nil || language != "fra" {
		t.Fatalf("saved language = %q, %v", language, err)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+french.SessionID+"/audio", bytes.NewBufferString(`{"audio_stream_index":3,"position_ms":5000,"observation":3,`+capabilities+`}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unknown-language switch = %d: %s", response.Code, response.Body.String())
	}
	if language, _ := house.AudioLanguage(one.ID); language != "fra" {
		t.Fatalf("unknown language replaced useful preference with %q", language)
	}

	next := plan(oneToken, "two", "")
	if next.Code != http.StatusCreated || !bytes.Contains(next.Body.Bytes(), []byte(`"audio_stream_index":2`)) {
		t.Fatalf("next episode preference = %d: %s", next.Code, next.Body.String())
	}
	invalid := plan(oneToken, "one", `,"audio_stream_index":99`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid index = %d: %s", invalid.Code, invalid.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+french.SessionID+"/audio", bytes.NewBufferString(switchBody))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: twoToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-profile audio switch = %d", response.Code)
	}
}

func TestCanceledAudioPreparationPreservesLiveSession(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("film"), 0o600); err != nil {
		t.Fatal(err)
	}
	audio := `[{"index":1,"codec":"aac","profile":"LC","channels":2,"sample_rate":48000,"bitrate":128000,"language":"eng","default":true},{"index":2,"codec":"aac","profile":"LC","channels":2,"sample_rate":48000,"bitrate":128000,"language":"fra"}]`
	if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,container,video_codec,video_profile,video_level,video_width,video_height,video_bitrate,video_frame_rate_milli,video_bit_depth,audio_json,subtitle_json,updated_at) VALUES('film','film','Film','film.mp4',1,'film','mp4','h264','High',12,320,180,1000000,24000,8,?,'[]',0)`, audio); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('film_root',?)`, root); err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Select(profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	library, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	settings := playback.DefaultSettings(t.TempDir())
	settings.GenerationBytes = 1 << 20
	settings.GlobalBytes = 4 << 20
	settings.MaxGenerations = 4
	executor := &webFakeExecutor{}
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, FS: afero.NewOsFs(), InputBase: "http://127.0.0.1:8787", Executor: executor, ManifestWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	server := NewServerWithPlayback(house, library, manager)
	server.readyMu.Lock()
	server.readiness.FFmpeg = true
	server.readyMu.Unlock()
	handler := server.Handler()
	capabilities := `"capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_direct":true,"supports_remux":true,"supports_transcode":true,"max_width":320,"max_height":180,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}`

	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"film",`+capabilities+`}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("initial plan = %d: %s", response.Code, response.Body.String())
	}
	var initial struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor.onStart = cancel
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+initial.SessionID+"/audio", bytes.NewBufferString(`{"audio_stream_index":2,"position_ms":1234,"observation":1,`+capabilities+`}`)).WithContext(ctx)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if language, err := house.AudioLanguage(profile.ID); err != nil || language != "" {
		t.Fatalf("canceled request saved preference = %q, %v", language, err)
	}
	if _, ok := manager.Lookup(initial.SessionID, profile.ID, false); !ok {
		t.Fatal("canceled request revoked original session")
	}
	if len(manager.Status().Generations) != 0 {
		t.Fatal("canceled request leaked candidate generation")
	}
	if executor.process == nil || !executor.process.signaled {
		t.Fatal("canceled request did not retire candidate process")
	}
}
