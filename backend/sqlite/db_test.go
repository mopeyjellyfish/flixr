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
