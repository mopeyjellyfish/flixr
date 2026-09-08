package sqlite_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestConnectionPolicyAppliesToEveryPoolConnectionAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.WriterMaxOpenConns() != 1 || db.ReaderMaxOpenConns() != 4 {
		t.Fatalf("pool limits = %d/%d", db.WriterMaxOpenConns(), db.ReaderMaxOpenConns())
	}
	for _, conn := range []struct {
		name string
		db   *sql.DB
	}{{"writer", db.Writer()}, {"reader", db.Reader()}} {
		for range 2 {
			c, err := conn.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var foreignKeys, timeout, autoCheckpoint int
			var journal string
			if err := c.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&journal); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(context.Background(), "PRAGMA wal_autocheckpoint").Scan(&autoCheckpoint); err != nil {
				t.Fatal(err)
			}
			c.Close()
			if foreignKeys != 1 || timeout != 5000 || journal != "wal" || autoCheckpoint != 1000 {
				t.Fatalf("%s policy = foreign_keys=%d busy_timeout=%d journal=%q wal_autocheckpoint=%d", conn.name, foreignKeys, timeout, journal, autoCheckpoint)
			}
		}
	}
	if _, err := db.Exec("INSERT INTO profiles(id,name) VALUES('p','P')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO progress(profile_id,catalog_id,position_ms) VALUES('missing','missing',1)"); err == nil {
		t.Fatal("foreign key violation succeeded")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(dir); err != nil {
		t.Fatal(err)
	}
}

func TestExistingDatabaseReceivesCatalogMetadataFieldsMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE catalog_metadata_fields"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP INDEX catalog_artwork_object"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE catalog_artwork DROP COLUMN object_name"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version=12"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film','f','tags','family','local',1)"); err != nil {
		t.Fatalf("migrated metadata table: %v", err)
	}
}

func TestLibraryLocationSafetySchemaIsInstalled(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO library_locations(id,library_id,root_path,state,scan_complete,item_count,missing_count,pending_scan_id,updated_at,message) VALUES('location-1','films','/media/films','review_required',0,0,1,'scan-1',1,'review removals')`); err != nil {
		t.Fatalf("insert library location: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,root_kind) VALUES('catalog-1','film','Film','film.mp4','film'); INSERT INTO catalog_physical_files(id,catalog_id,location_id,root_kind,relative_path,fingerprint) VALUES('physical-1','catalog-1','location-1','film','film.mp4','fingerprint')`); err != nil {
		t.Fatalf("insert catalog source: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO library_removal_candidates(location_id,scan_id,physical_file_id,catalog_id) VALUES('location-1','scan-1','physical-1','catalog-1')`); err != nil {
		t.Fatalf("insert removal candidate: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM catalog_physical_files WHERE id='physical-1'`); err != nil {
		t.Fatal(err)
	}
	var candidates int
	if err := db.QueryRow(`SELECT COUNT(*) FROM library_removal_candidates`).Scan(&candidates); err != nil || candidates != 0 {
		t.Fatalf("orphaned removal candidates = %d, %v", candidates, err)
	}
}
