package main

import (
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/config"
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

func TestConfigureMetadataAppliesApplicationOverrideAndDisablePrecedence(t *testing.T) {
	old := applicationTMDBToken
	applicationTMDBToken = "application-token"
	t.Cleanup(func() { applicationTMDBToken = old })

	c := catalog.New()
	disabled := false
	override := "owner-token"
	if err := configureMetadata(c, config.Environment{TMDBToken: &override, MetadataEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	status := c.MetadataStatus()
	if status.State != "disabled" || status.Configured {
		t.Fatalf("disabled status = %+v", status)
	}
	if err := c.SetMetadataEnabled(true); err != nil {
		t.Fatal(err)
	}
	if status = c.MetadataStatus(); status.Source != "owner" {
		t.Fatalf("override status = %+v", status)
	}
	if err := c.SetTMDBToken(""); err != nil {
		t.Fatal(err)
	}
	if status = c.MetadataStatus(); status.Source != "application" {
		t.Fatalf("application fallback = %+v", status)
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
