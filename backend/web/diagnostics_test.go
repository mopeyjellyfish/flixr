package web_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	export := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/diagnostics", nil)
	request.AddCookie(claim.Result().Cookies()[0])
	handler.ServeHTTP(export, request)
	if export.Code != http.StatusOK {
		t.Fatalf("owner export status=%d: %s", export.Code, export.Body.String())
	}
	if got := export.Header().Get("Content-Disposition"); got == "" {
		t.Fatal("missing download disposition")
	}
	archive, err := zip.NewReader(bytes.NewReader(export.Body.Bytes()), int64(export.Body.Len()))
	if err != nil || len(archive.File) != 1 {
		t.Fatalf("diagnostic archive=%v files=%d", err, len(archive.File))
	}
	file, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Health   json.RawMessage `json:"health"`
		Failures []struct {
			ErrorID string `json:"error_id"`
		} `json:"failures"`
	}
	if err := json.NewDecoder(file).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Health) == 0 || len(bundle.Failures) != 1 || bundle.Failures[0].ErrorID != errorID {
		t.Fatalf("unmatched diagnostics: %#v", bundle)
	}
}
