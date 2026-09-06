package main

import (
	"path/filepath"
	"testing"
)

func TestDataLockRejectsContention(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".lock")
	unlock, ok, err := acquireDataLock(lockPath)
	if err != nil || !ok {
		t.Fatalf("first lock ok=%v err=%v", ok, err)
	}
	defer unlock()
	otherUnlock, otherOK, err := acquireDataLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if otherOK {
		otherUnlock()
		t.Fatal("second data lock acquired")
	}
}
