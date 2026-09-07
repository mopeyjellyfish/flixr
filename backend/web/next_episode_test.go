package web_test

import (
	"bytes"
	"context"
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

func TestPlaybackNextUsesTheAuthorizedSessionProfileAndCatalogContext(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tv := t.TempDir()
	for index, name := range []string{"Signal/Season 01/Signal S01E01.mp4", "Signal/Season 01/Signal S01E02.mp4"} {
		path := filepath.Join(tv, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte{byte(index + 1)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{Container: "mp4", VideoCodec: "h264", Audio: []catalog.AudioTrack{{Codec: "aac"}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ids := map[int]string{}
	rows, err := db.Query("SELECT id,relative_path FROM catalog_items")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			t.Fatal(err)
		}
		var episode int
		if filepath.Base(path) == "Signal S01E01.mp4" {
			episode = 1
		} else {
			episode = 2
		}
		ids[episode] = id
	}
	rows.Close()

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
	handler := web.NewServer(h, c).Handler()
	request := func(token, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	plan := request(oneToken, http.MethodPost, "/api/v1/playback/plans", `{"catalog_id":"`+ids[1]+`","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`)
	if plan.Code != http.StatusCreated {
		t.Fatalf("plan = %d: %s", plan.Code, plan.Body)
	}
	var playback struct {
		SessionID    string `json:"session_id"`
		HeartbeatURL string `json:"heartbeat_url"`
	}
	if err := json.Unmarshal(plan.Body.Bytes(), &playback); err != nil {
		t.Fatal(err)
	}
	ended := request(oneToken, http.MethodPost, playback.HeartbeatURL, `{"position_ms":1000,"observation":1,"ended":true}`)
	if ended.Code != http.StatusOK {
		t.Fatalf("ended = %d: %s", ended.Code, ended.Body)
	}

	path := "/api/v1/playback/sessions/" + playback.SessionID + "/next"
	wrongProfile := request(twoToken, http.MethodGet, path, "")
	if wrongProfile.Code != http.StatusForbidden {
		t.Fatalf("cross-profile next = %d: %s", wrongProfile.Code, wrongProfile.Body)
	}
	next := request(oneToken, http.MethodGet, path, "")
	if next.Code != http.StatusOK {
		t.Fatalf("next = %d: %s", next.Code, next.Body)
	}
	var result catalog.EpisodeSequence
	if err := json.Unmarshal(next.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != catalog.EpisodeSequenceNext || result.Episode == nil || result.Episode.ID != ids[2] {
		t.Fatalf("next response = %#v", result)
	}
	withSpecials := request(oneToken, http.MethodGet, path+"?include_specials=true", "")
	if withSpecials.Code != http.StatusOK {
		t.Fatalf("specials opt-in = %d: %s", withSpecials.Code, withSpecials.Body)
	}
	invalidSpecials := request(oneToken, http.MethodGet, path+"?include_specials=sometimes", "")
	if invalidSpecials.Code != http.StatusBadRequest {
		t.Fatalf("invalid specials opt-in = %d: %s", invalidSpecials.Code, invalidSpecials.Body)
	}
	stopped := request(oneToken, http.MethodPost, "/api/v1/playback/sessions/"+playback.SessionID+"/stop", "")
	if stopped.Code != http.StatusOK {
		t.Fatalf("stop = %d: %s", stopped.Code, stopped.Body)
	}
	if afterStop := request(oneToken, http.MethodGet, path, ""); afterStop.Code != http.StatusForbidden {
		t.Fatalf("next after stop = %d: %s", afterStop.Code, afterStop.Body)
	}
}
