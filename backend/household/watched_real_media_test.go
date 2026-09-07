//go:build media_integration

package household_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestEndedRealMediaPersistsPerProfileAcrossReopen(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	source := filepath.Join("..", "testdata", "media", "films", "Blue Horizon 2026.mp4")
	media, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "Film.mp4"), media, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	library, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = library.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err = library.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, _, err := library.Browse("", 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("real media scan: %#v %v", items, err)
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
	oneToken, err := h.Select(one.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	twoToken, err := h.Select(two.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := playback.NewManager(playback.ManagerConfig{Settings: playback.DefaultSettings(t.TempDir()), InputBase: "http://127.0.0.1:8787", Executor: playback.OSExecutor{}, SaveProgress: h.ProgressForProfile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	handler := web.NewServerWithPlayback(h, library, manager).Handler()
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	plan := func(token string) string {
		t.Helper()
		w := request("POST", "/api/v1/playback/plans", token, fmt.Sprintf(`{"catalog_id":%q,"capabilities":{"containers":["mp4"],"video_codecs":["h264"],"video_profiles":["High","Main","Baseline","Constrained Baseline"],"audio_codecs":["aac"]}}`, items[0].ID))
		if w.Code != 201 {
			t.Fatalf("real media plan: %d %s", w.Code, w.Body)
		}
		var v struct {
			HeartbeatURL string `json:"heartbeat_url"`
			MediaURL     string `json:"media_url"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		w = request("GET", v.MediaURL, token, "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), media) {
			t.Fatalf("real media stream: status %d, bytes %d", w.Code, w.Body.Len())
		}
		return v.HeartbeatURL
	}
	onePlayback, twoPlayback := plan(oneToken), plan(twoToken)
	// Ended at a position below 90% proves the ended HTTP path, not just threshold completion.
	playable, err := library.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	position := playable.DurationMS / 4
	if position <= 0 {
		t.Fatalf("missing real duration: %d", playable.DurationMS)
	}
	for _, entry := range []struct {
		url, token string
		ended      bool
	}{{onePlayback, oneToken, true}, {twoPlayback, twoToken, false}} {
		w := request("POST", entry.url, entry.token, fmt.Sprintf(`{"position_ms":%d,"observation":1,"ended":%t}`, position, entry.ended))
		if w.Code != 200 {
			t.Fatalf("real heartbeat: %d %s", w.Code, w.Body)
		}
	}
	// Expiry and shutdown must not reinterpret cached positions as new viewing.
	manager.Sweep(time.Now().Add(time.Hour))
	plan(oneToken) // A new, unobserved replay must not clear completion at shutdown.
	if err = manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var completed int
	if err = db.QueryRow(`SELECT completed FROM progress WHERE profile_id=? AND catalog_id=?`, one.ID, items[0].ID).Scan(&completed); err != nil || completed != 1 {
		t.Fatalf("ended completion=%d %v", completed, err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err = household.Open(db)
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
	if position, err := h.Position(oneSession, items[0].ID); err != nil || position != 0 {
		t.Fatalf("completed resume = %d, %v", position, err)
	}
	if got, err := h.Position(twoSession, items[0].ID); err != nil || got != position {
		t.Fatalf("other profile resume = %d, want %d: %v", got, position, err)
	}
}
