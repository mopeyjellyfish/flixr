package sqlite_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestPersonalHistoryMigratesActualPre018Database(t *testing.T) {
	dir := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(dir, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		var version int
		if _, err := fmt.Sscanf(filepath.Base(file), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 18 {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if _, err := old.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO profiles(id,name) VALUES('p','Ada')",
		"INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','Film.mkv')",
		"INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation,observation) VALUES('p','film',123,456,1,456,3,5)",
	} {
		if _, err := old.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var position, completed, generation int64
	if err := db.QueryRow("SELECT position_ms,completed,generation FROM progress WHERE profile_id='p' AND catalog_id='film'").Scan(&position, &completed, &generation); err != nil {
		t.Fatal(err)
	}
	if position != 123 || completed != 1 || generation != 3 {
		t.Fatalf("migration altered progress: %d %d %d", position, completed, generation)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	page, err := h.History("p", 10, "")
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("migration invented viewing events: %+v %v", page, err)
	}
	generation, _, err = h.BeginPlayback("p", "film")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := h.RecordPlaybackProgress("p", "film", 200, generation, 1, true, &household.ViewingEvent{CatalogID: "film", Title: "Film", Kind: "film"})
	if err != nil || !accepted {
		t.Fatalf("migrated completion=%v %v", accepted, err)
	}
	page, err = h.History("p", 10, "")
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("migrated ledger: %+v %v", page, err)
	}
}
