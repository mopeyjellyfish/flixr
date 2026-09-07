package household_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	_ "modernc.org/sqlite"
)

func TestPre013ProgressMigratesToWatchedState(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join("..", "sqlite", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		var version int
		if _, err = fmt.Sscanf(filepath.Base(file), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 13 {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = raw.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = raw.Exec("INSERT INTO schema_migrations(version) VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = raw.Exec(`INSERT INTO profiles(id,name) VALUES('ada','Ada'); INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','film.mp4'); INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at) VALUES('ada','film',500,100)`); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var position, completed, completedAt, generation, observation int64
	if err = db.QueryRow("SELECT position_ms,completed,completed_at,generation,observation FROM progress WHERE profile_id='ada' AND catalog_id='film'").Scan(&position, &completed, &completedAt, &generation, &observation); err != nil {
		t.Fatal(err)
	}
	if position != 500 || completed != 0 || completedAt != 0 || generation != 0 || observation != 0 {
		t.Fatalf("upgraded progress = %d/%d/%d", position, completed, completedAt)
	}
}
