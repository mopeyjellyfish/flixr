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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIFallthroughUsesJSONErrorEnvelope(t *testing.T) {
	h, err := household.New()
	require.NoError(t, err)
	handler := web.NewServer(h, catalog.New()).Handler()

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil),
		httptest.NewRequest(http.MethodDelete, "/api/v1/setup/status", nil),
		httptest.NewRequest(http.MethodGet, "/api/v2/missing", nil),
		httptest.NewRequest(http.MethodGet, "/api/health", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
		var body map[string]map[string]string
		require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
		assert.Equal(t, "not_found", body["error"]["code"])
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	assert.Equal(t, http.StatusOK, status.Code)
}

func TestProfileCredentialFailuresUseDistinctCapacityAndRateLimitCodes(t *testing.T) {
	h, err := household.New()
	require.NoError(t, err)
	handler := web.NewServer(h, catalog.New()).Handler()

	claim := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+h.SetupToken()+`","password":"correct horse battery staple"}`))
	claim.Header.Set("Origin", "http://flixr.test")
	claimed := httptest.NewRecorder()
	handler.ServeHTTP(claimed, claim)
	require.Equal(t, http.StatusCreated, claimed.Code)

	create := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/profiles", bytes.NewBufferString(`{"name":"Ari","pin":"1234"}`))
	create.Header.Set("Origin", "http://flixr.test")
	create.AddCookie(claimed.Result().Cookies()[0])
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	require.Equal(t, http.StatusCreated, created.Code)

	var profile struct{ ID string }
	require.NoError(t, json.NewDecoder(created.Body).Decode(&profile))

	responses := make(chan *httptest.ResponseRecorder, 3)
	for range 3 {
		go func() {
			request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/profiles/"+profile.ID+"/select", bytes.NewBufferString(`{"pin":"bad"}`))
			request.Header.Set("Origin", "http://flixr.test")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			responses <- response
		}()
	}

	var codes []string
	for range 3 {
		var body struct{ Error struct{ Code string } }
		response := <-responses
		require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
		if response.Code == http.StatusTooManyRequests {
			codes = append(codes, body.Error.Code)
		}
	}
	assert.Contains(t, codes, "credential_busy")

	for range 5 {
		request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/profiles/"+profile.ID+"/select", bytes.NewBufferString(`{"pin":"bad"}`))
		request.Header.Set("Origin", "http://flixr.test")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
	request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/profiles/"+profile.ID+"/select", bytes.NewBufferString(`{"pin":"1234"}`))
	request.Header.Set("Origin", "http://flixr.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.JSONEq(t, `{"error":{"code":"pin_rate_limited"}}`, response.Body.String())
}

func TestOwnerLoginDistinguishesCapacityAndRateLimitCodes(t *testing.T) {
	h, err := household.New()
	require.NoError(t, err)
	handler := web.NewServer(h, catalog.New()).Handler()

	claim := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+h.SetupToken()+`","password":"correct horse battery staple"}`))
	claim.Header.Set("Origin", "http://flixr.test")
	claimed := httptest.NewRecorder()
	handler.ServeHTTP(claimed, claim)
	require.Equal(t, http.StatusCreated, claimed.Code)

	responses := make(chan *httptest.ResponseRecorder, 3)
	for range 3 {
		go func() {
			request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/owner/login", bytes.NewBufferString(`{"password":"wrong password"}`))
			request.Header.Set("Origin", "http://flixr.test")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			responses <- response
		}()
	}

	var codes []string
	for range 3 {
		var body struct{ Error struct{ Code string } }
		response := <-responses
		require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
		if response.Code == http.StatusTooManyRequests {
			codes = append(codes, body.Error.Code)
		}
	}
	assert.Contains(t, codes, "credential_busy")

	for range 5 {
		request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/owner/login", bytes.NewBufferString(`{"password":"wrong password"}`))
		request.Header.Set("Origin", "http://flixr.test")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/owner/login", bytes.NewBufferString(`{"password":"correct horse battery staple"}`))
	request.Header.Set("Origin", "http://flixr.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.JSONEq(t, `{"error":{"code":"login_rate_limited"}}`, response.Body.String())
}

func TestUpdateProfileErrorContract(t *testing.T) {
	h, err := household.New()
	require.NoError(t, err)
	handler := web.NewServer(h, catalog.New()).Handler()

	claim := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+h.SetupToken()+`","password":"correct horse battery staple"}`))
	claim.Header.Set("Origin", "http://flixr.test")
	claimed := httptest.NewRecorder()
	handler.ServeHTTP(claimed, claim)
	require.Equal(t, http.StatusCreated, claimed.Code)

	request := httptest.NewRequest(http.MethodPatch, "http://flixr.test/api/v1/profiles/missing", bytes.NewBufferString(`{"name":"Ada"}`))
	request.Header.Set("Origin", "http://flixr.test")
	request.AddCookie(claimed.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.JSONEq(t, `{"error":{"code":"profile_not_found"}}`, response.Body.String())
}
