package web

import (
	"bytes"
	"context"
	"encoding/json"
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
	got := mediaProperties(item)
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
}

func (e *webFakeExecutor) Start(_ string, args []string, _ io.Writer) (playback.Process, error) {
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
	if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,container,video_codec,video_profile,audio_json,subtitle_json,updated_at) VALUES('film','film','Film','film.mkv',1,'film','matroska,webm','h264','High','[{"codec":"aac"}]','[]',0)`); err != nil {
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

	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_remux":true}}`))
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
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/stop", nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !executor.process.signaled {
		t.Fatalf("stop = %d, signaled=%v", response.Code, executor.process.signaled)
	}
}
