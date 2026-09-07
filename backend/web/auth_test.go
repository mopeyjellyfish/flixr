package web_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestCatalogRequiresSelectedProfileAndRejectsCrossOriginWrite(t *testing.T) {
	h := newHousehold(t)
	server := web.NewServer(h, catalog.New()).Handler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/home", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous catalog status=%d", w.Code)
	}
	claim := httptest.NewRequest(http.MethodPost, "/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+h.SetupToken()+`","password":"passphrase"}`))
	claim.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, claim)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin claim status=%d", w.Code)
	}
}

func TestReadinessRequiresOwnerAndSessionCookieIsStrict(t *testing.T) {
	house := newHousehold(t)
	server := web.NewServer(house, catalog.New()).Handler()
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/owner/readiness/recheck", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous readiness status=%d", w.Code)
	}
	claim := httptest.NewRequest(http.MethodPost, "/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"passphrase"}`))
	w = httptest.NewRecorder()
	server.ServeHTTP(w, claim)
	if w.Code != http.StatusCreated {
		t.Fatalf("claim status=%d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("unsafe session cookie: %#v", cookie)
	}
}

func TestTLSClaimUsesSecureSessionCookie(t *testing.T) {
	house := newHousehold(t)
	server := web.NewServer(house, catalog.New()).Handler()
	request := httptest.NewRequest(http.MethodPost, "https://flixr.local/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"passphrase"}`))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("claim status %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("TLS claim cookie missing security attributes")
	}
}

func TestOwnerRecoveryHasNoHTTPRoute(t *testing.T) {
	h := web.NewServer(newHousehold(t), catalog.New()).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/owner/recover", bytes.NewBufferString(`{"password":"replacement"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("recovery route status=%d", w.Code)
	}
}
