package household_test

import (
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestProgressKeepsNewerObservationAndWatchedStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.CreateProfile("Ada", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','film.mp4')"); err != nil {
		t.Fatal(err)
	}
	if err = h.RecordProgress(p.ID, "film", 800, 200, false); err != nil {
		t.Fatal(err)
	}
	if err = h.RecordProgress(p.ID, "film", 100, 100, false); err != nil {
		t.Fatal(err)
	}
	var latest int64
	if err = db.QueryRow("SELECT position_ms FROM progress WHERE profile_id=? AND catalog_id='film'", p.ID).Scan(&latest); err != nil || latest != 800 {
		t.Fatalf("late progress overwrote newer state: %d, %v", latest, err)
	}
	if err = h.SetWatched(p.ID, []string{"film"}, true); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(filepath.Clean(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err = household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	var position int64
	var completed int
	if err = db.QueryRow("SELECT position_ms,completed FROM progress WHERE profile_id=? AND catalog_id='film'", p.ID).Scan(&position, &completed); err != nil {
		t.Fatal(err)
	}
	if position != 1<<62 || completed != 1 {
		t.Fatalf("persisted watched state = %d/%d", position, completed)
	}
	session, err := h.Select(p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if position, err := h.Position(session, "film"); err != nil || position != 0 {
		t.Fatalf("completed item resume = %d, %v", position, err)
	}
}
