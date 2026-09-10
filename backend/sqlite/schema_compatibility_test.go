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
