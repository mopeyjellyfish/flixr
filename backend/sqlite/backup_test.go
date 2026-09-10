package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	if version != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, LatestSchemaVersion)
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

func TestOnlineBackupRemainsConsistentDuringConcurrentWrite(t *testing.T) {
	data := t.TempDir()
	source, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	tx, err := source.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES('concurrent-a','before'),('concurrent-b','before')`); err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x", 4096)
	for index := range 300 {
		if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, "backup-padding-"+fmt.Sprint(index), value); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "snapshot.db")
	stepReached := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- source.onlineBackup(ctx, destination, func(more bool) {
			if more {
				once.Do(func() {
					close(stepReached)
					<-resume
				})
			}
		})
	}()
	select {
	case <-stepReached:
	case err = <-done:
		t.Fatalf("backup completed before concurrent write: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	resumed := false
	defer func() {
		if !resumed {
			close(resume)
		}
	}()
	concurrent, err := sql.Open("sqlite", databaseDSN(filepath.Join(data, "flixr.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = concurrent.Exec(`UPDATE settings SET value='after' WHERE key IN ('concurrent-a','concurrent-b')`); err != nil {
		concurrent.Close()
		t.Fatal(err)
	}
	if err = concurrent.Close(); err != nil {
		t.Fatal(err)
	}
	close(resume)
	resumed = true
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	snapshot, err := sql.Open("sqlite", databaseDSN(destination))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var a, b, integrity string
	if err = snapshot.QueryRow(`SELECT value FROM settings WHERE key='concurrent-a'`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = snapshot.QueryRow(`SELECT value FROM settings WHERE key='concurrent-b'`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if err = snapshot.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if a != b || (a != "before" && a != "after") || integrity != "ok" {
		t.Fatalf("snapshot concurrent values=%q/%q integrity=%q", a, b, integrity)
	}
}
