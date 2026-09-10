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
	if _, err = db.Exec(`CREATE TABLE preserved(value TEXT NOT NULL); INSERT INTO preserved VALUES('before')`); err != nil {
		t.Fatal(err)
	}

	failing := fstest.MapFS{
		"migrations/001_first.sql": {Data: []byte(`CREATE TABLE first_migration(value TEXT NOT NULL);`)},
		"migrations/002_fail.sql":  {Data: []byte(`CREATE TABLE partial_migration(value TEXT NOT NULL); INSERT INTO missing_table VALUES('fail');`)},
	}
	if err = migrateFS(context.Background(), db, failing); err == nil {
		t.Fatal("invalid migration succeeded")
	}
	assertMigrationState(t, db, false)

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
	if _, err = db.Exec(`CREATE TABLE preserved(value TEXT NOT NULL); INSERT INTO preserved VALUES('before')`); err != nil {
		t.Fatal(err)
	}

	source := fstest.MapFS{
		"migrations/001_first.sql":     {Data: []byte(`CREATE TABLE first_migration(value TEXT NOT NULL);`)},
		"migrations/002_cancelled.sql": {Data: []byte(`CREATE TABLE cancelled_migration(value TEXT NOT NULL);`)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = migrateFS(ctx, db, cancelReadFS{FS: source, name: "migrations/002_cancelled.sql", cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("migration cancellation error = %v, want context cancellation", err)
	}
	assertMigrationState(t, db, false)

	if err = migrateFS(context.Background(), db, source); err != nil {
		t.Fatalf("restart after cancelled migration: %v", err)
	}
	var applied int
	if err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 2 {
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

func assertMigrationState(t *testing.T, db *sql.DB, migrationTables bool) {
	t.Helper()
	var value string
	if err := db.QueryRow(`SELECT value FROM preserved`).Scan(&value); err != nil || value != "before" {
		t.Fatalf("preserved value after rollback = %q, %v", value, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('schema_migrations','first_migration','partial_migration','cancelled_migration')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	want := 0
	if migrationTables {
		want = 1
	}
	if count != want {
		t.Fatalf("migration tables after rollback = %d, want %d", count, want)
	}
}
