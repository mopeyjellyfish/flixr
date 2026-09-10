package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

func TestGroupedMemberSidecarsSurviveQualityAndSeek(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	for name, body := range map[string]string{
		"A Canonical.mp4":  "canonical-video",
		"B Member.mp4":     "member-video",
		"B Member.fra.m4a": "member-audio",
		"B Member.eng.vtt": "WEBVTT\n\n00:00.000 --> 00:01.000\nMember subtitle\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	library, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		switch filepath.Ext(file.Name()) {
		case ".m4a":
			return catalog.MediaProperties{Audio: []catalog.AudioTrack{{Index: 0, Codec: "aac", Language: "fra", Default: true, Channels: 2}}}, nil
		case ".vtt":
			return catalog.MediaProperties{Subtitles: []catalog.SubtitleTrack{{Index: 0, Codec: "webvtt", Language: "eng", Default: true}}}, nil
		default:
			return catalog.MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", Width: 1280, Height: 720, Bitrate: 2_000_000, FrameRateMilli: 24_000, BitDepth: 8, DurationMS: 120_000}, nil
		}
	}))
	if err != nil || library.SetRoots(root, "") != nil || library.Scan(t.Context(), 1) != nil {
		t.Fatalf("scan grouped sidecars: %v", err)
	}
	items, err := library.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	canonical, member := items[0], items[1]
	if _, err := library.CreateMediaVersionGroup(t.Context(), "film", canonical.ID, []string{member.ID}); err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := house.CreateProfile("Viewer", "")
	token, _ := house.Select(profile.ID, "")
	settings := playback.DefaultSettings(t.TempDir())
	settings.GenerationBytes = 1 << 20
	settings.GlobalBytes = 4 << 20
	settings.MaxGenerations = 4
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, FS: afero.NewOsFs(), InputBase: "http://127.0.0.1:8787", Executor: &webFakeExecutor{}, ManifestWait: time.Second})
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
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	capabilities := `{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_fmp4_hls":true,"supports_remux":true,"supports_transcode":true,"max_width":1280,"max_height":720,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}`
	planBody := fmt.Sprintf(`{"catalog_id":%q,"version_id":%q,"version_capabilities":{%q:%s}}`, canonical.ID, member.ID, member.ID, capabilities)
	initial := request(http.MethodPost, "/api/v1/playback/plans", planBody)
	if initial.Code != http.StatusCreated {
		t.Fatalf("member sidecar plan = %d %s", initial.Code, initial.Body.String())
	}
	var state struct {
		SessionID   string `json:"session_id"`
		SubtitleURL string `json:"subtitle_url"`
		Plan        struct {
			Kind          playback.Kind `json:"kind"`
			AudioExternal bool          `json:"audio_external"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(initial.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Plan.Kind == playback.Direct || !state.Plan.AudioExternal || state.SubtitleURL == "" {
		t.Fatalf("member sidecar selection = %s", initial.Body.String())
	}
	assertSubtitle := func(url string) {
		t.Helper()
		response := request(http.MethodGet, url, "")
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("Member subtitle")) {
			t.Fatalf("member subtitle = %d %s", response.Code, response.Body.String())
		}
	}
	assertSubtitle(state.SubtitleURL)
	qualityBody := fmt.Sprintf(`{"capabilities":%s,"quality":{"mode":"original"},"position_ms":123,"observation":1}`, capabilities)
	quality := request(http.MethodPost, "/api/v1/playback/sessions/"+state.SessionID+"/quality", qualityBody)
	if quality.Code != http.StatusOK || json.Unmarshal(quality.Body.Bytes(), &state) != nil {
		t.Fatalf("member quality = %d %s", quality.Code, quality.Body.String())
	}
	seek := request(http.MethodPost, "/api/v1/playback/sessions/"+state.SessionID+"/seek", `{"position_ms":321,"observation":2}`)
	if seek.Code != http.StatusOK || json.Unmarshal(seek.Body.Bytes(), &state) != nil {
		t.Fatalf("member seek = %d %s", seek.Code, seek.Body.String())
	}
	assertSubtitle(state.SubtitleURL)
	var canonicalPosition int64
	if err := db.QueryRow(`SELECT position_ms FROM progress WHERE profile_id=? AND catalog_id=?`, profile.ID, canonical.ID).Scan(&canonicalPosition); err != nil || canonicalPosition != 321 {
		t.Fatalf("canonical progress = %d, %v", canonicalPosition, err)
	}
	var memberRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM progress WHERE profile_id=? AND catalog_id=?`, profile.ID, member.ID).Scan(&memberRows); err != nil || memberRows != 0 {
		t.Fatalf("member progress rows = %d, %v", memberRows, err)
	}
}
