package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestLibraryScanSafetyMigrationPreservesExistingCatalogAndProgress(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 24 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,fingerprint,size_bytes,mtime_unix) VALUES('film','film','Film','Film.mp4','film','fingerprint',42,7); INSERT INTO profiles(id,name) VALUES('profile','One'); INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES('profile','film',123)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var position int
	if err := db.QueryRow(`SELECT position_ms FROM progress WHERE catalog_id='film'`).Scan(&position); err != nil || position != 123 {
		t.Fatalf("migrated progress = %d, %v", position, err)
	}
	if _, err := db.Exec(`INSERT INTO library_locations(root_kind,state) VALUES('film','unknown')`); err != nil {
		t.Fatalf("migrated location table: %v", err)
	}
}
