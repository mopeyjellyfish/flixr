package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestPlaybackObservationsUseServerGeneration(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,container,video_codec,audio_json,subtitle_json) VALUES('film','film','Film','film.mp4','film','mp4','h264','[{"codec":"aac"}]','[]')`)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.Select(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(h, c).Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	plan := func() string {
		t.Helper()
		w := request("POST", "/api/v1/playback/plans", `{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`)
		if w.Code != 201 {
			t.Fatalf("plan: %d %s", w.Code, w.Body)
		}
		var v struct {
			HeartbeatURL string `json:"heartbeat_url"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.HeartbeatURL
	}
	heartbeat := func(url string, position, observation, clock int64) {
		t.Helper()
		w := request("POST", url, fmt.Sprintf(`{"position_ms":%d,"observation":%d,"observed_at":%d}`, position, observation, clock))
		if w.Code != 200 {
			t.Fatalf("heartbeat: %d %s", w.Code, w.Body)
		}
	}
	state := func(position int64, completed int) {
		t.Helper()
		var gotPosition, updated int64
		var gotCompleted int
		if err := db.QueryRow(`SELECT position_ms,completed,updated_at FROM progress WHERE profile_id=? AND catalog_id='film'`, p.ID).Scan(&gotPosition, &gotCompleted, &updated); err != nil {
			t.Fatal(err)
		}
		if gotPosition != position || gotCompleted != completed {
			t.Fatalf("progress=%d/%d, want %d/%d", gotPosition, gotCompleted, position, completed)
		}
		if updated > time.Now().Add(time.Second).UnixMilli() {
			t.Fatalf("client clock persisted: %d", updated)
		}
	}
	first := plan()
	heartbeat(first, 800, 2, time.Now().Add(24*time.Hour).UnixMilli())
	state(800, 0)
	heartbeat(first, 100, 1, time.Now().Add(48*time.Hour).UnixMilli())
	state(800, 0)
	second := plan()
	heartbeat(second, 400, 1, 1)
	state(400, 0)
	heartbeat(first, 900, 1<<62, 1<<62)
	state(400, 0)
	for _, watched := range []bool{true, false} {
		w := request("PUT", "/api/v1/catalog/watched/film/film", fmt.Sprintf(`{"watched":%t}`, watched))
		if w.Code != 200 {
			t.Fatalf("manual: %d %s", w.Code, w.Body)
		}
		heartbeat(second, 999, 1<<62, 1<<62)
		if watched {
			state(1<<62, 1)
		} else {
			state(0, 0)
		}
	}
	third := plan()
	heartbeat(third, 200, 1, 1)
	state(200, 0)
	ended := request("POST", third, `{"position_ms":200,"observation":2,"ended":true}`)
	if ended.Code != 200 {
		t.Fatalf("ended=%d", ended.Code)
	}
	var endedAck struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(ended.Body.Bytes(), &endedAck); err != nil || !endedAck.Accepted {
		t.Fatalf("ended acknowledgement = %s: %v", ended.Body, err)
	}
	stale := request("POST", third, `{"position_ms":100,"observation":1}`)
	var staleAck struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(stale.Body.Bytes(), &staleAck); err != nil || staleAck.Accepted {
		t.Fatalf("stale acknowledgement = %s: %v", stale.Body, err)
	}
	heartbeat(third, 200, 3, 1)
	state(200, 1)
	replay := plan()
	heartbeat(replay, 50, 1, 1)
	state(50, 0)
	w := request("PUT", "/api/v1/progress/film", `{"position_ms":999,"observed_at":9223372036854775807}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("unversioned legacy write=%d, want 409", w.Code)
	}
	state(50, 0)
	read := request("GET", "/api/v1/progress/film", "")
	var legacy struct {
		Generation int64 `json:"generation"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"position_ms":300,"generation":%d}`, legacy.Generation)
	if w := request("PUT", "/api/v1/progress/film", body); w.Code != 200 {
		t.Fatalf("versioned legacy=%d %s", w.Code, w.Body)
	}
	if w := request("PUT", "/api/v1/progress/film", body); w.Code != 409 {
		t.Fatalf("duplicate legacy=%d", w.Code)
	}
	heartbeat(replay, 400, 100, 1)
	state(300, 0)
	if _, err := db.Exec(`UPDATE catalog_items SET duration_ms=1000 WHERE id='film'`); err != nil {
		t.Fatal(err)
	}
	// Reopen the catalog so the handler uses the stored duration.
	c, err = catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	handler = web.NewServer(h, c).Handler()
	threshold := plan()
	heartbeat(threshold, 899, 1, 1)
	state(899, 0)
	heartbeat(threshold, 900, 2, 1)
	state(900, 1)

	closed := playback.NewDirectManager()
	if err := closed.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler = web.NewServerWithPlayback(h, c, closed).Handler()
	before := request("GET", "/api/v1/progress/film", "").Body.String()
	failed := request("POST", "/api/v1/playback/plans", `{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`)
	if failed.Code != http.StatusForbidden {
		t.Fatalf("closed direct manager=%d %s", failed.Code, failed.Body)
	}
	if after := request("GET", "/api/v1/progress/film", "").Body.String(); after != before {
		t.Fatalf("failed direct plan changed progress token: %s -> %s", before, after)
	}

}
