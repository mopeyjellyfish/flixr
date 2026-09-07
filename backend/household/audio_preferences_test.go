package household_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestAudioLanguagePreferenceSurvivesUpgradeAndRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("Ada", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE catalog_audio_sidecars"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE profile_audio_preferences"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version=14"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sqlite.Open(filepath.Clean(dir))
	if err != nil {
		t.Fatal(err)
	}
	house, err = household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := house.SaveAudioLanguage(profile.ID, " FRA "); err != nil {
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
	house, err = household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := house.AudioLanguage(profile.ID); err != nil || got != "fra" {
		t.Fatalf("audio language = %q, %v", got, err)
	}
	if err := house.SaveAudioLanguage("missing", "eng"); !errors.Is(err, household.ErrProfileNotFound) {
		t.Fatalf("missing profile error = %v", err)
	}
}
