package web_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestDiagnosticsAreOwnerOnlyAndBounded(t *testing.T) {
	house := newHousehold(t)
	handler := web.NewServer(house, catalog.New()).Handler()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/owner/diagnostics", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer export status=%d", denied.Code)
	}
	errorID := denied.Header().Get("X-Flixr-Error-ID")
	if errorID == "" {
		t.Fatal("missing correlated error ID")
	}
	claim := httptest.NewRecorder()
	handler.ServeHTTP(claim, httptest.NewRequest(http.MethodPost, "/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"passphrase"}`)))
	owner := claim.Result().Cookies()[0]
	create := httptest.NewRequest(http.MethodPost, "/api/v1/profiles", bytes.NewBufferString(`{"name":"Viewer"}`))
	create.AddCookie(owner)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create profile status=%d", created.Code)
	}
	var profile struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(created.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	selectProfile := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/"+profile.ID+"/select", bytes.NewBufferString(`{"pin":""}`))
	selected := httptest.NewRecorder()
	handler.ServeHTTP(selected, selectProfile)
	if selected.Code != http.StatusOK {
		t.Fatalf("select profile status=%d", selected.Code)
	}
	viewerDenied := httptest.NewRecorder()
	viewerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/owner/diagnostics", nil)
	viewerRequest.AddCookie(selected.Result().Cookies()[0])
	handler.ServeHTTP(viewerDenied, viewerRequest)
	if viewerDenied.Code != http.StatusForbidden {
		t.Fatalf("profile export status=%d", viewerDenied.Code)
	}
	viewerErrorID := viewerDenied.Header().Get("X-Flixr-Error-ID")
	if viewerErrorID == "" {
		t.Fatal("profile failure missing correlated error ID")
	}
	privatePayload := `{"token":"top-secret","path":"/Users/owner/Movies/Private Film.mkv"} trailing`
	failed := httptest.NewRecorder()
	failedRequest := httptest.NewRequest(http.MethodPut, "/api/v1/owner/settings/tmdb", bytes.NewBufferString(privatePayload))
	failedRequest.AddCookie(owner)
	handler.ServeHTTP(failed, failedRequest)
	if failed.Code != http.StatusBadRequest {
		t.Fatalf("private failure status=%d", failed.Code)
	}
	privateErrorID := failed.Header().Get("X-Flixr-Error-ID")
	if privateErrorID == "" {
		t.Fatal("private failure missing correlated error ID")
	}
	export := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/diagnostics", nil)
	request.AddCookie(owner)
	handler.ServeHTTP(export, request)
	if export.Code != http.StatusOK {
		t.Fatalf("owner export status=%d: %s", export.Code, export.Body.String())
	}
	if got := export.Header().Get("Content-Disposition"); got == "" {
		t.Fatal("missing download disposition")
	}
	archive, err := zip.NewReader(bytes.NewReader(export.Body.Bytes()), int64(export.Body.Len()))
	if err != nil {
		t.Fatalf("diagnostic archive=%v", err)
	}
	if len(archive.File) != 1 {
		t.Fatalf("diagnostic files=%d", len(archive.File))
	}
	file, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Health   json.RawMessage `json:"health"`
		Failures []struct {
			ErrorID string `json:"error_id"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, failure := range bundle.Failures {
		ids[failure.ErrorID] = true
	}
	if len(bundle.Health) == 0 || !ids[errorID] || !ids[viewerErrorID] || !ids[privateErrorID] {
		t.Fatalf("unmatched diagnostics: %#v", bundle)
	}
	for _, private := range []string{"top-secret", "/Users/owner", "Private Film"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("archive leaked %q", private)
		}
	}
}
