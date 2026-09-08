package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestOwnerClaimIsOneTime(t *testing.T) {
	house := newHousehold(t)
	h := web.NewServer(house, catalog.New()).Handler()
	claim := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+token+`","password":"secret-passphrase"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := claim("").Code; got != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", got)
	}
	status := httptest.NewRecorder()
	h.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	var body map[string]any
	if err := json.NewDecoder(status.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, leaked := body["token"]; leaked {
		t.Fatal("setup status leaked the console-only setup token")
	}
	if got := claim(house.SetupToken()).Code; got != http.StatusCreated {
		t.Fatalf("valid claim status = %d, want 201", got)
	}
	if got := claim(house.SetupToken()).Code; got != http.StatusConflict {
		t.Fatalf("reused token status = %d, want 409", got)
	}
}

func TestSetupStatusReportsAutomaticMetadataAvailabilityWithoutCredentials(t *testing.T) {
	house := newHousehold(t)
	catalogue := catalog.New()
	catalogue.SetApplicationTMDBToken("application-token")
	response := httptest.NewRecorder()
	web.NewServer(house, catalogue).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"metadata":{"provider":"tmdb","enabled":true,"configured":true,"source":"application"`) || strings.Contains(response.Body.String(), "application-token") {
		t.Fatalf("setup status = %d %s", response.Code, response.Body.String())
	}
}

func newHousehold(t *testing.T) *household.Manager {
	t.Helper()
	h, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	return h
}
