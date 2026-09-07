package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestPlaybackPlanAndDirectRangeAreProfileBound(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	want := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), want, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,container,video_codec,audio_json,subtitle_json,updated_at) VALUES('film','film','Film','film.mp4',1,'film','mp4','h264','[{"codec":"aac"}]','[]',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('film_root',?)`, root); err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	one, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	two, err := h.CreateProfile("Two", "")
	if err != nil {
		t.Fatal(err)
	}
	oneToken, _ := h.Select(one.ID, "")
	twoToken, _ := h.Select(two.ID, "")
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(h, c).Handler()
	plan := func(token, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/playback/plans", bytes.NewBufferString(`{"catalog_id":"`+id+`","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"],"supports_direct":true}}`))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := plan(oneToken, "film")
	if w.Code != http.StatusCreated {
		t.Fatalf("plan = %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Plan struct {
			Kind string `json:"kind"`
		} `json:"plan"`
		MediaURL  string `json:"media_url"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Plan.Kind != "direct" {
		t.Fatalf("plan %q", result.Plan.Kind)
	}
	r := httptest.NewRequest(http.MethodGet, result.MediaURL, nil)
	r.Header.Set("Range", "bytes=3-6")
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "3456" {
		t.Fatalf("range = %d %q", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodGet, result.MediaURL, nil)
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: twoToken})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross profile = %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions/"+result.SessionID+"/heartbeat", bytes.NewBufferString(`{"position_ms":4321}`))
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: oneToken})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d: %s", w.Code, w.Body.String())
	}
	secondPlan := plan(oneToken, "film")
	var resumed struct {
		ResumeMS int64 `json:"resume_ms"`
	}
	if err := json.NewDecoder(secondPlan.Body).Decode(&resumed); err != nil {
		t.Fatal(err)
	}
	if secondPlan.Code != http.StatusCreated || resumed.ResumeMS != 4321 {
		t.Fatalf("resume plan = %d at %d", secondPlan.Code, resumed.ResumeMS)
	}
	if got := plan(oneToken, "../film").Code; got != http.StatusNotFound {
		t.Fatalf("traversal = %d", got)
	}
}
