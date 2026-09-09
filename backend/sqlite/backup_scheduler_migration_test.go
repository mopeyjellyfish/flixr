package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestBackupSchedulerMigrationPreservesExistingHousehold(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 29 {
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
	if _, err = legacy.Exec(`INSERT INTO settings(key,value) VALUES('migration-proof','kept')`); err != nil {
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
	var value, destination, status string
	var count int
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='migration-proof'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT destination,last_status,retain_count FROM backup_policy WHERE id=1`).Scan(&destination, &status, &count); err != nil {
		t.Fatal(err)
	}
	if value != "kept" || destination != "" || status != "never" || count != 7 {
		t.Fatalf("migrated value=%q destination=%q status=%q count=%d", value, destination, status, count)
	}
}
