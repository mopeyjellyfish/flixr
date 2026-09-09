package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestNamedLibraryMigrationPreservesLegacyRootsSourcesAndReview(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version > 24 {
			continue
		}
		body, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = legacy.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
		if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec(`INSERT INTO settings(key,value) VALUES('film_root','/media/films'),('tv_root','/media/tv')`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,fingerprint,primary_file_id,available) VALUES('film-1','film','Film','Film.mp4','film','fp','physical-1',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO profiles(id,name) VALUES('profile-1','One'); INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES('profile-1','film-1',123)`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO catalog_physical_files(id,catalog_id,root_kind,relative_path,fingerprint,present,selected) VALUES('physical-1','film-1','film','Film.mp4','fp',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO library_locations(root_kind,root_path,state,scan_complete,item_count,missing_count,pending_scan_id,last_scan_id,updated_at,message) VALUES('film','/media/films','review_required',0,0,1,'scan-1','scan-1',42,'review')`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(`INSERT INTO library_removal_candidates(root_kind,scan_id,physical_file_id,catalog_id) VALUES('film','scan-1','physical-1','film-1')`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var libraryID, locationID, name, kind, root, state, pending string
	if err = db.QueryRow(`SELECT l.id,x.id,l.name,l.kind,x.root_path,x.state,x.pending_scan_id FROM libraries l JOIN library_locations x ON x.library_id=l.id WHERE l.id='films'`).Scan(&libraryID, &locationID, &name, &kind, &root, &state, &pending); err != nil {
		t.Fatal(err)
	}
	if libraryID != "films" || locationID != "films-root" || name != "Films" || kind != "film" || root != "/media/films" || state != "review_required" || pending != "scan-1" {
		t.Fatalf("migrated library = %q %q %q %q %q %q %q", libraryID, locationID, name, kind, root, state, pending)
	}
	var sourceLocation, itemLocation, candidateLocation string
	if err = db.QueryRow(`SELECT location_id FROM catalog_physical_files WHERE id='physical-1'`).Scan(&sourceLocation); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT source_location_id FROM catalog_items WHERE id='film-1'`).Scan(&itemLocation); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT location_id FROM library_removal_candidates WHERE scan_id='scan-1'`).Scan(&candidateLocation); err != nil {
		t.Fatal(err)
	}
	if sourceLocation != "films-root" || itemLocation != "films-root" || candidateLocation != "films-root" {
		t.Fatalf("migrated source keys = %q %q %q", sourceLocation, itemLocation, candidateLocation)
	}
	var position int64
	if err = db.QueryRow(`SELECT position_ms FROM progress WHERE profile_id='profile-1' AND catalog_id='film-1'`).Scan(&position); err != nil || position != 123 {
		t.Fatalf("migrated profile progress = %d, %v", position, err)
	}
}

func TestNamedLibraryMigrationRetainsEqualLegacyRootsForTopologyReview(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", databaseDSN(filepath.Join(dir, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version > 25 {
			continue
		}
		body, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = legacy.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
		if _, err = legacy.Exec("INSERT INTO schema_migrations VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec(`INSERT INTO settings(key,value) VALUES('film_root','/media/shared'),('tv_root','/media/shared')`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("equal legacy roots bricked migration: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,root_path FROM library_locations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			t.Fatal(err)
		}
		got[id] = root
	}
	if got["films-root"] != "/media/shared" || got["tv-root"] != "/media/shared" {
		t.Fatalf("legacy roots not retained: %#v", got)
	}
}
