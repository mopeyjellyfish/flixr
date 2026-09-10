package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestMigrationFailureRollsBackAndCanRestart(t *testing.T) {
	db, err := sql.Open("sqlite", databaseDSN(t.TempDir()+"/flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY); INSERT INTO schema_migrations VALUES(1); CREATE TABLE preserved(value TEXT NOT NULL); INSERT INTO preserved VALUES('before')`); err != nil {
		t.Fatal(err)
	}

	failing := fstest.MapFS{
		"migrations/001_first.sql": {Data: []byte(`CREATE TABLE first_migration(value TEXT NOT NULL);`)},
		"migrations/002_fail.sql":  {Data: []byte(`CREATE TABLE partial_migration(value TEXT NOT NULL); INSERT INTO missing_table VALUES('fail');`)},
	}
	if err = migrateFS(context.Background(), db, failing); err == nil {
		t.Fatal("invalid migration succeeded")
	}
	assertMigrationState(t, db, 1)

	corrected := fstest.MapFS{
		"migrations/001_first.sql":  {Data: []byte(`CREATE TABLE first_migration(value TEXT NOT NULL);`)},
		"migrations/002_second.sql": {Data: []byte(`CREATE TABLE partial_migration(value TEXT NOT NULL);`)},
	}
	if err = migrateFS(context.Background(), db, corrected); err != nil {
		t.Fatalf("restart after failed migration: %v", err)
	}
	var applied int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 2 {
		t.Fatalf("applied migrations after restart = %d, %v", applied, err)
	}
}

func TestMigrationCancellationRollsBackAndCanRestart(t *testing.T) {
	path := t.TempDir() + "/flixr.db"
	db, err := sql.Open("sqlite", databaseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY); INSERT INTO schema_migrations VALUES(1); CREATE TABLE preserved(value TEXT NOT NULL); INSERT INTO preserved VALUES('before')`); err != nil {
		t.Fatal(err)
	}

	source := fstest.MapFS{
		"migrations/001_existing.sql":  {Data: []byte(`SELECT 1;`)},
		"migrations/002_first.sql":     {Data: []byte(`CREATE TABLE first_migration(value TEXT NOT NULL);`)},
		"migrations/003_cancelled.sql": {Data: []byte(`CREATE TABLE cancelled_migration(value TEXT NOT NULL);`)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = migrateFS(ctx, db, cancelReadFS{FS: source, name: "migrations/003_cancelled.sql", cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("migration cancellation error = %v, want context cancellation", err)
	}
	assertMigrationState(t, db, 1)

	if err = migrateFS(context.Background(), db, source); err != nil {
		t.Fatalf("restart after cancelled migration: %v", err)
	}
	var applied int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 3 {
		t.Fatalf("applied migrations after restart = %d, %v", applied, err)
	}
}

type cancelReadFS struct {
	fs.FS
	name   string
	cancel context.CancelFunc
}

func (f cancelReadFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err == nil && name == f.name {
		f.cancel()
	}
	return file, err
}

func assertMigrationState(t *testing.T, db *sql.DB, applied int) {
	t.Helper()
	var value string
	if err := db.QueryRow(`SELECT value FROM preserved`).Scan(&value); err != nil || value != "before" {
		t.Fatalf("preserved value after rollback = %q, %v", value, err)
	}
	var partialTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('first_migration','partial_migration','cancelled_migration')`).Scan(&partialTables); err != nil {
		t.Fatal(err)
	}
	if partialTables != 0 {
		t.Fatalf("partial migration tables after rollback = %d", partialTables)
	}
	var gotApplied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&gotApplied); err != nil || gotApplied != applied {
		t.Fatalf("applied migrations after rollback = %d, %v; want %d", gotApplied, err, applied)
	}
}
