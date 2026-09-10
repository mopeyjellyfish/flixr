package sqlite_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestUpgradeFromV0152PreservesHouseholdState(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "v0.15.2", "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "flixr.db"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	for attempt := range 2 {
		db, openErr := sqlite.Open(dir)
		if openErr != nil {
			t.Fatalf("open attempt %d: %v", attempt+1, openErr)
		}
		assertV0152HouseholdState(t, db)
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

func assertV0152HouseholdState(t *testing.T, db *sqlite.DB) {
	t.Helper()
	version, err := db.SchemaVersion()
	if err != nil || version != sqlite.LatestSchemaVersion {
		t.Fatalf("upgraded schema version = %d, %v", version, err)
	}
	assertRow(t, db, `SELECT name,hex(pin_hash),hex(salt),attempts,locked_until FROM profiles WHERE id='profile-upgrade'`, "Upgrade Viewer", "11121314", "15161718", 2, 123456789)
	assertRow(t, db, `SELECT hex(password_hash),hex(salt) FROM owner WHERE id=1`, "01020304", "05060708")
	assertRow(t, db, `SELECT subject,expires_at,revoked FROM sessions WHERE token_hash=x'21222324'`, "profile:profile-upgrade", 2000000000, 0)
	assertRow(t, db, `SELECT view_mode,sort_mode FROM profile_view_preferences WHERE profile_id='profile-upgrade' AND media='all'`, "grid", "year")
	assertRow(t, db, `SELECT value FROM settings WHERE key='metadata_language'`, "ja-JP")
	assertRow(t, db, `SELECT value FROM settings WHERE key='metadata_region'`, "JP")
	assertRow(t, db, `SELECT added_at FROM profile_film_list WHERE profile_id='profile-upgrade' AND catalog_id='film-upgrade'`, 1700000000)
	assertRow(t, db, `SELECT language FROM profile_audio_preferences WHERE profile_id='profile-upgrade'`, "jpn")
	assertRow(t, db, `SELECT mode,language,prefer_sdh FROM profile_subtitle_preferences WHERE profile_id='profile-upgrade'`, "automatic", "eng", 1)
	assertRow(t, db, `SELECT position_ms,updated_at,generation,observation FROM progress WHERE profile_id='profile-upgrade' AND catalog_id='film-upgrade'`, 3456000, 1700000100, 7, 12)
	assertRow(t, db, `SELECT rating,provenance,updated_at FROM profile_ratings WHERE profile_id='profile-upgrade' AND catalog_id='film-upgrade'`, 5, "local", 1700000200)
	assertRow(t, db, `SELECT title,event_type,recorded_at FROM viewing_events WHERE event_id='event-upgrade'`, "Preserved Film", "summary", 1700000300)
	assertRow(t, db, `SELECT group_concat(id,',') FROM (SELECT id FROM libraries ORDER BY created_at,name COLLATE NOCASE,id)`, "films,archive,tv")
	assertRow(t, db, `SELECT library_ids_json,unrated_policy,version FROM profile_access_policies WHERE profile_id='profile-upgrade'`, "[]", "allow", 1)
	assertRow(t, db, `SELECT last_status,retain_count FROM backup_policy WHERE id=1`, "never", 7)
}

func assertRow(t *testing.T, db *sqlite.DB, query string, want ...any) {
	t.Helper()
	got := make([]any, len(want))
	targets := make([]any, len(want))
	for index := range got {
		targets[index] = &got[index]
	}
	if err := db.QueryRow(query).Scan(targets...); err != nil {
		t.Fatal(err)
	}
	for index := range want {
		if value, ok := got[index].(int64); ok {
			got[index] = int(value)
		}
		if got[index] != want[index] {
			t.Fatalf("query %q column %d = %#v, want %#v", query, index, got[index], want[index])
		}
	}
}
