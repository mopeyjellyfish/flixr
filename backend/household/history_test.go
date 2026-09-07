package household_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestPersonalHistoryIsIdempotentBoundedAndSurvivesMissingMediaAndRestart(t *testing.T) {
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
	event := household.ViewingEvent{CatalogID: "removed-film", Title: "Removed Film", Kind: "film", Type: household.EventCompleted, Provenance: household.ProvenanceLocal, SourceID: "playback:1"}
	if err := h.RecordViewingEvent(p.ID, event); err != nil {
		t.Fatal(err)
	}
	if err := h.RecordViewingEvent(p.ID, event); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 99; i++ {
		if err := h.RecordViewingEvent(p.ID, household.ViewingEvent{CatalogID: "import", Title: "Imported", Kind: "film", Type: household.EventSummary, Provenance: household.ProvenanceImport, SourceID: "import-" + string(rune(i+1))}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := h.History(p.ID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 100 {
		t.Fatalf("history length=%d", len(page.Events))
	}
	found := false
	for _, got := range page.Events {
		if got.CatalogID == "removed-film" {
			found = true
			if got.SourceTime != nil {
				t.Fatal("local source time invented")
			}
		}
	}
	if !found {
		t.Fatal("missing-media history vanished")
	}
	if err := h.SetRating(p.ID, "removed-film", 5, household.ProvenanceLocal, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
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
	rating, ok, err := h.Rating(p.ID, "removed-film")
	if err != nil || !ok || rating.Value != 5 {
		t.Fatalf("rating=%+v %v %v", rating, ok, err)
	}
	page, err = h.History(p.ID, 5, "")
	if err != nil || len(page.Events) != 5 {
		t.Fatalf("history reopen=%d %v", len(page.Events), err)
	}
}
func TestHistoryClearUndoAndProfileIsolation(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := h.CreateProfile("One", "")
	two, _ := h.CreateProfile("Two", "")
	for _, p := range []household.Profile{one, two} {
		if err := h.RecordViewingEvent(p.ID, household.ViewingEvent{CatalogID: "film", Title: "Film", Kind: "film", Type: household.EventSummary, Provenance: household.ProvenanceImport, SourceID: p.ID}); err != nil {
			t.Fatal(err)
		}
	}
	clear, err := h.ClearHistory(one.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := h.History(one.ID, 10, "")
	if len(page.Events) != 0 {
		t.Fatal("clear did not hide history")
	}
	page, _ = h.History(two.ID, 10, "")
	if len(page.Events) != 1 {
		t.Fatal("clear crossed profile")
	}
	if err := h.UndoClearHistory(one.ID, clear.ID); err != nil {
		t.Fatal(err)
	}
	page, _ = h.History(one.ID, 10, "")
	if len(page.Events) != 1 {
		t.Fatal("undo did not restore history")
	}
	_ = time.Now()
}
