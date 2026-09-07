package household_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestEndedRealMediaPersistsPerProfileAcrossReopen(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	source := filepath.Join("..", "testdata", "media", "films", "Blue Horizon 2026.mp4")
	bytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "Film.mp4"), bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	library, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = library.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err = library.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, _, err := library.Browse("", 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("real media scan: %#v %v", items, err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	one, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	two, err := h.CreateProfile("Two", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.RecordProgress(one.ID, items[0].ID, 2_000, 100, true); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err = household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	oneSession, err := h.Select(one.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	twoSession, err := h.Select(two.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if position, err := h.Position(oneSession, items[0].ID); err != nil || position != 0 {
		t.Fatalf("completed resume = %d, %v", position, err)
	}
	if position, err := h.Position(twoSession, items[0].ID); err != nil || position != 0 {
		t.Fatalf("other profile resume = %d, %v", position, err)
	}
}
