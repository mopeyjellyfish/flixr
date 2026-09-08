package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

func TestSubtitleEndpointIsProfileBoundInertAndDoesNotReplacePlayback(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Film.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Film.eng.Forced.srt"), []byte("1\n00:00:05,000 --> 00:00:06,000\n<script>alert(1)</script>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prober := catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		info, _ := file.Stat()
		if info.Size() == int64(len("video")) {
			return catalog.MediaProperties{Container: "mp4", VideoCodec: "h264"}, nil
		}
		return catalog.MediaProperties{Subtitles: []catalog.SubtitleTrack{{Index: 0, Codec: "subrip"}}}, nil
	})
	library, err := catalog.OpenWithProber(db, prober)
	if err != nil || library.SetRoots(root, "") != nil || library.Scan(context.Background(), 1) != nil {
		t.Fatalf("catalog: %v", err)
	}
	items, _ := library.List("", 0, 10)
	house, _ := household.Open(db)
	one, _ := house.CreateProfile("One", "")
	two, _ := house.CreateProfile("Two", "")
	oneToken, _ := house.Select(one.ID, "")
	twoToken, _ := house.Select(two.ID, "")
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: playback.DefaultSettings(t.TempDir()), DB: db, FS: afero.NewOsFs(), Executor: &webFakeExecutor{}, InputBase: "http://127.0.0.1:8787"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithPlayback(house, library, manager)
	handler := server.Handler()
	planBody := `{"catalog_id":"` + items[0].ID + `","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":[],"supports_direct":true}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(planBody))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("plan = %d: %s", response.Code, response.Body.String())
	}
	var plan struct {
		SessionID        string                  `json:"session_id"`
		SubtitleURL      string                  `json:"subtitle_url"`
		SubtitleTracks   []catalog.SubtitleTrack `json:"subtitle_tracks"`
		SelectedSubtitle *catalog.SubtitleTrack  `json:"selected_subtitle"`
	}
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.SubtitleURL == "" || plan.SelectedSubtitle == nil || !plan.SelectedSubtitle.Forced || len(plan.SubtitleTracks) != 1 {
		t.Fatalf("subtitle plan = %#v", plan)
	}
	request = httptest.NewRequest(http.MethodGet, plan.SubtitleURL, nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>") || !strings.Contains(response.Body.String(), "&lt;script&gt;") {
		t.Fatalf("subtitle response = %d: %s", response.Code, response.Body.String())
	}
	admittedItem, err := library.PlaybackItemContext(context.Background(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	viewerID, _ := house.SessionIdentity(oneToken)
	hlsPlan := playback.Plan{Kind: playback.Transcode, SourceKey: admittedItem.SourceKey(), SubtitleSources: subtitleSources(admittedItem.Subtitles), SubtitleSelectionIndex: admittedItem.Subtitles[0].Index, SubtitleExternal: true, SubtitleSelected: true}
	hlsSession, err := manager.CreateForViewer(viewerID, one.ID, admittedItem.ID, hlsPlan, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	hlsURL := "/api/v1/playback/sessions/" + hlsSession.ID + "/subtitle.vtt?index=" + strconv.Itoa(admittedItem.Subtitles[0].Index) + "&external=true"
	request = httptest.NewRequest(http.MethodGet, hlsURL, nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "00:00:03.000 --> 00:00:04.000") {
		t.Fatalf("source-relative HLS subtitle = %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, plan.SubtitleURL, nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: twoToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-profile subtitle = %d", response.Code)
	}
	if err := os.WriteFile(filepath.Join(root, "Film.eng.Forced.srt"), []byte("1\n00:00:05,000 --> 00:00:06,000\nchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, plan.SubtitleURL, nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("changed sidecar = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+plan.SessionID+"/subtitle", bytes.NewBufferString(`{"mode":"off"}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("subtitle off = %d: %s", response.Code, response.Body.String())
	}
	var off struct {
		SessionID   string `json:"session_id"`
		SubtitleURL string `json:"subtitle_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&off); err != nil {
		t.Fatal(err)
	}
	if off.SessionID != plan.SessionID || off.SubtitleURL != "" {
		t.Fatalf("off replaced playback = %#v", off)
	}
	if preference, err := house.SubtitlePreference(one.ID); err != nil || preference.Mode != household.SubtitleOff {
		t.Fatalf("saved off = %#v, %v", preference, err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(planBody))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("recovery plan = %d: %s", response.Code, response.Body.String())
	}
	var recovered struct {
		SubtitleURL      string                 `json:"subtitle_url"`
		SelectedSubtitle *catalog.SubtitleTrack `json:"selected_subtitle"`
	}
	if err := json.NewDecoder(response.Body).Decode(&recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.SubtitleURL != "" || recovered.SelectedSubtitle != nil {
		t.Fatalf("explicit off did not survive replan: %#v", recovered)
	}
}
