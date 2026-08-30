package household_test

import (
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestOwnerAndProfilesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	token := h.SetupToken()
	if _, err := h.Claim(token, "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	p, err := h.CreateProfile("Ada", "1234")
	if err != nil {
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
	if !h.Claimed() {
		t.Fatal("owner was not persisted")
	}
	if _, err := h.Login("correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Select(p.ID, "1234"); err != nil {
		t.Fatal(err)
	}
}
