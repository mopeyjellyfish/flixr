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

type viewerResponse struct {
	Preference catalog.ViewPreference `json:"preference"`
	Items      []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Listed bool   `json:"listed"`
	} `json:"items"`
	Sections []struct {
		Name string `json:"name"`
	} `json:"sections"`
}

func TestViewerGridContractUsesStableSortsAndProfileListedState(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	for _, item := range []struct {
		id, title, genres string
		year, added       int64
	}{
		{"a1", "Alpha", `["Drama"]`, 2024, 30},
		{"a2", "Alpha", `["Drama"]`, 2024, 20},
		{"b", "Beta", `["Drama"]`, 2024, 30},
		{"z", "Zulu", `["Drama"]`, 2025, 10},
	} {
		_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at,year,genres_json,added_at) VALUES(?, 'film', ?, ?, 1, 'film', 0, ?, ?, ?)`, item.id, item.title, item.id+".mp4", item.year, item.genres, item.added)
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO catalog_series(id,title,local_only,updated_at,genres_json,added_at) VALUES('s1','Series',1,0,'["Drama"]',5)`)
	require.NoError(t, err)
	library, err := catalog.Open(db)
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
		r := httptest.NewRequest(method, "http://flixr.test"+target, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://flixr.test")
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	get := func(session string) viewerResponse {
		t.Helper()
		response := request(http.MethodGet, "/api/v1/catalog/view?media=all", session, "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var view viewerResponse
		require.NoError(t, json.NewDecoder(response.Body).Decode(&view))
		return view
	}
	ids := func(view viewerResponse) []string {
		out := make([]string, len(view.Items))
		for i, item := range view.Items {
			out[i] = item.ID
		}
		return out
	}
	setGrid := func(sort string) viewerResponse {
		t.Helper()
		response := request(http.MethodPut, "/api/v1/catalog/preferences/all", oneSession, `{"view":"grid","sort":"`+sort+`"}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.JSONEq(t, `{"view":"grid","sort":"`+sort+`"}`, response.Body.String())
		return get(oneSession)
	}

	assert.Equal(t, []string{"a1", "a2", "b", "s1", "z"}, ids(setGrid("title")))
	assert.Equal(t, []string{"z", "a1", "a2", "b", "s1"}, ids(setGrid("year")))
	assert.Equal(t, []string{"a1", "b", "a2", "z", "s1"}, ids(setGrid("added")))
	for _, id := range []string{"a1", "a2", "b"} {
		updated := int64(50)
		if id == "b" {
			updated = 70
		}
		_, err := db.Exec(`INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at) VALUES(?,?,1,?)`, one.ID, id, updated)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"b", "a1", "a2", "s1", "z"}, ids(setGrid("watched")))

	for _, sort := range []string{"title", "year", "added", "watched"} {
		response := request(http.MethodPut, "/api/v1/catalog/preferences/series", oneSession, `{"view":"rows","sort":"`+sort+`"}`)
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.JSONEq(t, `{"view":"rows","sort":"`+sort+`"}`, response.Body.String())
		response = request(http.MethodGet, "/api/v1/catalog/preferences/series", oneSession, "")
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.JSONEq(t, `{"view":"rows","sort":"`+sort+`"}`, response.Body.String())
	}
	assert.Equal(t, http.StatusBadRequest, request(http.MethodGet, "/api/v1/catalog/preferences/invalid", oneSession, "").Code)
	assert.Equal(t, http.StatusBadRequest, request(http.MethodGet, "/api/v1/catalog/view?media=invalid", oneSession, "").Code)

	for _, route := range []string{"/api/v1/catalog/list/film/a1", "/api/v1/catalog/list/series/s1"} {
		for range 2 {
			response := request(http.MethodPut, route, oneSession, "")
			assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"listed":true}`, response.Body.String())
		}
	}
	view := setGrid("title")
	listed := map[string]bool{}
	for _, item := range view.Items {
		listed[item.ID] = item.Listed
	}
	assert.True(t, listed["a1"])
	assert.True(t, listed["s1"])
	assert.False(t, listed["a2"])
	for _, item := range get(twoSession).Items {
		assert.False(t, item.Listed)
	}
	for _, route := range []string{"/api/v1/catalog/list/film/a1", "/api/v1/catalog/list/series/s1"} {
		for range 2 {
			response := request(http.MethodDelete, route, oneSession, "")
			assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"listed":false}`, response.Body.String())
		}
	}

	rows := request(http.MethodPut, "/api/v1/catalog/preferences/all", twoSession, `{"view":"rows","sort":"title"}`)
	require.Equal(t, http.StatusOK, rows.Code, rows.Body.String())
	var rowView viewerResponse
	rows = request(http.MethodGet, "/api/v1/catalog/view?media=all", twoSession, "")
	require.NoError(t, json.NewDecoder(rows.Body).Decode(&rowView))
	for _, section := range rowView.Sections {
		assert.NotEqual(t, "Comedy", section.Name)
	}
}
