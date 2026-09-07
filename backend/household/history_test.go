package household_test

import (
	"errors"
	"fmt"
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
}

func TestHistoryOwnsReceiptIdentityAndTraversesEveryPage(t *testing.T) {
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
	start := time.Now().UnixMilli()
	sourceTime := int64(1234)
	for _, profile := range []household.Profile{one, two} {
		for i := 0; i < 107; i++ {
			event := household.ViewingEvent{ID: "foreign-id", CatalogID: "film", Title: "Film", Kind: "film", Type: household.EventSummary, Provenance: household.ProvenanceImport, SourceID: fmt.Sprint(i), SourceTime: &sourceTime, RecordedAt: -42}
			if err := h.RecordViewingEvent(profile.ID, event); err != nil {
				t.Fatal(err)
			}
			if err := h.RecordViewingEvent(profile.ID, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	for _, profile := range []household.Profile{one, two} {
		cursor := ""
		count := 0
		for {
			page, err := h.History(profile.ID, 7, cursor)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range page.Events {
				if seen[event.ID] || event.ID == "foreign-id" {
					t.Fatalf("reused internal ID: %+v", event)
				}
				seen[event.ID] = true
				count++
				if event.RecordedAt < start || event.SourceTime == nil || *event.SourceTime != sourceTime {
					t.Fatalf("incorrect receipt/source time: %+v", event)
				}
			}
			if page.Next == "" {
				break
			}
			if page.Next == cursor {
				t.Fatal("cursor stalled")
			}
			cursor = page.Next
			if count > 107 {
				t.Fatal("pagination repeated events")
			}
		}
		if count != 107 {
			t.Fatalf("events=%d", count)
		}
	}
}

func TestHistoryUndoRejectsOtherProfileAndExpiredClear(t *testing.T) {
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
	event := household.ViewingEvent{CatalogID: "film", Title: "Film", Kind: "film", Type: household.EventSummary, Provenance: household.ProvenanceImport}
	if err := h.RecordViewingEvent(one.ID, event); err != nil {
		t.Fatal(err)
	}
	clear, err := h.ClearHistory(one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.UndoClearHistory(two.ID, clear.ID); !errors.Is(err, household.ErrHistoryClearNotFound) {
		t.Fatalf("cross-profile undo=%v", err)
	}
	if _, err := db.Exec("UPDATE viewing_history_clears SET undo_until=0 WHERE clear_id=?", clear.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.UndoClearHistory(one.ID, clear.ID); !errors.Is(err, household.ErrHistoryClearNotFound) {
		t.Fatalf("expired undo=%v", err)
	}
	page, err := h.History(one.ID, 10, "")
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("expired clear restored history: %+v %v", page, err)
	}
}

func TestClearHistoryReadFailureDoesNotCreateClear(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := h.CreateProfile("One", "")
	if _, err := db.Exec("DROP TABLE viewing_events"); err != nil {
		t.Fatal(err)
	}
	if clear, err := h.ClearHistory(p.ID); err == nil || clear.ID != "" {
		t.Fatalf("clear succeeded despite failed cutoff read: %+v %v", clear, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM viewing_history_clears").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("created %d clears", count)
	}
}

func TestCompletionSurvivesCatalogRemovalAndRewatch(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := h.CreateProfile("One", "")
	add := func() {
		t.Helper()
		if _, err := db.Exec("INSERT INTO catalog_items(id,kind,title,relative_path) VALUES('film','film','Film','Film.mkv')"); err != nil {
			t.Fatal(err)
		}
	}
	complete := func() {
		t.Helper()
		generation, _, err := h.BeginPlayback(p.ID, "film")
		if err != nil {
			t.Fatal(err)
		}
		event := &household.ViewingEvent{ID: "caller-id", CatalogID: "film", Title: "Film", Kind: "film", RecordedAt: -99}
		for observation := int64(1); observation <= 2; observation++ {
			accepted, err := h.RecordPlaybackProgress(p.ID, "film", 100, generation, observation, true, event)
			if err != nil || !accepted {
				t.Fatalf("completion=%v %v", accepted, err)
			}
		}
	}
	add()
	complete()
	if _, err := db.Exec("DELETE FROM catalog_items WHERE id='film'"); err != nil {
		t.Fatal(err)
	}
	page, err := h.History(p.ID, 10, "")
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("missing media history: %+v %v", page, err)
	}
	add()
	complete()
	complete()
	page, err = h.History(p.ID, 10, "")
	if err != nil || len(page.Events) != 3 {
		t.Fatalf("rewatch history: %+v %v", page, err)
	}
	for _, event := range page.Events {
		if event.ID == "caller-id" || event.RecordedAt <= 0 {
			t.Fatalf("caller controlled identity/time: %+v", event)
		}
	}
}
