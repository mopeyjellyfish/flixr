//go:build media_integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/spf13/afero"
)

func TestPlayerInsightsAreRealAndSessionBound(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library, err := catalog.Open(db)
	if err != nil || library.SetRoots("", "../testdata/media/tv") != nil || library.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan media corpus: %v", err)
	}
	items, err := library.List("Signal", 0, 10)
	if err != nil || len(items) == 0 {
		t.Fatalf("Signal items = %#v, %v", items, err)
	}
	var media catalog.Item
	for _, item := range items {
		if len(item.Subtitles) > 0 {
			media = item
			break
		}
	}
	if media.ID == "" {
		t.Fatalf("no embedded text subtitle in Signal items: %#v", items)
	}
	house, _ := household.Open(db)
	profile, _ := house.CreateProfile("Viewer", "")
	token, _ := house.Select(profile.ID, "")
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: playback.DefaultSettings(t.TempDir()), DB: db, FS: afero.NewOsFs(), Executor: &webFakeExecutor{}, InputBase: "http://127.0.0.1:8787"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithPlayback(house, library, manager)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"`+media.ID+`","capabilities":{"containers":["matroska","webm"],"video_codecs":["h264"],"video_profiles":["High"],"audio_codecs":["aac"],"supports_direct":true,"max_width":320,"max_height":180,"max_frame_rate_milli":24000,"max_bit_depth":8,"max_audio_channels":2}}`))
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("plan = %d: %s", response.Code, response.Body.String())
	}
	var plan struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil || plan.SessionID == "" {
		t.Fatalf("subtitle plan = %#v, %v", plan, err)
	}

	asset := "/api/v1/playback/sessions/" + plan.SessionID
	get := func(path, cookie string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "flixr_session", Value: cookie})
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res
	}
	for _, suffix := range []string{"/chapters", "/preview.jpg?position_ms=0"} {
		res := get(asset+suffix, token)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", suffix, res.Code, res.Body.String())
		}
		if strings.Contains(suffix, "preview") && res.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatal("not a JPEG")
		}
		if res.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("shared cache allowed")
		}
		if res := get(asset+suffix, ""); res.Code < 400 {
			t.Fatal("anonymous preview allowed")
		}
	}
	other, _ := house.CreateProfile("Other", "")
	otherToken, _ := house.Select(other.ID, "")
	if res := get(asset+"/chapters", otherToken); res.Code < 400 {
		t.Fatal("cross-profile chapters allowed")
	}
	for _, position := range []string{"-1", "99999999999", "NaN"} {
		if res := get(asset+"/preview.jpg?position_ms="+position, token); res.Code != http.StatusBadRequest {
			t.Fatalf("invalid position: %d", res.Code)
		}
	}
	viewerID, _ := house.SessionIdentity(token)
	if !manager.StopForViewer(plan.SessionID, viewerID, profile.ID) {
		t.Fatal("stop failed")
	}
	for _, suffix := range []string{"/chapters", "/preview.jpg?position_ms=0"} {
		if res := get(asset+suffix, token); res.Code < 400 {
			t.Fatal("stopped session served cached asset")
		}
	}

}
