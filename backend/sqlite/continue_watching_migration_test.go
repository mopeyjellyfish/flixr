package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestContinueWatchingMigrationPreservesExistingProfileState(t *testing.T) {
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
		if version >= 25 {
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
	if _, err = legacy.Exec(`INSERT INTO profiles(id,name) VALUES('profile','Profile');
		INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','film.mp4');
		INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES('profile','film',321);
		INSERT INTO profile_film_list(profile_id,catalog_id,added_at) VALUES('profile','film',123);`); err != nil {
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
	var position, listed, dismissals int
	if err = db.QueryRow("SELECT position_ms FROM progress WHERE profile_id='profile' AND catalog_id='film'").Scan(&position); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM profile_film_list WHERE profile_id='profile' AND catalog_id='film'").Scan(&listed); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM profile_continue_watching_dismissals").Scan(&dismissals); err != nil {
		t.Fatal(err)
	}
	if position != 321 || listed != 1 || dismissals != 0 {
		t.Fatalf("migrated state position=%d listed=%d dismissals=%d", position, listed, dismissals)
	}
}
