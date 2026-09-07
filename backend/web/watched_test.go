package web_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestWatchedActionRemovesFilmFromContinueWatching(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path,duration_ms) VALUES('film','film','Film','film.mp4',1000)"); err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.CreateProfile("Ada", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.Select(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.RecordProgress(p.ID, "film", 500, 100, false); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	server := web.NewServer(h, c).Handler()
	r := httptest.NewRequest(http.MethodPut, "http://flixr.test/api/v1/catalog/watched/film/film", bytes.NewBufferString(`{"watched":true}`))
	r.Header.Set("Origin", "http://flixr.test")
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("watched action = %d: %s", w.Code, w.Body.String())
	}
	view, err := c.Viewer(p.ID, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Sections[0].Items) != 0 {
		t.Fatalf("completed film remained in Continue Watching: %#v", view.Sections[0].Items)
	}
}
