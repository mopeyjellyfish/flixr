package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/screens"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/stretchr/testify/require"
)

func TestOwnerRevocationRejectsExistingPlaybackAuthority(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "film.mp4"), []byte("0123456789"), 0600))
	_, err = db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,container,video_codec,audio_json) VALUES('film','film','Film','film.mp4','film','mp4','h264','[{"codec":"aac"}]')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO settings(key,value) VALUES('film_root',?)`, root)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	owner, err := house.Claim(house.SetupToken(), "owner password")
	require.NoError(t, err)
	profile, err := house.CreateProfile("Viewer", "")
	require.NoError(t, err)
	viewer, err := house.Select(profile.ID, "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	manager := playback.NewDirectManager()
	handler := web.NewServerWithPlayback(house, library, manager).Handler()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	var inventory struct {
		Sessions []household.Session `json:"sessions"`
	}
	w := request("GET", "/api/v1/owner/sessions", "", owner)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &inventory))
	var sessionID string
	for _, session := range inventory.Sessions {
		if session.Subject == profile.ID {
			sessionID = session.ID
		}
	}
	require.NotEmpty(t, sessionID)
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/owner/sessions"}, {"DELETE", "/api/v1/owner/sessions/" + sessionID}, {"DELETE", "/api/v1/profiles/" + profile.ID}} {
		require.Equal(t, http.StatusForbidden, request(route.method, route.path, "", viewer).Code)
	}
	w = request("POST", "/api/v1/playback/plans", `{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`, viewer)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var plan struct {
		MediaURL  string `json:"media_url"`
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plan))
	require.Equal(t, http.StatusOK, request("GET", plan.MediaURL, "", viewer).Code)
	viewerIdentity, ok := house.SessionIdentity(viewer)
	require.True(t, ok)
	otherViewer, err := house.Select(profile.ID, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, request("GET", plan.MediaURL, "", otherViewer).Code)
	require.Equal(t, http.StatusOK, request("DELETE", "/api/v1/owner/sessions/"+sessionID, "", owner).Code)
	require.Equal(t, http.StatusForbidden, request("GET", plan.MediaURL, "", viewer).Code)
	require.Equal(t, http.StatusForbidden, request("POST", "/api/v1/playback/sessions/"+plan.SessionID+"/heartbeat", `{"position_ms":1}`, viewer).Code)
	_, ok = manager.LookupForViewer(plan.SessionID, viewerIdentity, profile.ID, false)
	require.False(t, ok, "revoking viewer authorization must release its playback session")

	planFor := func(token string) (string, string) {
		response := request("POST", "/api/v1/playback/plans", `{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`, token)
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		var created struct {
			SessionID string `json:"session_id"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
		identity, valid := house.SessionIdentity(token)
		require.True(t, valid)
		return created.SessionID, identity
	}
	pinViewer, err := house.Select(profile.ID, "")
	require.NoError(t, err)
	pinPlayback, pinIdentity := planFor(pinViewer)
	w = request("PATCH", "/api/v1/profiles/"+profile.ID, `{"pin":"1234"}`, owner)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, ok = manager.LookupForViewer(pinPlayback, pinIdentity, profile.ID, false)
	require.False(t, ok, "PIN change retained revoked playback")

	deletedViewer, err := house.Select(profile.ID, "1234")
	require.NoError(t, err)
	deletedPlayback, deletedIdentity := planFor(deletedViewer)
	_, err = db.Exec("CREATE TRIGGER fail_profile_delete BEFORE DELETE ON profiles BEGIN SELECT RAISE(ABORT, 'interrupted'); END")
	require.NoError(t, err)
	w = request("DELETE", "/api/v1/profiles/"+profile.ID, "", owner)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	_, ok = manager.LookupForViewer(deletedPlayback, deletedIdentity, profile.ID, false)
	require.True(t, ok, "failed profile deletion stopped valid playback")
	_, err = db.Exec("DROP TRIGGER fail_profile_delete")
	require.NoError(t, err)
	w = request("DELETE", "/api/v1/profiles/"+profile.ID, "", owner)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, ok = manager.LookupForViewer(deletedPlayback, deletedIdentity, profile.ID, false)
	require.False(t, ok, "profile deletion retained revoked playback")
}

func TestProfileChangeAndLogoutReleaseOnlyThePriorViewerPlayback(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "film.mp4"), []byte("0123456789"), 0600))
	_, err = db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,container,video_codec,audio_json) VALUES('film','film','Film','film.mp4','film','mp4','h264','[{"codec":"aac"}]')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO settings(key,value) VALUES('film_root',?)`, root)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	one, err := house.CreateProfile("One", "")
	require.NoError(t, err)
	two, err := house.CreateProfile("Two", "")
	require.NoError(t, err)
	priorViewer, err := house.Select(one.ID, "")
	require.NoError(t, err)
	otherDevice, err := house.Select(one.ID, "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	manager := playback.NewDirectManager()
	handler := web.NewServerWithPlayback(house, library, manager).Handler()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	plan := func(token string) string {
		w := request("POST", "/api/v1/playback/plans", `{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`, token)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		var response struct {
			SessionID string `json:"session_id"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		return response.SessionID
	}
	priorPlayback := plan(priorViewer)
	otherPlayback := plan(otherDevice)
	priorIdentity, ok := house.SessionIdentity(priorViewer)
	require.True(t, ok)
	otherIdentity, ok := house.SessionIdentity(otherDevice)
	require.True(t, ok)

	w := request("POST", "/api/v1/profiles/"+two.ID+"/select", `{}`, priorViewer)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, ok = manager.LookupForViewer(priorPlayback, priorIdentity, one.ID, false)
	require.False(t, ok, "profile change retained the prior viewer's playback")
	_, ok = manager.LookupForViewer(otherPlayback, otherIdentity, one.ID, false)
	require.True(t, ok, "profile change interrupted another device")
	result := w.Result()
	cookies := result.Cookies()
	require.Len(t, cookies, 1)
	newViewer := cookies[0].Value
	newPlayback := plan(newViewer)
	newIdentity, ok := house.SessionIdentity(newViewer)
	require.True(t, ok)

	w = request("POST", "/api/v1/logout", ``, newViewer)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, ok = manager.LookupForViewer(newPlayback, newIdentity, two.ID, false)
	require.False(t, ok, "logout retained the viewer's playback")
	_, ok = manager.LookupForViewer(otherPlayback, otherIdentity, one.ID, false)
	require.True(t, ok, "logout interrupted another device")
}

func TestRevokedReceiverCannotReceiveScreenCommands(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Viewer", "")
	require.NoError(t, err)
	viewer, err := house.Select(profile.ID, "")
	require.NoError(t, err)
	manager := screens.New(time.Minute)
	defer manager.Shutdown()
	server := httptest.NewServer(web.NewServerWithScreens(house, catalog.New(), playback.NewDirectManager(), manager).Handler())
	defer server.Close()
	screen, ticket, err := manager.Advertise(profile.ID, "TV", time.Now())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/receiver?ticket="+ticket, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {"flixr_session=" + viewer}, "Origin": {server.URL}}})
	require.NoError(t, err)
	defer conn.CloseNow()
	authority, err := manager.Authorize(screen.ID, profile.ID, time.Now())
	require.NoError(t, err)
	sessions, err := house.ActiveSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NoError(t, house.RevokeSession(sessions[0].ID))
	_, err = manager.Control(authority.Token, profile.ID, screens.Command{Type: "play", CatalogID: "film"}, time.Now())
	require.NoError(t, err)
	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
}
