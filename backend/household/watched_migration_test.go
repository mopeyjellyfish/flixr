package household_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	_ "modernc.org/sqlite"
)

func TestV12ProgressMigratesToWatchedState(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY); CREATE TABLE progress (profile_id TEXT NOT NULL, catalog_id TEXT NOT NULL, position_ms INTEGER NOT NULL, updated_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(profile_id,catalog_id)); INSERT INTO progress VALUES('ada','film',500,100);`)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 12; version++ {
		if _, err = raw.Exec("INSERT INTO schema_migrations(version) VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var position, completed, completedAt int64
	if err = db.QueryRow("SELECT position_ms,completed,completed_at FROM progress WHERE profile_id='ada' AND catalog_id='film'").Scan(&position, &completed, &completedAt); err != nil {
		t.Fatal(err)
	}
	if position != 500 || completed != 0 || completedAt != 0 {
		t.Fatalf("upgraded progress = %d/%d/%d", position, completed, completedAt)
	}
}
