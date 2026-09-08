package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
)

type validatingProvider struct{ err error }

func (p validatingProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{}, nil
}
func (p validatingProvider) Validate(context.Context, string) error { return p.err }

func TestTMDBSettingsValidateBeforeSavingAndExposeActionableState(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	session, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	catalogue := catalog.New()
	server := NewServer(house, catalogue)
	request := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/owner/settings/tmdb", bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}

	catalogue.SetProvider(validatingProvider{err: catalog.ErrInvalidCredential})
	invalid := request(http.MethodPut, `{"token":"bad"}`)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "metadata_invalid_credential") || catalogue.TMDBConfigured() {
		t.Fatalf("invalid save = %d %s configured=%v", invalid.Code, invalid.Body.String(), catalogue.TMDBConfigured())
	}
	catalogue.SetProvider(validatingProvider{err: errors.New("provider offline")})
	unavailable := request(http.MethodPut, `{"token":"secret"}`)
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), "metadata_unavailable") || catalogue.TMDBConfigured() {
		t.Fatalf("unavailable save = %d %s configured=%v", unavailable.Code, unavailable.Body.String(), catalogue.TMDBConfigured())
	}
	catalogue.SetProvider(validatingProvider{})
	saved := request(http.MethodPut, `{"token":"secret"}`)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"configured":true`) || !strings.Contains(saved.Body.String(), `"state":"configured"`) || strings.Contains(saved.Body.String(), "secret") {
		t.Fatalf("valid save = %d %s", saved.Code, saved.Body.String())
	}
}

func TestTMDBSettingsDisableAndOverrideRemovalAreIndependent(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	session, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	catalogue := catalog.New()
	catalogue.SetApplicationTMDBToken("application-token")
	catalogue.SetProvider(validatingProvider{})
	server := NewServer(house, catalogue)
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/owner/settings/tmdb", bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}

	if response := request(`{"token":"owner-token"}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"owner"`) {
		t.Fatalf("owner override = %d %s", response.Code, response.Body.String())
	}
	if response := request(`{"enabled":false}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"disabled"`) {
		t.Fatalf("disable = %d %s", response.Code, response.Body.String())
	}
	if response := request(`{"token":""}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"disabled"`) {
		t.Fatalf("remove while disabled = %d %s", response.Code, response.Body.String())
	}
	if response := request(`{"enabled":true}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"application"`) || strings.Contains(response.Body.String(), "application-token") {
		t.Fatalf("re-enable = %d %s", response.Code, response.Body.String())
	}
}
