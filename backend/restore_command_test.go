package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/backup"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestRestoreBackupCommandRestoresVerifiedArchiveOffline(t *testing.T) {
	source := t.TempDir()
	db, err := sqlite.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO settings(key,value) VALUES('drill','passed')`); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	result, err := backup.Create(context.Background(), backup.Source{DB: db, DataDir: source, AppVersion: "test"}, backup.Options{Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	var output bytes.Buffer
	if err = restoreBackup([]string{"--data-dir", target, "--archive", result.Path}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Backup restored and verified.\n" {
		t.Fatalf("output=%q", output.String())
	}
	restored, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err = restored.QueryRow(`SELECT value FROM settings WHERE key='drill'`).Scan(&value); err != nil || value != "passed" {
		t.Fatalf("drill=%q err=%v", value, err)
	}
}

func TestRestoreBackupCommandRejectsLiveTarget(t *testing.T) {
	target := t.TempDir()
	archive := filepath.Join(t.TempDir(), "bad.flixr-backup")
	_ = os.WriteFile(archive, []byte("bad"), 0o600)
	unlock, ok, err := acquireDataLock(filepath.Join(target, ".lock"))
	if err != nil || !ok {
		t.Fatalf("lock=%v,%v", ok, err)
	}
	defer unlock()
	if err := restoreBackup([]string{"--data-dir", target, "--archive", archive}, &bytes.Buffer{}); err == nil {
		t.Fatal("live target accepted")
	}
}
