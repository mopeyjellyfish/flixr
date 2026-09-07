package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestLogicalIdentityMigrationSeedsExistingCatalogRows(t *testing.T) {
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
		fmt.Sscanf(entry.Name(), "%d_", &version)
		if version >= 19 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec("INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,fingerprint,size_bytes,mtime_unix) VALUES('legacy','film','Legacy','Film.mp4','film','fingerprint',42,7)"); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO profiles(id,name) VALUES('p','One'); INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES('p','legacy',123)"); err != nil {
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
	var catalogID, primary, digest string
	var selected, present, position int
	if err = db.QueryRow("SELECT catalog_id,id,full_digest,present,selected FROM catalog_physical_files WHERE relative_path='Film.mp4'").Scan(&catalogID, &primary, &digest, &present, &selected); err != nil {
		t.Fatal(err)
	}
	if catalogID != "legacy" || primary != "legacy:legacy" || digest != "" || present != 1 || selected != 1 {
		t.Fatalf("migrated source %s %s %s %d %d", catalogID, primary, digest, present, selected)
	}
	if err = db.QueryRow("SELECT position_ms FROM progress WHERE catalog_id='legacy'").Scan(&position); err != nil || position != 123 {
		t.Fatalf("progress %d %v", position, err)
	}
}
