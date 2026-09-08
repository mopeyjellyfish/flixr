package household_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestSubtitlePreferenceSurvivesUpgradeRestartAndExplicitOff(t *testing.T) {
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
	if _, err := db.Exec("DROP TABLE catalog_subtitle_sidecars"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE profile_subtitle_preferences"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version=23"); err != nil {
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
	want := household.SubtitlePreference{Mode: household.SubtitleOff, Language: "fra", PreferSDH: true}
	if err := house.SaveSubtitlePreference(profile.ID, want); err != nil {
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
	if got, err := house.SubtitlePreference(profile.ID); err != nil || got != want {
		t.Fatalf("subtitle preference = %#v, %v", got, err)
	}
	if err := house.SaveSubtitlePreference(profile.ID, household.SubtitlePreference{Mode: "invalid"}); !errors.Is(err, household.ErrInvalidSubtitlePreference) {
		t.Fatalf("invalid mode error = %v", err)
	}
	if err := house.SaveSubtitlePreference("missing", want); !errors.Is(err, household.ErrProfileNotFound) {
		t.Fatalf("missing profile error = %v", err)
	}
}
