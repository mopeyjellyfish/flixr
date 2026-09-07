package web_test

import (
	"bytes"
	"encoding/json"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoryAndRatingsAreProfileScopedAndKeepUnknownImportedTime(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := h.CreateProfile("One", "")
	two, _ := h.CreateProfile("Two", "")
	a, _ := h.Select(one.ID, "")
	b, _ := h.Select(two.ID, "")
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(h, c).Handler()
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		if method != "GET" {
			r.Header.Set("Origin", "http://example.com")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/v1/history/import", a, `{"events":[{"catalog_id":"gone","title":"Gone","kind":"film","type":"summary","source_id":"source-1"}]}`); w.Code != 200 {
		t.Fatalf("import=%d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/v1/ratings/gone", a, `{"value":5}`); w.Code != 200 {
		t.Fatalf("rating=%d", w.Code)
	}
	w := request("GET", "/api/v1/history?limit=1", a, "")
	var page struct {
		Events []struct {
			CatalogID  string `json:"catalog_id"`
			SourceTime *int64 `json:"source_time"`
		} `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].CatalogID != "gone" || page.Events[0].SourceTime != nil {
		t.Fatalf("history=%+v", page)
	}
	if w := request("GET", "/api/v1/history", b, ""); w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("gone")) {
		t.Fatalf("isolation=%d %s", w.Code, w.Body)
	}
	clear := request("POST", "/api/v1/history/clear", a, "")
	var action struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(clear.Body).Decode(&action)
	if w := request("GET", "/api/v1/history", a, ""); bytes.Contains(w.Body.Bytes(), []byte("gone")) {
		t.Fatal("clear did not hide")
	}
	if w := request("POST", "/api/v1/history/clear/"+action.ID+"/undo", a, ""); w.Code != 200 {
		t.Fatalf("undo=%d", w.Code)
	}
}
