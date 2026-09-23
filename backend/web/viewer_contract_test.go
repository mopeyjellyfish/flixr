package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type viewerResponseItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Listed bool   `json:"listed"`
}

type viewerResponse struct {
	Preference catalog.ViewPreference `json:"preference"`
	Items      []viewerResponseItem   `json:"items"`
	Sections   []struct {
		Name       string               `json:"name"`
		Items      []viewerResponseItem `json:"items"`
		NextCursor string               `json:"next_cursor"`
	} `json:"sections"`
	NextCursor string `json:"next_cursor"`
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

func TestViewerFirstPageIsBoundedAndAuthorizedPagesAreStable(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	for _, item := range []struct{ id, title string }{
		{"a1", "Alpha"}, {"a2", "Alpha"}, {"b", "Beta"}, {"c", "Charlie"}, {"d", "Delta"},
	} {
		_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at,genres_json,added_at) VALUES(?, 'film', ?, ?, 1, 'film', 0, '["Drama"]', 1)`, item.id, item.title, item.id+".mp4")
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film','a2','tags','denied','local',1)`)
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Child", "")
	require.NoError(t, err)
	_, _, err = house.UpdatePolicy(profile.ID, access.Policy{DenyTags: []string{"denied"}})
	require.NoError(t, err)
	_, err = library.SavePreference(profile.ID, "film", catalog.ViewPreference{View: "grid", Sort: "title"})
	require.NoError(t, err)
	session, err := house.Select(profile.ID, "")
	require.NoError(t, err)
	handler := web.NewServer(house, library).Handler()
	get := func(target string) (*httptest.ResponseRecorder, viewerResponse) {
		r := httptest.NewRequest(http.MethodGet, "http://flixr.test"+target, nil)
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var response viewerResponse
		if w.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		}
		return w, response
	}

	firstRecorder, first := get("/api/v1/catalog/view?media=film&limit=2")
	require.Equal(t, http.StatusOK, firstRecorder.Code, firstRecorder.Body.String())
	require.Equal(t, []string{"a1", "b"}, viewerIDs(first))
	require.NotEmpty(t, first.NextCursor)
	secondRecorder, second := get("/api/v1/catalog/view?media=film&limit=2&cursor=" + url.QueryEscape(first.NextCursor))
	require.Equal(t, http.StatusOK, secondRecorder.Code, secondRecorder.Body.String())
	assert.Equal(t, []string{"c", "d"}, viewerIDs(second))
	assert.Empty(t, second.NextCursor)

	_, err = library.SavePreference(profile.ID, "film", catalog.ViewPreference{View: "rows", Sort: "title"})
	require.NoError(t, err)
	preferenceChanged, _ := get("/api/v1/catalog/view?media=film&limit=2&cursor=" + url.QueryEscape(first.NextCursor))
	assert.Equal(t, http.StatusBadRequest, preferenceChanged.Code)
	sectionRecorder, sectionPage := get("/api/v1/catalog/view?media=film&section=New&limit=2")
	require.Equal(t, http.StatusOK, sectionRecorder.Code, sectionRecorder.Body.String())
	require.Len(t, sectionPage.Sections, 1)
	assert.Equal(t, []string{"a1", "b"}, responseItemIDs(sectionPage.Sections[0].Items))
	require.NotEmpty(t, sectionPage.NextCursor)
	require.NoError(t, library.SetListed(profile.ID, "film", "a1", true))
	stateRecorder, _ := get("/api/v1/catalog/view?media=all&state_kind=film&state_id=a1")
	require.Equal(t, http.StatusOK, stateRecorder.Code, stateRecorder.Body.String())
	assert.JSONEq(t, `{"state":{"listed":true,"continue_watching_dismissed":false}}`, stateRecorder.Body.String())
	_, _, err = house.UpdatePolicy(profile.ID, access.Policy{DenyTags: []string{"other"}})
	session, err = house.Select(profile.ID, "")
	require.NoError(t, err)
	policyChanged, _ := get("/api/v1/catalog/view?media=film&section=New&limit=2&cursor=" + url.QueryEscape(sectionPage.NextCursor))
	assert.Equal(t, http.StatusBadRequest, policyChanged.Code)

	malformed, _ := get("/api/v1/catalog/view?media=film&limit=2&cursor=not-a-cursor")
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
}

func TestViewerStateDoesNotRevealMediaVersionMembers(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	for _, id := range []string{"film", "film-member"} {
		_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at) VALUES(?,'film',?,?,1,'film',0)`, id, id, id+".mp4")
		require.NoError(t, err)
	}
	for _, id := range []string{"series", "series-member"} {
		_, err := db.Exec(`INSERT INTO catalog_series(id,title,local_only,updated_at) VALUES(?,?,1,0)`, id, id)
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO catalog_film_version_memberships(canonical_catalog_id,member_catalog_id,created_at) VALUES('film','film-member',1)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO catalog_series_version_memberships(canonical_series_id,member_series_id,created_at) VALUES('series','series-member',1)`)
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Viewer", "")
	require.NoError(t, err)
	session, err := house.Select(profile.ID, "")
	require.NoError(t, err)
	handler := web.NewServer(house, library).Handler()
	for _, tc := range []struct{ kind, member, canonical string }{{"film", "film-member", "film"}, {"series", "series-member", "series"}} {
		for id, status := range map[string]int{tc.member: http.StatusNotFound, tc.canonical: http.StatusOK} {
			r := httptest.NewRequest(http.MethodGet, "http://flixr.test/api/v1/catalog/view?media=all&state_kind="+tc.kind+"&state_id="+id, nil)
			r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, status, w.Code, "%s/%s: %s", tc.kind, id, w.Body.String())
		}
	}
}

func viewerIDs(view viewerResponse) []string { return responseItemIDs(view.Items) }

func responseItemIDs(items []viewerResponseItem) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}
