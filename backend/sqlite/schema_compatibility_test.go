package sqlite_test

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	_ "modernc.org/sqlite"
)

func TestOpenRefusesUnsupportedAppliedSchemaBeforeMigration(t *testing.T) {
	for _, version := range []int{16, sqlite.LatestSchemaVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			dir := t.TempDir()
			db, err := sqlite.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`CREATE TABLE upgrade_sentinel(value TEXT NOT NULL); INSERT INTO upgrade_sentinel(value) VALUES('preserved')`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, openErr := sqlite.Open(dir)
			if openErr == nil {
				reopened.Close()
				t.Fatalf("unsupported applied schema %d was accepted", version)
			}
			var compatibility *sqlite.SchemaCompatibilityError
			if !errors.Is(openErr, sqlite.ErrIncompatibleSchema) || !errors.As(openErr, &compatibility) {
				t.Fatalf("open error = %v, want typed schema compatibility error", openErr)
			}
			if compatibility.FoundVersion != version || compatibility.SupportedVersion != sqlite.LatestSchemaVersion {
				t.Fatalf("compatibility error = %+v", compatibility)
			}
			if message := openErr.Error(); !strings.Contains(message, "update Flixr") || !strings.Contains(message, "restore a compatible backup") {
				t.Fatalf("compatibility guidance = %q", message)
			}

			raw, err := sql.Open("sqlite", filepath.Join(dir, "flixr.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var value string
			if err = raw.QueryRow(`SELECT value FROM upgrade_sentinel`).Scan(&value); err != nil || value != "preserved" {
				t.Fatalf("sentinel after refusal = %q, %v", value, err)
			}
			var marker int
			if err = raw.QueryRow(`SELECT version FROM schema_migrations WHERE version=?`, version).Scan(&marker); err != nil || marker != version {
				t.Fatalf("schema marker after refusal = %d, %v", marker, err)
			}
		})
	}
}

func TestOpenRefusesNonemptyDatabaseWithoutMigrationHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flixr.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`CREATE TABLE foreign_product_state(value TEXT NOT NULL); INSERT INTO foreign_product_state VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, openErr := sqlite.Open(dir)
	if openErr == nil {
		db.Close()
		t.Fatal("nonempty database without migration history was accepted")
	}
	var compatibility *sqlite.SchemaCompatibilityError
	if !errors.Is(openErr, sqlite.ErrIncompatibleSchema) || !errors.As(openErr, &compatibility) {
		t.Fatalf("open error = %v, want typed schema compatibility error", openErr)
	}
	if compatibility.FoundVersion != 0 || compatibility.SupportedVersion != sqlite.LatestSchemaVersion || !compatibility.MissingMigrationHistory {
		t.Fatalf("compatibility error = %+v", compatibility)
	}
	if message := openErr.Error(); !strings.Contains(message, "no Flixr migration history") || !strings.Contains(message, "empty data directory") || !strings.Contains(message, "restore a compatible backup") {
		t.Fatalf("compatibility guidance = %q", message)
	}

	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var value string
	if err = raw.QueryRow(`SELECT value FROM foreign_product_state`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("foreign state after refusal = %q, %v", value, err)
	}
	var schema []string
	rows, err := raw.Query(`SELECT type || ':' || name FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var object string
		if err = rows.Scan(&object); err != nil {
			t.Fatal(err)
		}
		schema = append(schema, object)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(schema) != "[table:foreign_product_state]" {
		t.Fatalf("schema after refusal = %v", schema)
	}
}

func TestOpenInitializesGenuinelyEmptyDatabase(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = raw.Ping(); err != nil {
		t.Fatal(err)
	}
	var userObjects int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&userObjects); err != nil || userObjects != 0 {
		t.Fatalf("empty database user objects = %d, %v", userObjects, err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := db.SchemaVersion()
	if err != nil || version != sqlite.LatestSchemaVersion {
		t.Fatalf("initialized schema = %d, %v", version, err)
	}
}
