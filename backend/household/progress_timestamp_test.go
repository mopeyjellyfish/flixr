package household_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestProgressWritesUpdatedAt(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := h.CreateProfile("Ada", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','Film.demo')"); err != nil {
		t.Fatal(err)
	}
	if err := h.ProgressForProfile(profile.ID, "film", 100); err != nil {
		t.Fatal(err)
	}
	var updated int64
	if err := db.QueryRow("SELECT updated_at FROM progress WHERE profile_id=? AND catalog_id=?", profile.ID, "film").Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated == 0 {
		t.Fatal("progress insert did not write updated_at")
	}
	if _, err := db.Exec("UPDATE progress SET updated_at=1 WHERE profile_id=? AND catalog_id=?", profile.ID, "film"); err != nil {
		t.Fatal(err)
	}
	if err := h.ProgressForProfile(profile.ID, "film", 200); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT updated_at FROM progress WHERE profile_id=? AND catalog_id=?", profile.ID, "film").Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated <= 1 {
		t.Fatalf("progress update timestamp=%d, want > 1", updated)
	}
}
