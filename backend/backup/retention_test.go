package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateKeepsNewestVerifiedBackupAndUnrelatedFiles(t *testing.T) {
	destination := t.TempDir()
	os.WriteFile(filepath.Join(destination, ownerMarker), nil, 0o600)
	old := seedBackup(t, destination)
	time.Sleep(10 * time.Millisecond)
	newest := seedBackup(t, destination)
	unrelated := filepath.Join(destination, "notes.txt")
	os.WriteFile(unrelated, []byte("keep"), 0o600)
	corrupt := filepath.Join(destination, "flixr-backup-corrupt"+archiveSuffix)
	os.WriteFile(corrupt, []byte("bad"), 0o600)

	if err := Rotate(context.Background(), destination, Retention{Count: 1, Age: time.Hour, BudgetBytes: 1 << 30}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatalf("old backup remains: %v", err)
	}
	for _, path := range []string{newest.Path, unrelated, corrupt} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s retained: %v", filepath.Base(path), err)
		}
	}
}

func TestRotateNeverDeletesFinalVerifiedBackup(t *testing.T) {
	destination := t.TempDir()
	result := seedBackup(t, destination)
	if err := Rotate(context.Background(), destination, Retention{Count: 1, Age: time.Nanosecond, BudgetBytes: 1}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("final backup removed: %v", err)
	}
}

func seedBackup(t *testing.T, destination string) Result {
	t.Helper()
	source := durableTestArchive(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	name := "flixr-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z") + archiveSuffix
	path := filepath.Join(destination, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, ownerMarker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return Result{Path: path, Size: int64(len(data))}
}
