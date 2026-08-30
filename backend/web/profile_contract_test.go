package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestProfileResponseUsesStableLowercaseFields(t *testing.T) {
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
		t.Fatalf("claim status = %d, want %d", claimed.Code, http.StatusCreated)
	}

	create := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/profiles", bytes.NewBufferString(`{"name":"Living Room","pin":""}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Origin", "http://flixr.test")
	create.AddCookie(claimed.Result().Cookies()[0])
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create profile status = %d, want %d", created.Code, http.StatusCreated)
	}

	var profile map[string]any
	if err := json.NewDecoder(created.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "name", "protected"} {
		if _, ok := profile[key]; !ok {
			t.Fatalf("profile response missing lowercase %q: %#v", key, profile)
		}
	}
	for _, key := range []string{"ID", "Name", "Protected"} {
		if _, ok := profile[key]; ok {
			t.Fatalf("profile response leaked unstable field %q: %#v", key, profile)
		}
	}
}
