package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestSettingsInventoryRedactsSecretsAndMarksEnvironmentLocks(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServerWithConfiguration(house, catalog.New(), playback.NewDirectManager(), map[string]bool{"library.films_root": true, "metadata.tmdb_token": true})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/owner/settings", nil)
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Settings []struct {
			Key     string `json:"key"`
			Value   string `json:"value"`
			Mutable bool   `json:"mutable"`
			Secret  bool   `json:"secret"`
		} `json:"settings"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	got := map[string]struct {
		Value           string
		Mutable, Secret bool
	}{}
	for _, setting := range body.Settings {
		got[setting.Key] = struct {
			Value           string
			Mutable, Secret bool
		}{setting.Value, setting.Mutable, setting.Secret}
	}
	if got["library.films_root"].Mutable || !got["metadata.tmdb_token"].Secret || got["metadata.tmdb_token"].Value != "environment-managed" {
		t.Fatalf("settings = %#v", got)
	}
}

func TestEnvironmentLockedRootRejectsAtomicUpdate(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	library := catalog.New()
	s := NewServerWithConfiguration(house, library, playback.NewDirectManager(), map[string]bool{"library.films_root": true})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/owner/roots", nil)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	r.Body = io.NopCloser(strings.NewReader(`{"films":"/media/films","tv":"/media/tv"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	films, tv := library.Roots()
	if films != "" || tv != "" {
		t.Fatalf("partial root update: %q %q", films, tv)
	}
}

func TestSettingsImportAppliesOneScopeAndRejectsMixedScopes(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	library := catalog.New()
	s := NewServerWithConfiguration(house, library, playback.NewDirectManager(), nil)
	filmsDir, tvDir := filepath.Join(t.TempDir(), "films"), filepath.Join(t.TempDir(), "tv")
	if err := os.MkdirAll(filmsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tvDir, 0700); err != nil {
		t.Fatal(err)
	}
	request := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/owner/settings/import", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if got := request(`{"version":1,"settings":{"library.films_root":"` + filmsDir + `","library.tv_root":"` + tvDir + `"}}`); got != http.StatusOK {
		t.Fatalf("library import = %d", got)
	}
	films, tv := library.Roots()
	if films != filmsDir || tv != tvDir {
		t.Fatalf("roots = %q %q", films, tv)
	}
	if got := request(`{"version":1,"settings":{"library.films_root":"/other","playback.global_bytes":"1"}}`); got != http.StatusConflict {
		t.Fatalf("mixed import = %d", got)
	}
	films, _ = library.Roots()
	if films != filmsDir {
		t.Fatalf("mixed import partially applied: %q", films)
	}
}

func TestSettingsExportNeverContainsTMDBSecret(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	library := catalog.New()
	if err := library.SetTMDBToken("do-not-export-me"); err != nil {
		t.Fatal(err)
	}
	s := NewServerWithConfiguration(house, library, playback.NewDirectManager(), nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/owner/settings/export", nil)
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "do-not-export-me") || strings.Contains(w.Body.String(), "tmdb_token") {
		t.Fatalf("secret leaked in export: %s", w.Body.String())
	}
}

func TestSettingsExportRoundTripsAndPreviewSharesImportValidation(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	library := catalog.New()
	s := NewServerWithConfiguration(house, library, playback.NewDirectManager(), nil)
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(map[bool]string{true: http.MethodPost, false: http.MethodGet}[body != ""], path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	exported := call("/api/v1/owner/settings/export", "")
	if exported.Code != http.StatusOK {
		t.Fatal(exported.Code)
	}
	if got := call("/api/v1/owner/settings/import/preview", exported.Body.String()); got.Code != http.StatusOK {
		t.Fatalf("export preview = %d: %s", got.Code, got.Body.String())
	}
	if got := call("/api/v1/owner/settings/import", exported.Body.String()); got.Code != http.StatusOK {
		t.Fatalf("export import = %d: %s", got.Code, got.Body.String())
	}
	for _, path := range []string{"/api/v1/owner/settings/import/preview", "/api/v1/owner/settings/import"} {
		if got := call(path, `{"version":1,"settings":{"library.films_root":"/missing","playback.global_bytes":"1"}}`); got.Code != http.StatusConflict {
			t.Fatalf("mixed %s = %d", path, got.Code)
		}
		if got := call(path, `{"version":1,"settings":{"playback.global_bytes":"1"}}`); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s = %d", path, got.Code)
		}
	}
}

func TestLockedPlaybackFieldAllowsUnchangedValueAlongsideEditableLimit(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	settings := playback.DefaultSettings(t.TempDir())
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: settings, DB: db, InputBase: "http://127.0.0.1:1", Executor: &webFakeExecutor{}})
	if err != nil {
		t.Fatal(err)
	}
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	token, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServerWithConfiguration(house, catalog.New(), manager, map[string]bool{"playback.segment_dir": true})
	body := fmt.Sprintf(`{"segment_dir":%q,"generation_bytes":%d,"global_bytes":%d,"max_generations":%d}`, settings.SegmentDir, settings.GenerationBytes, settings.GlobalBytes*2, settings.MaxGenerations)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/owner/settings/playback", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("editable playback limit status = %d: %s", w.Code, w.Body.String())
	}
}
