package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestScanSchedulerMigrationPreservesExistingRunAndCreatesPolicies(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 27 {
			continue
		}
		body, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = legacy.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec(`INSERT INTO scan_runs(id,started_at,status,scanned,failed,unmatched,message) VALUES('prior',123,'complete',7,0,1,'kept')`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var scanned, skipped, policies, jobs int
	var message string
	if err := db.QueryRow(`SELECT scanned,skipped,message FROM scan_runs WHERE id='prior'`).Scan(&scanned, &skipped, &message); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM library_scan_policies`).Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if scanned != 7 || skipped != 0 || message != "kept" || policies != 2 || jobs != 0 {
		t.Fatalf("migrated scan state scanned=%d skipped=%d message=%q policies=%d jobs=%d", scanned, skipped, message, policies, jobs)
	}
}
