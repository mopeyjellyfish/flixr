package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/screens"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScreenWebSocketRequiresProfileAndSameOrigin(t *testing.T) {
	house := newHousehold(t)
	manager := screens.New(time.Minute)
	server := httptest.NewServer(web.NewServerWithScreens(house, catalog.New(), playback.NewDirectManager(), manager).Handler())
	defer server.Close()

	ctx := t.Context()
	_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/receiver?ticket=missing", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}}})
	require.Error(t, err)
	assert.Equal(t, http.StatusForbidden, response.StatusCode)

	client, profileCookie := screenProfileClient(t, server.URL, house)
	advertise := postJSON(t, client, server.URL+"/api/v1/screens/presence", `{"name":"Living room"}`)
	require.Equal(t, http.StatusCreated, advertise.StatusCode)
	var presence struct {
		Screen screens.Screen `json:"screen"`
		Ticket string         `json:"ticket"`
	}
	require.NoError(t, json.NewDecoder(advertise.Body).Decode(&presence))

	_, response, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/receiver?ticket="+presence.Ticket, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{profileCookie}, "Origin": []string{"http://attacker.invalid"}}})
	require.Error(t, err)
	assert.Equal(t, http.StatusForbidden, response.StatusCode)

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/receiver?ticket="+presence.Ticket, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{profileCookie}, "Origin": []string{server.URL}}})
	require.NoError(t, err)
	defer conn.CloseNow()

	listed, err := client.Get(server.URL + "/api/v1/screens")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, listed.StatusCode)

	authorize := postJSON(t, client, server.URL+"/api/v1/screens/"+presence.Screen.ID+"/sessions", `{}`)
	require.Equal(t, http.StatusCreated, authorize.StatusCode)
	var session screens.Session
	require.NoError(t, json.NewDecoder(authorize.Body).Decode(&session))
	control, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/control?token="+session.Token, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{profileCookie}, "Origin": []string{server.URL}}})
	require.NoError(t, err)
	defer control.CloseNow()
	require.NoError(t, control.Write(ctx, websocket.MessageText, []byte(`{"version":1,"type":"play","catalog_id":"film-1","position_ms":42}`)))
	_, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":1,"type":"play","catalog_id":"film-1","position_ms":42}`, string(payload))
	// An idle controller and receiver must not survive manager shutdown.
	manager.Shutdown()
	deadline, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, _, err = control.Read(deadline)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	_, _, err = conn.Read(deadline)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
}

func TestScreenControlRejectsPlayForContentOutsideProfilePolicy(t *testing.T) {
	house := newHousehold(t)
	manager := screens.New(time.Minute)
	server := httptest.NewServer(web.NewServerWithScreens(house, catalog.New(), playback.NewDirectManager(), manager).Handler())
	defer server.Close()

	client, profileCookie := screenProfileClientWithPolicy(t, server.URL, house, `{"library_ids":["kids"],"unrated_policy":"allow","allow_tags":[],"deny_tags":[]}`)
	advertise := postJSON(t, client, server.URL+"/api/v1/screens/presence", `{"name":"Living room"}`)
	require.Equal(t, http.StatusCreated, advertise.StatusCode)
	var presence struct {
		Screen screens.Screen `json:"screen"`
		Ticket string         `json:"ticket"`
	}
	require.NoError(t, json.NewDecoder(advertise.Body).Decode(&presence))
	ctx := t.Context()
	receiver, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/receiver?ticket="+presence.Ticket, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{profileCookie}, "Origin": []string{server.URL}}})
	require.NoError(t, err)
	defer receiver.CloseNow()
	authorized := postJSON(t, client, server.URL+"/api/v1/screens/"+presence.Screen.ID+"/sessions", `{}`)
	require.Equal(t, http.StatusCreated, authorized.StatusCode)
	var session screens.Session
	require.NoError(t, json.NewDecoder(authorized.Body).Decode(&session))
	control, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/screens/control?token="+session.Token, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{profileCookie}, "Origin": []string{server.URL}}})
	require.NoError(t, err)
	defer control.CloseNow()
	require.NoError(t, control.Write(ctx, websocket.MessageText, []byte(`{"version":1,"type":"play","catalog_id":"adult-film","position_ms":42}`)))
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _, err = control.Read(deadline)
	require.Error(t, err)
	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(err))
}

func screenProfileClient(t *testing.T, base string, house interface{ SetupToken() string }) (*http.Client, string) {
	return screenProfileClientWithPolicy(t, base, house, "")
}

func screenProfileClientWithPolicy(t *testing.T, base string, house interface{ SetupToken() string }, policy string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	claim := postJSON(t, client, base+"/api/v1/setup/claim", `{"token":"`+house.SetupToken()+`","password":"password123"}`)
	require.Equal(t, http.StatusCreated, claim.StatusCode)
	created := postJSON(t, client, base+"/api/v1/profiles", `{"name":"Viewer","pin":""}`)
	var profile struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&profile))
	if policy != "" {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, base+"/api/v1/owner/profiles/"+profile.ID+"/access-policy", bytes.NewBufferString(policy))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		updated, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, updated.StatusCode)
		updated.Body.Close()
	}
	selected := postJSON(t, client, base+"/api/v1/profiles/"+profile.ID+"/select", `{"pin":""}`)
	require.Equal(t, http.StatusOK, selected.StatusCode)
	cookies := client.Jar.Cookies(selected.Request.URL)
	require.NotEmpty(t, cookies)
	return client, cookies[0].String()
}

func postJSON(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	require.NoError(t, err)
	return response
}
