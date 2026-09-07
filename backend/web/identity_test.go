package web_test

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

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestIdentityRepairRoutesRequireOwner(t *testing.T) {
	h := newHousehold(t)
	server := web.NewServer(h, catalog.New()).Handler()
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/owner/identity/repairs"}, {"POST", "/api/v1/owner/identity/merges"}, {"POST", "/api/v1/owner/identity/merges/m/unmerge"}} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s: %d", route.method, route.path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+h.SetupToken()+`","password":"passphrase"}`)))
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	r := httptest.NewRequest("GET", "/api/v1/owner/identity/repairs", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "{\"conflicts\":[],\"merges\":[]}\n" {
		t.Fatalf("owner repairs: %d %s", w.Code, w.Body.String())
	}
}

func TestOwnerIdentityRepairRoundTripDoesNotDisclosePaths(t *testing.T) {
	films := t.TempDir()
	for name, content := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Claim(h.SetupToken(), "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := h.Select(profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	server := web.NewServer(h, c).Handler()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	body := fmt.Sprintf(`{"kind":"film","survivor_id":%q,"source_id":%q}`, items[0].ID, items[1].ID)
	if w := request("POST", "/api/v1/owner/identity/merges", body, viewer); w.Code != 403 {
		t.Fatalf("profile merge %d", w.Code)
	}
	w := request("POST", "/api/v1/owner/identity/merges", body, owner)
	if w.Code != 200 {
		t.Fatalf("owner merge %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), films) || strings.Contains(w.Body.String(), "A.mp4") || strings.Contains(w.Body.String(), "relative_path") || strings.Contains(w.Body.String(), "full_digest") {
		t.Fatal("repair leaked physical source")
	}
	var merge catalog.IdentityMerge
	if err := json.Unmarshal(w.Body.Bytes(), &merge); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/owner/identity/merges", body, owner); w.Code != 409 {
		t.Fatalf("repeated merge %d", w.Code)
	}
	path := "/api/v1/owner/identity/merges/" + merge.ID + "/unmerge"
	if w := request("POST", path, "", viewer); w.Code != 403 {
		t.Fatalf("profile unmerge %d", w.Code)
	}
	if w := request("POST", path, "", owner); w.Code != 200 {
		t.Fatalf("owner unmerge %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", path, "", owner); w.Code != 409 {
		t.Fatalf("repeated unmerge %d", w.Code)
	}
}
