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
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestOwnerPlaybackActivityAndExactStopAreOwnerOnly(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("Viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	profileToken, err := house.Select(profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	manager := playback.NewDirectManager()
	session, err := manager.CreateForViewer("viewer-secret", profile.ID, "film-safe-id", playback.Plan{Kind: playback.Direct, SourceKey: "/private/media/film.mkv", Description: "Original media"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServerWithPlayback(house, catalog.New(), manager).Handler()

	profileRequest := httptest.NewRequest(http.MethodGet, "http://flixr.test/api/v1/owner/playback/activity", nil)
	profileRequest.AddCookie(&http.Cookie{Name: "flixr_session", Value: profileToken})
	profileResponse := httptest.NewRecorder()
	handler.ServeHTTP(profileResponse, profileRequest)
	if profileResponse.Code != http.StatusForbidden {
		t.Fatalf("profile activity status = %d", profileResponse.Code)
	}

	claim := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"password123"}`))
	claim.Header.Set("Origin", "http://flixr.test")
	claimed := httptest.NewRecorder()
	handler.ServeHTTP(claimed, claim)
	if claimed.Code != http.StatusCreated {
		t.Fatalf("claim = %d: %s", claimed.Code, claimed.Body.String())
	}
	owner := claimed.Result().Cookies()[0]

	activity := httptest.NewRequest(http.MethodGet, "http://flixr.test/api/v1/owner/playback/activity?limit=1", nil)
	activity.AddCookie(owner)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, activity)
	if result.Code != http.StatusOK {
		t.Fatalf("activity = %d: %s", result.Code, result.Body.String())
	}
	body := result.Body.String()
	if strings.Contains(body, session.ID) || strings.Contains(body, "viewer-secret") || strings.Contains(body, "/private/media") {
		t.Fatalf("activity leaked secret: %s", body)
	}
	var page playback.ActivityPage
	if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].Title != "Unknown title" || page.Sessions[0].Device != "Unknown device" {
		t.Fatalf("activity page = %+v", page)
	}

	badOrigin := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/owner/playback/sessions/"+page.Sessions[0].OwnerHandle+"/stop", nil)
	badOrigin.Header.Set("Origin", "http://evil.test")
	badOrigin.AddCookie(owner)
	badOriginResult := httptest.NewRecorder()
	handler.ServeHTTP(badOriginResult, badOrigin)
	if badOriginResult.Code != http.StatusForbidden {
		t.Fatalf("bad origin stop = %d", badOriginResult.Code)
	}

	stop := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/owner/playback/sessions/"+page.Sessions[0].OwnerHandle+"/stop", nil)
	stop.Header.Set("Origin", "http://flixr.test")
	stop.AddCookie(owner)
	stopped := httptest.NewRecorder()
	handler.ServeHTTP(stopped, stop)
	if stopped.Code != http.StatusOK {
		t.Fatalf("stop = %d: %s", stopped.Code, stopped.Body.String())
	}
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, stop.Clone(stop.Context()))
	if stale.Code != http.StatusNotFound {
		t.Fatalf("stale stop = %d", stale.Code)
	}
}

func TestOwnerPlaybackActivityRejectsInvalidPagination(t *testing.T) {
	house := newHousehold(t)
	handler := web.NewServer(house, catalog.New()).Handler()
	claim := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"password123"}`))
	claim.Header.Set("Origin", "http://flixr.test")
	claimed := httptest.NewRecorder()
	handler.ServeHTTP(claimed, claim)

	request := httptest.NewRequest(http.MethodGet, "http://flixr.test/api/v1/owner/playback/activity?limit=101&cursor=tampered", nil)
	request.AddCookie(claimed.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") {
		t.Fatalf("invalid page = %d: %s", response.Code, response.Body.String())
	}
}
