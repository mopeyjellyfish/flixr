package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
)

func TestSetupDiagnosticsAreOwnerOnlyPathAwareAndPublicStatusStaysRedacted(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	setupToken := house.SetupToken()
	server := NewServerWithConfigurationValues(house, catalog.New(), nil, nil, nil, map[string]string{
		"server.data_dir":      "/container/config",
		"playback.segment_dir": "/container/cache",
		"metadata.tmdb_token":  "diagnostic-secret",
	})
	server.pathProbe = func(path string, writable bool) setupPathState {
		switch path {
		case "/container/config", "/container/cache", "/media/films":
			return setupPathReady
		case "/media/missing":
			return setupPathMissing
		default:
			return setupPathUnreadable
		}
	}
	handler := server.Handler()

	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	for _, private := range []string{"/container/config", "/container/cache", "/media/films"} {
		if strings.Contains(public.Body.String(), private) {
			t.Fatalf("public setup status leaked %q: %s", private, public.Body.String())
		}
	}

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/owner/setup?films=%2Fmedia%2Ffilms", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer diagnostics status = %d", denied.Code)
	}

	session, err := house.Claim(setupToken, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/setup?films="+url.QueryEscape("/media/films")+"&tv="+url.QueryEscape("/media/missing"), nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("owner diagnostics status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "diagnostic-secret") || strings.Contains(response.Body.String(), setupToken) {
		t.Fatalf("owner setup diagnostics leaked credentials: %s", response.Body.String())
	}
	var body struct {
		Step   string `json:"step"`
		Checks []struct {
			ID, State, Path, Message, Action string
		} `json:"checks"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Step != catalog.SetupProgressChoice {
		t.Fatalf("newly claimed setup step = %q", body.Step)
	}
	checks := make(map[string]struct{ State, Path, Action string })
	for _, check := range body.Checks {
		checks[check.ID] = struct{ State, Path, Action string }{check.State, check.Path, check.Action}
	}
	if got := checks["films"]; got.State != string(setupPathReady) || got.Path != "/media/films" {
		t.Fatalf("films check = %#v", got)
	}
	if got := checks["tv"]; got.State != string(setupPathMissing) || got.Path != "/media/missing" || !strings.Contains(strings.ToLower(got.Action), "compose") {
		t.Fatalf("missing TV check = %#v", got)
	}
	if checks["data"].Path != "/container/config" || checks["cache"].Path != "/container/cache" {
		t.Fatalf("runtime checks = %#v", checks)
	}
}

func TestSetupRecheckBoundsToolCapabilityProbes(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	session, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(house, catalog.New())
	server.lookPath = func(name string) (string, error) { return "/tools/" + name, nil }
	server.toolProbe = func(ctx context.Context, _ string) bool {
		<-ctx.Done()
		return false
	}
	server.readinessTimeout = 5 * time.Millisecond
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/setup", nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	response := httptest.NewRecorder()
	started := time.Now()
	server.Handler().ServeHTTP(response, request)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded recheck took %s", elapsed)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"ffprobe","label":"Media inspection","state":"unavailable"`) {
		t.Fatalf("timed-out readiness = %d %s", response.Code, response.Body.String())
	}
}

func TestSetupProgressTransitionsSurviveRestartWithoutRecreatingResources(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	catalogue := catalog.New()
	session, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(house, catalogue).Handler()
	patch := func(step string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/owner/setup", bytes.NewBufferString(`{"step":"`+step+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := patch(catalog.SetupProgressLibraries); response.Code != http.StatusOK {
		t.Fatalf("start-fresh progress = %d %s", response.Code, response.Body.String())
	}
	if response := patch(catalog.SetupProgressProfile); response.Code != http.StatusOK {
		t.Fatalf("skip progress = %d %s", response.Code, response.Body.String())
	}
	if response := patch("token=private"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid progress = %d %s", response.Code, response.Body.String())
	}
	if len(house.Profiles()) != 0 {
		t.Fatal("progress changes created a profile")
	}
	films, tv := catalogue.Roots()
	if films != "" || tv != "" {
		t.Fatalf("progress changes created roots: %q %q", films, tv)
	}

	restarted := NewServer(house, catalogue).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/setup", nil)
	request.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	response := httptest.NewRecorder()
	restarted.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `"step":"profile"`) {
		t.Fatalf("restarted setup = %d %s", response.Code, response.Body.String())
	}
}

func TestDefaultSetupPathProbeChecksDirectoryKindReadAndWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(file, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probeSetupPath(dir, false); got != setupPathReady {
		t.Fatalf("readable directory = %q", got)
	}
	if got := probeSetupPath(dir, true); got != setupPathReady {
		t.Fatalf("writable directory = %q", got)
	}
	if got := probeSetupPath(file, false); got != setupPathNotDirectory {
		t.Fatalf("regular file = %q", got)
	}
	if got := probeSetupPath(filepath.Join(dir, "missing"), false); got != setupPathMissing {
		t.Fatalf("missing path = %q", got)
	}
}
