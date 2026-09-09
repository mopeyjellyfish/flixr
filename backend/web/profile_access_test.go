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

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type profileAccessFixture struct {
	handler http.Handler
	house   *household.Manager
	profile household.Profile
	owner   string
	viewer  string
	allowed string
	denied  string
}

func newProfileAccessFixture(t *testing.T) profileAccessFixture {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Allowed.mp4"), []byte("allowed"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Denied.mp4"), []byte("denied"), 0o600))
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{Container: "mp4", VideoCodec: "h264"}, nil
	}))
	require.NoError(t, err)
	require.NoError(t, c.SetRoots(root, ""))
	require.NoError(t, c.Scan(context.Background(), 2))
	items, _, err := c.Browse("", 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	var allowedID, deniedID string
	for _, item := range items {
		if item.Title == "Allowed" {
			allowedID = item.ID
		} else {
			deniedID = item.ID
		}
	}
	_, err = c.EditMetadata("film", allowedID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "tags", Value: "family", Source: "local", Locked: true}, {Field: "content_rating", Value: "PG", Source: "local", Locked: true}}})
	require.NoError(t, err)
	_, err = c.EditMetadata("film", deniedID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "tags", Value: "family,scary", Source: "local", Locked: true}, {Field: "content_rating", Value: "18", Source: "local", Locked: true}}})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE catalog_items SET genres_json=CASE id WHEN ? THEN '["Family"]' WHEN ? THEN '["Horror"]' ELSE genres_json END`, allowedID, deniedID)
	require.NoError(t, err)
	h, err := household.Open(db)
	require.NoError(t, err)
	owner, err := h.Claim(h.SetupToken(), "password")
	require.NoError(t, err)
	profile, err := h.CreateProfile("Child", "")
	require.NoError(t, err)
	_, _, err = h.UpdatePolicy(profile.ID, access.Policy{RatingRegion: "GB", MaxRating: "12", Unrated: access.UnratedDeny, AllowTags: []string{"family"}, DenyTags: []string{"scary"}})
	require.NoError(t, err)
	viewer, err := h.Select(profile.ID, "")
	require.NoError(t, err)
	return profileAccessFixture{handler: web.NewServer(h, c).Handler(), house: h, profile: profile, owner: owner, viewer: viewer, allowed: allowedID, denied: deniedID}
}

func (f profileAccessFixture) request(t *testing.T, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://flixr.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://flixr.test")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestRestrictedCatalogOmitsDeniedTitlesFromBrowseSearchCountsAndDirectURLs(t *testing.T) {
	f := newProfileAccessFixture(t)
	for _, path := range []string{"/api/v1/catalog/home", "/api/v1/catalog/search?q=Denied", "/api/v1/catalog/view?media=all"} {
		response := f.request(t, http.MethodGet, path, "", f.viewer)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "Denied")
		assert.NotContains(t, response.Body.String(), f.denied)
		assert.NotContains(t, response.Body.String(), "Horror")
		if strings.Contains(path, "/view") {
			assert.Contains(t, response.Body.String(), "Family")
		}
		if strings.Contains(path, "search") {
			assert.Contains(t, response.Body.String(), `"total":0`)
		}
	}
	for _, path := range []string{"/api/v1/catalog/films/" + f.denied, "/api/v1/catalog/items/" + f.denied, "/api/v1/catalog/artwork/" + f.denied + "/poster"} {
		response := f.request(t, http.MethodGet, path, "", f.viewer)
		assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "Denied")
	}
	allowed := f.request(t, http.MethodGet, "/api/v1/catalog/films/"+f.allowed, "", f.viewer)
	assert.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
}

func TestRestrictedCatalogRejectsAllTitleActionsAndPlaybackWithoutMetadataLeak(t *testing.T) {
	f := newProfileAccessFixture(t)
	tests := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/progress/" + f.denied, ""},
		{http.MethodPut, "/api/v1/catalog/list/film/" + f.denied, ""},
		{http.MethodPut, "/api/v1/catalog/watched/film/" + f.denied, `{"watched":true}`},
		{http.MethodPost, "/api/v1/playback/plans", `{"catalog_id":"` + f.denied + `","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":[],"supports_direct":true}}`},
	}
	for _, tt := range tests {
		response := f.request(t, tt.method, tt.path, tt.body, f.viewer)
		assert.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, response.Code, "%s %s: %s", tt.method, tt.path, response.Body.String())
		assert.NotContains(t, response.Body.String(), "Denied")
	}
	playback := f.request(t, http.MethodPost, "/api/v1/playback/plans", tests[len(tests)-1].body, f.viewer)
	assert.Contains(t, playback.Body.String(), `"content_access_denied"`)
}

func TestOwnerPolicyAPIRevokesExistingPlaybackAndProfileSessions(t *testing.T) {
	f := newProfileAccessFixture(t)
	plan := f.request(t, http.MethodPost, "/api/v1/playback/plans", `{"catalog_id":"`+f.allowed+`","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":[],"supports_direct":true}}`, f.viewer)
	require.Equal(t, http.StatusCreated, plan.Code, plan.Body.String())
	var created struct {
		MediaURL string `json:"media_url"`
	}
	require.NoError(t, json.Unmarshal(plan.Body.Bytes(), &created))

	updated := f.request(t, http.MethodPut, "/api/v1/owner/profiles/"+f.profile.ID+"/access-policy", `{"library_ids":[],"rating_region":"GB","max_rating":"U","unrated_policy":"deny","allow_tags":[],"deny_tags":[]}`, f.owner)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Contains(t, updated.Body.String(), `"version":3`)
	stale := f.request(t, http.MethodGet, created.MediaURL, "", f.viewer)
	assert.Equal(t, http.StatusForbidden, stale.Code, stale.Body.String())
	for _, path := range []string{"/api/v1/catalog/artwork/" + f.allowed + "/poster", strings.TrimSuffix(created.MediaURL, "/media") + "/preview.jpg?position_ms=0"} {
		r := httptest.NewRequest(http.MethodGet, "http://flixr.test"+path, nil)
		r.Header.Set("If-None-Match", `"previous"`)
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: f.viewer})
		conditional := httptest.NewRecorder()
		f.handler.ServeHTTP(conditional, r)
		assert.Equal(t, http.StatusForbidden, conditional.Code, conditional.Body.String())
		assert.Equal(t, "private, no-store", conditional.Header().Get("Cache-Control"))
	}
}

func TestPolicyEndpointRejectsInvalidRegionWithoutRevokingSession(t *testing.T) {
	f := newProfileAccessFixture(t)
	response := f.request(t, http.MethodPut, "/api/v1/owner/profiles/"+f.profile.ID+"/access-policy", `{"rating_region":"CA","max_rating":"PG","unrated_policy":"deny"}`, f.owner)
	assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	_, active := f.house.Profile(f.viewer)
	assert.True(t, active)
}

func TestPolicyResponseDoesNotExposeHiddenTitleMetadata(t *testing.T) {
	f := newProfileAccessFixture(t)
	response := f.request(t, http.MethodPost, "/api/v1/playback/plans", `{"catalog_id":"`+f.denied+`","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":[],"supports_direct":true}}`, f.viewer)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"content_access_denied"`)))
	assert.NotContains(t, response.Body.String(), f.denied)
}

func TestRestrictedHistoryRatingsAndImportsCannotRevealOrMutateDeniedTitles(t *testing.T) {
	f := newProfileAccessFixture(t)
	require.NoError(t, f.house.RecordViewingEvent(f.profile.ID, household.ViewingEvent{CatalogID: f.denied, Title: "Denied", Kind: "film", Type: household.EventCompleted, Provenance: household.ProvenanceLocal}))
	require.NoError(t, f.house.RecordViewingEvent(f.profile.ID, household.ViewingEvent{CatalogID: f.allowed, Title: "Allowed", Kind: "film", Type: household.EventCompleted, Provenance: household.ProvenanceLocal}))
	history := f.request(t, http.MethodGet, "/api/v1/history", "", f.viewer)
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	assert.Contains(t, history.Body.String(), "Allowed")
	assert.NotContains(t, history.Body.String(), "Denied")

	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/ratings/" + f.denied, ""},
		{http.MethodPut, "/api/v1/ratings/" + f.denied, `{"value":5}`},
		{http.MethodPost, "/api/v1/history/import", `{"events":[{"catalog_id":"` + f.denied + `","title":"Denied","kind":"film","type":"completed"}]}`},
	} {
		response := f.request(t, request.method, request.path, request.body, f.viewer)
		assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "Denied")
	}
}

func TestSeriesAndNextUpIncludeOnlyEpisodesFromAllowedLibraries(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	allowedRoot, deniedRoot := t.TempDir(), t.TempDir()
	writeEpisode := func(root, name string, value byte) {
		path := filepath.Join(root, "Signal", "Season 01", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte{value}, 0o600))
	}
	writeEpisode(allowedRoot, "Signal S01E01.mp4", 1)
	writeEpisode(deniedRoot, "Signal S01E02.mp4", 2)
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{Container: "mp4", VideoCodec: "h264"}, nil
	}))
	require.NoError(t, err)
	require.NoError(t, c.SetRoots("", allowedRoot))
	other, err := c.CreateLibrary("Other TV", "episode")
	require.NoError(t, err)
	_, err = c.AddLibraryLocation(other.ID, deniedRoot)
	require.NoError(t, err)
	require.NoError(t, c.Scan(context.Background(), 2))
	seriesItems, _, err := c.Browse("", 0, 10)
	require.NoError(t, err)
	require.Len(t, seriesItems, 1)
	series, ok := c.Series(seriesItems[0].ID)
	require.True(t, ok)
	require.Len(t, series.Seasons, 1)
	require.Len(t, series.Seasons[0].Episodes, 2)
	firstID := series.Seasons[0].Episodes[0].ID
	secondID := series.Seasons[0].Episodes[1].ID

	h, err := household.Open(db)
	require.NoError(t, err)
	p, err := h.CreateProfile("Child", "")
	require.NoError(t, err)
	_, _, err = h.UpdatePolicy(p.ID, access.Policy{LibraryIDs: []string{"tv"}})
	require.NoError(t, err)
	token, err := h.Select(p.ID, "")
	require.NoError(t, err)
	f := profileAccessFixture{handler: web.NewServer(h, c).Handler(), house: h, profile: p, viewer: token}

	detail := f.request(t, http.MethodGet, "/api/v1/catalog/series/"+series.ID, "", token)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	assert.Contains(t, detail.Body.String(), firstID)
	assert.NotContains(t, detail.Body.String(), secondID)
	plan := f.request(t, http.MethodPost, "/api/v1/playback/plans", `{"catalog_id":"`+firstID+`","capabilities":{"containers":["mp4"],"video_codecs":["h264"],"audio_codecs":[],"supports_direct":true}}`, token)
	require.Equal(t, http.StatusCreated, plan.Code, plan.Body.String())
	var playback struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(plan.Body.Bytes(), &playback))
	next := f.request(t, http.MethodGet, "/api/v1/playback/sessions/"+playback.SessionID+"/next", "", token)
	require.Equal(t, http.StatusOK, next.Code, next.Body.String())
	assert.NotContains(t, next.Body.String(), secondID)
	assert.Contains(t, next.Body.String(), `"state":"end_of_series"`)
}
