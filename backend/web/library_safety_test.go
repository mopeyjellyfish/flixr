package web_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestOwnerReviewsAndConfirmsSuspiciousLibraryRemoval(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.mp4", "two.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || library.SetRoots(root, "") != nil || library.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, err := library.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("initial catalog: %#v, %v", items, err)
	}
	for _, name := range []string{"one.mp4", "two.mp4"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := library.Scan(t.Context(), 1); err == nil {
		t.Fatal("empty root did not require review")
	}
	locations, err := library.LibraryLocations()
	if err != nil || len(locations) != 1 {
		t.Fatalf("library locations: %#v, %v", locations, err)
	}

	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := house.Claim(house.SetupToken(), "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := house.Select(profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(house, library).Handler()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "/api/v1/owner/scan/status", "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("anonymous status = %d", response.Code)
	}
	status := request(http.MethodGet, "/api/v1/owner/scan/status", "", owner)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"state":"review_required"`) || strings.Contains(status.Body.String(), root) || strings.Contains(status.Body.String(), "one.mp4") {
		t.Fatalf("owner status = %d %s", status.Code, status.Body.String())
	}
	body, err := json.Marshal(map[string]string{"scan_id": locations[0].PendingScanID, "root_kind": "film"})
	if err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPost, "/api/v1/owner/scan/removals/confirm", string(body), viewer); response.Code != http.StatusForbidden {
		t.Fatalf("profile cleanup = %d", response.Code)
	}
	if response := request(http.MethodPost, "/api/v1/owner/scan/removals/confirm", `{"scan_id":"stale","root_kind":"film"}`, owner); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "removal_review_changed") {
		t.Fatalf("stale cleanup = %d %s", response.Code, response.Body.String())
	}
	confirmed := request(http.MethodPost, "/api/v1/owner/scan/removals/confirm", string(body), owner)
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"state":"available"`) {
		t.Fatalf("confirmed cleanup = %d %s", confirmed.Code, confirmed.Body.String())
	}
	for _, item := range items {
		if after, ok := library.Item(item.ID); !ok || after.Playable {
			t.Fatalf("confirmed item = %#v, exists=%v", after, ok)
		}
	}
}
