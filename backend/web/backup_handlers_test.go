package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/backup"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestBackupRoutesAreOwnerOnlySameOriginAndQueueVerifiedWork(t *testing.T) {
	data := t.TempDir()
	destination := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := backup.NewManager(db, backup.Source{DB: db, DataDir: data, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(house, cat)
	server.AttachBackupManager(manager)
	handler := server.Handler()
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "http://flixr.test/api/v1/owner/backups", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	claim := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/setup/claim", bytes.NewBufferString(`{"token":"`+house.SetupToken()+`","password":"password"}`))
	request.Header.Set("Origin", "http://flixr.test")
	handler.ServeHTTP(claim, request)
	cookie := claim.Result().Cookies()[0]
	policy := []byte(`{"enabled":false,"destination":"` + destination + `","schedule_kind":"interval","interval_seconds":3600,"local_time":"03:00","timezone":"UTC","retain_count":2,"retain_age_seconds":86400,"budget_bytes":1073741824,"last_status":"never"}`)
	cross := httptest.NewRequest(http.MethodPut, "http://flixr.test/api/v1/owner/backups/policy", bytes.NewReader(policy))
	cross.Header.Set("Origin", "http://evil.test")
	cross.AddCookie(cookie)
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, cross)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", blocked.Code)
	}
	save := httptest.NewRequest(http.MethodPut, "http://flixr.test/api/v1/owner/backups/policy", bytes.NewReader(policy))
	save.Header.Set("Origin", "http://flixr.test")
	save.AddCookie(cookie)
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, save)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
	}
	run := httptest.NewRequest(http.MethodPost, "http://flixr.test/api/v1/owner/backups/jobs", nil)
	run.Header.Set("Origin", "http://flixr.test")
	run.AddCookie(cookie)
	queued := httptest.NewRecorder()
	handler.ServeHTTP(queued, run)
	if queued.Code != http.StatusAccepted {
		t.Fatalf("queue status=%d body=%s", queued.Code, queued.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)
	cancel()
	shutdown, done := context.WithCancel(context.Background())
	defer done()
	if err := manager.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
}
