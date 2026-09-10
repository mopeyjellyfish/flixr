package web_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestProfilePINFormatIsRejectedWithANamedCode(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(house, catalog.New()).Handler()
	claim := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"correct horse battery staple"}`))
	claim.Header.Set("Content-Type", "application/json")
	claim.Header.Set("Origin", "http://flixr.test")
	claimed := httptest.NewRecorder()
	handler.ServeHTTP(claimed, claim)
	if claimed.Code != http.StatusCreated {
		t.Fatalf("claim status %d: %s", claimed.Code, claimed.Body.String())
	}
	cookies := claimed.Result().Cookies()

	create := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/profiles", bytes.NewBufferString(`{"name":"Ada","pin":"12ab"}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Origin", "http://flixr.test")
	for _, c := range cookies {
		create.AddCookie(c)
	}
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusBadRequest || !strings.Contains(created.Body.String(), `"invalid_pin_format"`) {
		t.Fatalf("create status %d: %s", created.Code, created.Body.String())
	}
}
