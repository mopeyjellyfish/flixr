package web

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
)

func TestScanRefusesWhenFFprobeUnavailable(t *testing.T) {
	house, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	session, err := house.Claim(house.SetupToken(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(house, catalog.New())
	server.lookPath = func(name string) (string, error) {
		if name == "ffprobe" {
			return "", errors.New("not found")
		}
		return "/test/ffmpeg", nil
	}
	server.checkReadiness()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/owner/scan", bytes.NewBuffer(nil))
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != "{\"error\":{\"code\":\"ffprobe_unavailable\"}}\n" {
		t.Fatalf("scan without ffprobe = %d %s", w.Code, w.Body.String())
	}
	if status := server.catalog.ScanStatus(); status.Status == "running" {
		t.Fatal("ffprobe-unavailable request started a scan")
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/owner/scan/jobs", bytes.NewBufferString(`{"library_id":"films"}`))
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != "{\"error\":{\"code\":\"ffprobe_unavailable\"}}\n" {
		t.Fatalf("per-library scan without ffprobe = %d %s", w.Code, w.Body.String())
	}
}
