package catalog

import (
	"fmt"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestSaveStatusRetainsCurrentAndThirtyOneRecentScanReports(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 40 {
		id := fmt.Sprintf("run-%02d", i)
		if _, err := db.Exec("INSERT INTO scan_runs(id,started_at,finished_at,status) VALUES(?,100,101,'complete')", id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO scan_files(scan_id,relative_path,outcome,message) VALUES(?,?,'matched','provider detail')", id, id+".mkv"); err != nil {
			t.Fatal(err)
		}
	}
	c := &Catalog{db: db}
	c.saveStatus(ScanStatus{ID: "current", StartedAt: 100, Status: "running"})

	var runs int
	if err := db.QueryRow("SELECT count(*) FROM scan_runs").Scan(&runs); err != nil || runs != 32 {
		t.Fatalf("retained scan reports = %d err=%v", runs, err)
	}
	for _, id := range []string{"current", "run-39", "run-09"} {
		var found int
		if err := db.QueryRow("SELECT count(*) FROM scan_runs WHERE id=?", id).Scan(&found); err != nil || found != 1 {
			t.Fatalf("retained %s = %d err=%v", id, found, err)
		}
	}
	var files int
	if err := db.QueryRow("SELECT count(*) FROM scan_files WHERE scan_id='run-39'").Scan(&files); err != nil || files != 1 {
		t.Fatalf("latest provider detail = %d err=%v", files, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM scan_files WHERE scan_id='run-08'").Scan(&files); err != nil || files != 0 {
		t.Fatalf("expired provider detail = %d err=%v", files, err)
	}
}

func TestSaveStatusFailureDoesNotPruneExistingScanReports(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 33 {
		if _, err := db.Exec("INSERT INTO scan_runs(id,started_at,finished_at,status) VALUES(?,?,101,'complete')", fmt.Sprintf("run-%02d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_current_scan BEFORE INSERT ON scan_runs WHEN NEW.id='current' BEGIN SELECT RAISE(ABORT, 'injected upsert failure'); END`); err != nil {
		t.Fatal(err)
	}
	c := &Catalog{db: db}
	c.saveStatus(ScanStatus{ID: "current", StartedAt: 100, Status: "running"})

	var runs int
	if err := db.QueryRow("SELECT count(*) FROM scan_runs").Scan(&runs); err != nil || runs != 33 {
		t.Fatalf("reports after failed upsert = %d err=%v", runs, err)
	}
}
