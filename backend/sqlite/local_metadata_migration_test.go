package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestLocalMetadataMigrationPreservesOwnerFieldsAndAddsEpisodeTargets(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
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
		if version >= 31 {
			continue
		}
		body, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = legacy.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", version, err)
		}
		if _, err = legacy.Exec(`INSERT INTO schema_migrations VALUES(?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film','film','title','Owner','owner',1)`); err != nil {
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
	var value string
	if err = db.QueryRow(`SELECT value FROM catalog_metadata_fields WHERE catalog_kind='film' AND catalog_id='film' AND field='title'`).Scan(&value); err != nil || value != "Owner" {
		t.Fatalf("preserved=%q %v", value, err)
	}
	if _, err = db.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('episode','episode','title','Local','local',0)`); err != nil {
		t.Fatalf("episode field: %v", err)
	}
}
