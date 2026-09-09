package web_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

func TestOwnerManagesLibraryScanPolicyAndJobs(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Claim(h.SetupToken(), "passphrase")
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewServer(h, c).Handler()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "flixr_session", Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/api/v1/owner/libraries/films/scan-policy", "", ""); got.Code != http.StatusForbidden {
		t.Fatalf("anonymous policy = %d", got.Code)
	}
	saved := request(http.MethodPatch, "/api/v1/owner/libraries/films/scan-policy", `{"enabled":true,"schedule_kind":"daily","interval_seconds":86400,"local_time":"03:30","timezone":"Europe/London","exclusions":["Extras/**"]}`, owner)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"next_run_at"`) {
		t.Fatalf("save policy = %d %s", saved.Code, saved.Body.String())
	}
	started := request(http.MethodPost, "/api/v1/owner/scan/jobs", `{"library_id":"films"}`, owner)
	if started.Code != http.StatusAccepted || !strings.Contains(started.Body.String(), `"status":"queued"`) {
		t.Fatalf("start job = %d %s", started.Code, started.Body.String())
	}
	jobs := request(http.MethodGet, "/api/v1/owner/scan/jobs", "", owner)
	if jobs.Code != http.StatusOK || !strings.Contains(jobs.Body.String(), `"trigger":"manual"`) {
		t.Fatalf("jobs = %d %s", jobs.Code, jobs.Body.String())
	}
}
