package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestProgressIsolatedByProfileOverHTTP(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at) VALUES('catalog-1','film','Film','film.mp4',1,'film',0)"); err != nil {
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
	oneSession, err := h.Select(one.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	twoSession, err := h.Select(two.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	server := web.NewServer(h, c).Handler()
	request := func(method, session string, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/progress/catalog-1", bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodPut, oneSession, `{"position_ms":123}`).Code; got != http.StatusOK {
		t.Fatalf("write = %d", got)
	}
	w := request(http.MethodGet, twoSession, "")
	if w.Code != http.StatusOK {
		t.Fatalf("read = %d", w.Code)
	}
	var body map[string]int64
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["position_ms"] != 0 {
		t.Fatalf("second profile observed %d", body["position_ms"])
	}
	if got := request(http.MethodPut, twoSession, `{"position_ms":999}`).Code; got != http.StatusOK {
		t.Fatalf("second write = %d", got)
	}
	w = request(http.MethodGet, oneSession, "")
	if w.Code != http.StatusOK {
		t.Fatalf("first read = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["position_ms"] != 123 {
		t.Fatalf("second profile overwrote first position: %d", body["position_ms"])
	}
}
