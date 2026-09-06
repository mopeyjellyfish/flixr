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

func TestViewerAPIsPersistProfileRowsListsAndPreferences(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	library, err := catalog.OpenDemo(db)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	one, err := house.CreateProfile("One", "")
	require.NoError(t, err)
	two, err := house.CreateProfile("Two", "")
	require.NoError(t, err)
	oneSession, err := house.Select(one.ID, "")
	require.NoError(t, err)
	twoSession, err := house.Select(two.ID, "")
	require.NoError(t, err)
	handler := web.NewServer(house, library).Handler()

	request := func(method, target, session, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://example.test"+target, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://example.test")
		if session != "" {
			r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, target any) {
		t.Helper()
		require.NoError(t, json.NewDecoder(w.Body).Decode(target))
	}

	assert.Equal(t, http.StatusForbidden, request(http.MethodGet, "/api/v1/catalog/view?media=all", "", "").Code)

	initial := request(http.MethodGet, "/api/v1/catalog/view?media=all", oneSession, "")
	require.Equal(t, http.StatusOK, initial.Code, initial.Body.String())
	var rows struct {
		Preference struct {
			View string `json:"view"`
			Sort string `json:"sort"`
		} `json:"preference"`
		Sections []struct {
			Name  string         `json:"name"`
			Items []catalog.Item `json:"items"`
		} `json:"sections"`
	}
	decode(initial, &rows)
	assert.Equal(t, "rows", rows.Preference.View)
	assert.Equal(t, "title", rows.Preference.Sort)
	require.GreaterOrEqual(t, len(rows.Sections), 4)
	assert.Equal(t, []string{"Continue Watching", "New", "My List"}, []string{rows.Sections[0].Name, rows.Sections[1].Name, rows.Sections[2].Name})
	assert.Empty(t, rows.Sections[0].Items)
	assert.NotEmpty(t, rows.Sections[1].Items)
	assert.Empty(t, rows.Sections[2].Items)
	for _, item := range rows.Sections[3].Items {
		assert.Contains(t, item.Genres, rows.Sections[3].Name)
	}

	filmID := "086ba9c182077a3164558e108b408f45"
	seriesID := "403cb2d07ab4a59992f8a420f0b8250e"
	for _, endpoint := range []string{"/api/v1/catalog/list/film/" + filmID, "/api/v1/catalog/list/film/" + filmID, "/api/v1/catalog/list/series/" + seriesID} {
		response := request(http.MethodPut, endpoint, oneSession, "")
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.JSONEq(t, `{"listed":true}`, response.Body.String())
	}
	assert.Equal(t, http.StatusNotFound, request(http.MethodPut, "/api/v1/catalog/list/film/missing", oneSession, "").Code)

	assert.Equal(t, http.StatusOK, request(http.MethodPut, "/api/v1/progress/"+filmID, oneSession, `{"position_ms":100}`).Code)
	preference := request(http.MethodPut, "/api/v1/catalog/preferences/all", oneSession, `{"view":"grid","sort":"watched"}`)
	require.Equal(t, http.StatusOK, preference.Code, preference.Body.String())
	assert.JSONEq(t, `{"view":"grid","sort":"watched"}`, preference.Body.String())
	assert.Equal(t, http.StatusBadRequest, request(http.MethodPut, "/api/v1/catalog/preferences/all", oneSession, `{"view":"cards","sort":"watched"}`).Code)

	grid := request(http.MethodGet, "/api/v1/catalog/view?media=all", oneSession, "")
	require.Equal(t, http.StatusOK, grid.Code, grid.Body.String())
	var view struct {
		Preference struct {
			View string `json:"view"`
			Sort string `json:"sort"`
		} `json:"preference"`
		Items []catalog.Item `json:"items"`
	}
	decode(grid, &view)
	assert.Equal(t, "grid", view.Preference.View)
	assert.Equal(t, "watched", view.Preference.Sort)
	require.NotEmpty(t, view.Items)
	assert.Equal(t, filmID, view.Items[0].ID)

	films := request(http.MethodPut, "/api/v1/catalog/preferences/film", oneSession, `{"view":"grid","sort":"year"}`)
	require.Equal(t, http.StatusOK, films.Code, films.Body.String())
	filmView := request(http.MethodGet, "/api/v1/catalog/view?media=film", oneSession, "")
	require.Equal(t, http.StatusOK, filmView.Code, filmView.Body.String())
	decode(filmView, &view)
	for _, item := range view.Items {
		assert.Equal(t, "film", item.Kind)
	}

	other := request(http.MethodGet, "/api/v1/catalog/view?media=all", twoSession, "")
	require.Equal(t, http.StatusOK, other.Code, other.Body.String())
	decode(other, &rows)
	assert.Equal(t, "rows", rows.Preference.View)
	assert.Empty(t, rows.Sections[0].Items)
	assert.Empty(t, rows.Sections[2].Items)

	removed := request(http.MethodDelete, "/api/v1/catalog/list/film/"+filmID, oneSession, "")
	assert.Equal(t, http.StatusOK, removed.Code, removed.Body.String())
	assert.JSONEq(t, `{"listed":false}`, removed.Body.String())
	assert.Equal(t, http.StatusOK, request(http.MethodDelete, "/api/v1/catalog/list/film/"+filmID, oneSession, "").Code)

	playback := request(http.MethodPost, "/api/v1/playback/plans", oneSession, `{"catalog_id":"`+filmID+`","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":["aac"]}}`)
	assert.Equal(t, http.StatusConflict, playback.Code)
	assert.JSONEq(t, `{"error":{"code":"playback_not_playable"}}`, playback.Body.String())
}
