package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOnlineBackupProducesConsistentReadableSnapshot(t *testing.T) {
	source, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Exec(`INSERT INTO settings(key,value) VALUES('backup-test','before')`); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err := source.OnlineBackup(context.Background(), destination); err != nil {
		t.Fatal(err)
	}

	snapshot, err := sql.Open("sqlite", databaseDSN(destination))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var value, integrity string
	if err := snapshot.QueryRow(`SELECT value FROM settings WHERE key='backup-test'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if value != "before" || integrity != "ok" {
		t.Fatalf("snapshot value=%q integrity=%q", value, integrity)
	}
}

func TestSchemaVersionReportsLatestAppliedMigration(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := db.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 29 {
		t.Fatalf("schema version = %d, want 29", version)
	}
}

func TestOnlineBackupHonorsCancelledContext(t *testing.T) {
	source, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err = source.OnlineBackup(ctx, destination); err == nil {
		t.Fatal("cancelled backup succeeded")
	}
}
