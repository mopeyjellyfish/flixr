package main

import (
	"path/filepath"
	"testing"
)

func TestExclusiveLocksRejectContention(t *testing.T) {
	for _, name := range []string{"data", "segment"} {
		t.Run(name, func(t *testing.T) {
			lockPath := filepath.Join(t.TempDir(), map[string]string{"data": ".lock", "segment": ".flixr.lock"}[name])
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
				t.Fatal("second lock acquired")
			}
		})
	}
}

func TestWithinDataDirectory(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "var", "flixr")
	tests := []struct {
		name  string
		child string
		want  bool
	}{
		{"equal", root, true},
		{"nested", filepath.Join(root, "segments"), true},
		{"sibling", filepath.Join(filepath.Dir(root), "other"), false},
		{"parent", filepath.Dir(root), false},
		{"prefix sibling", root + "-other", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := within(root, test.child); got != test.want {
				t.Fatalf("within(%q, %q) = %v, want %v", root, test.child, got, test.want)
			}
		})
	}
}
