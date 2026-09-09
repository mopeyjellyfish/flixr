package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestRestoreIntoEmptyInstallationRecoversHouseholdSettingsLocksHistoryAndArtwork(t *testing.T) {
	archive := durableTestArchive(t)
	target := t.TempDir()
	result, err := Restore(context.Background(), archive, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetyBackup != "" {
		t.Fatalf("empty restore created safety backup %q", result.SafetyBackup)
	}
	db, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if profiles := house.Profiles(); len(profiles) != 1 || profiles[0].Name != "Backup viewer" {
		t.Fatalf("restored profiles = %+v", profiles)
	}
	var history, locked int
	var setting, accessLibraries, maxRating string
	if err := db.QueryRow(`SELECT COUNT(*) FROM viewing_events`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT locked FROM catalog_metadata_fields WHERE catalog_id='film' AND field='title'`).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='backup-proof'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT library_ids_json,max_rating FROM profile_access_policies LIMIT 1`).Scan(&accessLibraries, &maxRating); err != nil {
		t.Fatal(err)
	}
	if history != 1 || locked != 1 || setting != "durable" || accessLibraries != `["films"]` || maxRating != "PG" {
		t.Fatalf("restored history=%d locked=%d setting=%q", history, locked, setting)
	}
	artwork, err := os.ReadFile(filepath.Join(target, "artwork", "objects", "kept-image"))
	if err != nil || string(artwork) != "kept artwork" {
		t.Fatalf("restored artwork = %q, %v", artwork, err)
	}
	wantSegment := filepath.Join(target, "segments")
	segment, err := os.ReadFile(filepath.Join(target, ".playback-segment-dir"))
	if err != nil || string(segment) != wantSegment+"\n" {
		t.Fatalf("restored segment sidecar = %q, %v", segment, err)
	}
}

func TestRestoreFailureLeavesExistingInstallationAndVerifiedSafetyBackup(t *testing.T) {
	archive := durableTestArchive(t)
	target := t.TempDir()
	existing, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := existing.Exec(`INSERT INTO settings(key,value) VALUES('existing-proof','unchanged')`); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = restore(context.Background(), archive, target, restoreOptions{interruptAfterPublish: true})
	if !errors.Is(err, errSimulatedRestoreInterruption) {
		t.Fatalf("restore interruption = %v", err)
	}
	if err := RecoverInterruptedRestore(target); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var value string
	if err := reopened.QueryRow(`SELECT value FROM settings WHERE key='existing-proof'`).Scan(&value); err != nil || value != "unchanged" {
		t.Fatalf("existing target after recovery = %q, %v", value, err)
	}
	safety, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".flixr-restore-safety-*", "*"+archiveSuffix))
	if err != nil || len(safety) != 1 {
		t.Fatalf("safety backups = %v, %v", safety, err)
	}
	if _, err := Verify(context.Background(), safety[0]); err != nil {
		t.Fatalf("safety backup is invalid: %v", err)
	}
}

func TestRestoreRecoversInterruptionBetweenManagedEntries(t *testing.T) {
	archive := durableTestArchive(t)
	target := t.TempDir()
	db, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO settings(key,value) VALUES('prior','kept')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(target, "artwork", "objects")
	if err = os.MkdirAll(art, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(art, "prior"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = restore(context.Background(), archive, target, restoreOptions{interruptAfterMoves: 1}); !errors.Is(err, errSimulatedRestoreInterruption) {
		t.Fatalf("restore err=%v", err)
	}
	if err = RecoverInterruptedRestore(target); err != nil {
		t.Fatal(err)
	}
	prior, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	var value string
	if err = prior.QueryRow(`SELECT value FROM settings WHERE key='prior'`).Scan(&value); err != nil || value != "kept" {
		t.Fatalf("prior=%q err=%v", value, err)
	}
	if data, err := os.ReadFile(filepath.Join(art, "prior")); err != nil || string(data) != "kept" {
		t.Fatalf("art=%q err=%v", data, err)
	}
}

func TestRestoreRejectsCorruptArchiveBeforeTargetMutation(t *testing.T) {
	target := t.TempDir()
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "corrupt.flixr-backup")
	if err := os.WriteFile(archive, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), archive, target); err == nil {
		t.Fatal("corrupt archive was restored")
	}
	value, err := os.ReadFile(sentinel)
	if err != nil || string(value) != "unchanged" {
		t.Fatalf("target changed before validation: %q, %v", value, err)
	}
}

func TestRestoreRejectsInsufficientSpaceBeforeTargetMutation(t *testing.T) {
	archive := durableTestArchive(t)
	target := t.TempDir()
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restore(context.Background(), archive, target, restoreOptions{freeBytes: func(string) (int64, error) { return 0, nil }}); err == nil {
		t.Fatal("restore without space succeeded")
	}
	value, err := os.ReadFile(sentinel)
	if err != nil || string(value) != "unchanged" {
		t.Fatalf("target changed: %q %v", value, err)
	}
}

func TestRestoreRejectsOrphanedManagedDataWithoutSafetySnapshot(t *testing.T) {
	archive := durableTestArchive(t)
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "artwork"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), archive, target); err == nil {
		t.Fatal("orphaned managed data was overwritten")
	}
}

func TestRestoreInvalidatesBackedUpSessionsAndPreservesFreshLogin(t *testing.T) {
	data := t.TempDir()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := house.Claim(house.SetupToken(), "restore password")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("Restored profile", "1234")
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := house.Select(profile.ID, "1234")
	if err != nil {
		t.Fatal(err)
	}
	created, err := Create(context.Background(), Source{DB: db, DataDir: data, AppVersion: "test"}, Options{Destination: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, err = Restore(context.Background(), created.Path, target); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restored, err := household.Open(restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Owner(ownerToken) {
		t.Fatal("backed-up owner bearer token survived restore")
	}
	if _, ok := restored.Profile(viewerToken); ok {
		t.Fatal("backed-up viewer bearer token survived restore")
	}
	if freshOwner, err := restored.Login("restore password"); err != nil || !restored.Owner(freshOwner) {
		t.Fatalf("fresh owner login failed after restore: %v", err)
	}
	if freshViewer, err := restored.Select(profile.ID, "1234"); err != nil {
		t.Fatalf("fresh profile selection failed after restore: %v", err)
	} else if selected, ok := restored.Profile(freshViewer); !ok || selected.ID != profile.ID {
		t.Fatalf("fresh viewer session failed after restore: %+v %v", selected, ok)
	}
}

func TestRecoverInterruptedRestoreRejectsHostileJournalBeforeMutation(t *testing.T) {
	tests := []struct {
		name    string
		journal string
	}{
		{"dot stage", `{"phase":"prepared","stage":"."}`},
		{"parent stage", `{"phase":"prepared","stage":".."}`},
		{"absolute stage", `{"phase":"prepared","stage":"/tmp"}`},
		{"wrong stage prefix", `{"phase":"prepared","stage":"restore-stage-0123456789ab"}`},
		{"wrong stage id", `{"phase":"prepared","stage":".flixr-restore-stage-not-hex-value"}`},
		{"unknown phase", `{"phase":"removing","stage":".flixr-restore-stage-0123456789ab"}`},
		{"unknown field", `{"phase":"prepared","stage":".flixr-restore-stage-0123456789ab","path":".."}`},
		{"trailing json", `{"phase":"prepared","stage":".flixr-restore-stage-0123456789ab"}{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(parent, "outside-sentinel")
			if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, restoreJournalName), []byte(test.journal), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := RecoverInterruptedRestore(target); err == nil {
				t.Fatal("hostile journal was accepted")
			}
			if value, err := os.ReadFile(sentinel); err != nil || string(value) != "unchanged" {
				t.Fatalf("outside sentinel changed: %q, %v", value, err)
			}
		})
	}
}

func TestRecoverInterruptedRestoreRejectsSymlinkedStageBeforeMutation(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	external := filepath.Join(parent, "external")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(external, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	stageName := ".flixr-restore-stage-0123456789ab"
	if err := os.Symlink(external, filepath.Join(target, stageName)); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(restoreJournal{Phase: "prepared", Stage: stageName})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, restoreJournalName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverInterruptedRestore(target); err == nil {
		t.Fatal("symlinked restore stage was accepted")
	}
	if value, err := os.ReadFile(sentinel); err != nil || string(value) != "unchanged" {
		t.Fatalf("external sentinel changed: %q, %v", value, err)
	}
}

func durableTestArchive(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := house.Claim(house.SetupToken(), "backup test password"); err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("Backup viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := house.RecordViewingEvent(profile.ID, household.ViewingEvent{CatalogID: "film", Title: "Film", Kind: "film", Type: household.EventCompleted, Provenance: household.ProvenanceLocal}); err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(dataDir, "artwork", "objects")
	if err := os.MkdirAll(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, "kept-image"), []byte("kept artwork"), 0o600); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO catalog_artwork(catalog_id,kind,content_type,object_name) VALUES('film','poster','image/jpeg','kept-image')`,
		`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film','film','title','Owner title','owner',1)`,
		`INSERT INTO settings(key,value) VALUES('backup-proof','durable')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE profile_access_policies SET library_ids_json='["films"]',rating_region='GB',max_rating='PG',unrated_policy='deny',version=2 WHERE profile_id=?`, profile.ID); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	result, err := Create(context.Background(), Source{DB: db, DataDir: dataDir, AppVersion: "test"}, Options{Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Path
}
