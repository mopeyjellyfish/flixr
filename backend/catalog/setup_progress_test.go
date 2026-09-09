package catalog_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestSetupProgressPersistsAndRejectsUnknownSteps(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.SetupProgress(); err != nil || got != "" {
		t.Fatalf("new setup progress = %q, %v", got, err)
	}
	if err := c.SetSetupProgress(catalog.SetupProgressProfile); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err = catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.SetupProgress(); err != nil || got != catalog.SetupProgressProfile {
		t.Fatalf("reopened setup progress = %q, %v", got, err)
	}
	if err := c.SetSetupProgress("credential-from-form"); err == nil {
		t.Fatal("unknown setup progress was persisted")
	}
	if got, err := c.SetupProgress(); err != nil || got != catalog.SetupProgressProfile {
		t.Fatalf("invalid update changed setup progress = %q, %v", got, err)
	}
}
