package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
)

func TestEpisodeOrderRoutesRequireOwner(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(house, catalog.New())
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/owner/episode-orders"},
		{http.MethodGet, "/api/v1/owner/episode-orders/missing"},
		{http.MethodGet, "/api/v1/owner/episode-orders/missing/groups"},
		{http.MethodPut, "/api/v1/owner/episode-orders/missing"},
		{http.MethodPost, "/api/v1/owner/episode-orders/missing/preview"},
	} {
		r := httptest.NewRequest(route.method, route.path, nil)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s status=%d body=%s", route.method, route.path, w.Code, w.Body.String())
		}
	}
}

func TestEpisodeOrderProviderFailureResponses(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{catalog.ErrProviderUnavailable, 503, "metadata_unavailable"},
		{errors.New("provider transport failed"), 503, "metadata_unavailable"},
		{context.DeadlineExceeded, 503, "metadata_unavailable"},
		{catalog.ErrEpisodeOrderInvalid, 400, "episode_order_invalid"},
		{catalog.ErrCatalogNotFound, 404, "catalog_not_found"},
	} {
		w := httptest.NewRecorder()
		new(Server).episodeOrderProviderError(w, test.err)
		if w.Code != test.status || !bytes.Contains(w.Body.Bytes(), []byte(test.code)) {
			t.Errorf("error %v: status=%d body=%s", test.err, w.Code, w.Body.String())
		}
	}
}

func TestEpisodeOrderRoutesRejectCrossOriginAndMalformedOwnerWrites(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(house, catalog.New()).Handler()
	claim := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"fixture-password"}`))
	r.Header.Set("Origin", "http://flixr.test")
	handler.ServeHTTP(claim, r)
	if claim.Code != http.StatusCreated {
		t.Fatalf("claim status=%d", claim.Code)
	}
	cookie := claim.Result().Cookies()[0]
	for _, test := range []struct {
		method, path, body, origin string
		status                     int
	}{
		{http.MethodPut, "/missing", `{"order":"dvd","revision":0,"entries":[]}`, "http://other.test", http.StatusForbidden},
		{http.MethodPost, "/missing/preview", `{"group_id":"dvd"}`, "http://other.test", http.StatusForbidden},
		{http.MethodPut, "/missing", `{`, "http://flixr.test", http.StatusBadRequest},
		{http.MethodPut, "/missing", `{"order":"invented","revision":0,"entries":[]}`, "http://flixr.test", http.StatusBadRequest},
		{http.MethodPost, "/missing/preview", `{"group_id":""}`, "http://flixr.test", http.StatusBadRequest},
		{http.MethodGet, "?limit=51", "", "http://flixr.test", http.StatusBadRequest},
		{http.MethodGet, "?offset=-1", "", "http://flixr.test", http.StatusBadRequest},
		{http.MethodGet, "/missing", "", "http://flixr.test", http.StatusNotFound},
	} {
		r := httptest.NewRequest(test.method, "http://flixr.test/api/v1/owner/episode-orders"+test.path, bytes.NewBufferString(test.body))
		r.Header.Set("Origin", test.origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Errorf("%s %s origin=%s status=%d want=%d body=%s", test.method, test.path, test.origin, w.Code, test.status, w.Body.String())
		}
	}
}
