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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinueWatchingRoutesAreProfileBoundAndRestoreOnlyForNewViewing(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,container,video_codec,audio_json,subtitle_json,duration_ms) VALUES('film','film','Film','film.mp4','film','mp4','h264','[{"codec":"aac"}]','[]',1000)`)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	one, err := house.CreateProfile("One", "")
	require.NoError(t, err)
	two, err := house.CreateProfile("Two", "")
	require.NoError(t, err)
	oneToken, err := house.Select(one.ID, "")
	require.NoError(t, err)
	twoToken, err := house.Select(two.ID, "")
	require.NoError(t, err)
	require.NoError(t, house.RecordProgress(one.ID, "film", 500, 1, false))
	require.NoError(t, house.RecordProgress(two.ID, "film", 500, 1, false))
	library, err := catalog.Open(db)
	require.NoError(t, err)
	handler := web.NewServer(house, library).Handler()
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://flixr.test"+path, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://flixr.test")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	continueWatching := func(token string) []string {
		t.Helper()
		w := request(http.MethodGet, "/api/v1/catalog/view?media=all", token, "")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response struct {
			Sections []struct {
				Items []catalog.ViewerItem `json:"items"`
			} `json:"sections"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
		ids := []string{}
		for _, item := range response.Sections[0].Items {
			ids = append(ids, item.ID)
		}
		return ids
	}

	path := "/api/v1/catalog/continue-watching/film/film"
	assert.Equal(t, http.StatusForbidden, request(http.MethodDelete, path, "", "").Code)
	assert.Equal(t, http.StatusOK, request(http.MethodDelete, path, oneToken, "").Code)
	assert.Empty(t, continueWatching(oneToken))
	assert.Equal(t, []string{"film"}, continueWatching(twoToken))
	assert.Equal(t, http.StatusNotFound, request(http.MethodDelete, "/api/v1/catalog/continue-watching/film/missing", oneToken, "").Code)

	planBody := func(intent string) string {
		return `{"catalog_id":"film","continue_watching_intent":"` + intent + `","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"],"supports_direct":true}}`
	}
	assert.Equal(t, http.StatusBadRequest, request(http.MethodPost, "/api/v1/playback/plans", oneToken, planBody("background")).Code)
	assert.Empty(t, continueWatching(oneToken))
	unspecified := request(http.MethodPost, "/api/v1/playback/plans", oneToken, `{"catalog_id":"film","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"],"supports_direct":true}}`)
	require.Equal(t, http.StatusCreated, unspecified.Code, unspecified.Body.String())
	assert.Empty(t, continueWatching(oneToken))
	for _, intent := range []string{"recovery", "automatic"} {
		plan := request(http.MethodPost, "/api/v1/playback/plans", oneToken, planBody(intent))
		require.Equal(t, http.StatusCreated, plan.Code, plan.Body.String())
		assert.Empty(t, continueWatching(oneToken))
	}
	viewing := request(http.MethodPost, "/api/v1/playback/plans", oneToken, planBody("user"))
	require.Equal(t, http.StatusCreated, viewing.Code, viewing.Body.String())
	assert.Equal(t, []string{"film"}, continueWatching(oneToken))

	assert.Equal(t, http.StatusOK, request(http.MethodDelete, path, oneToken, "").Code)
	assert.Equal(t, http.StatusOK, request(http.MethodPut, path, oneToken, "").Code)
	assert.Equal(t, []string{"film"}, continueWatching(oneToken))
}
