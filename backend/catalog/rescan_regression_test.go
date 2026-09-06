package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestFailedRescanKeepsCatalogRowAndProgress(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "film.mp4")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fail := false
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		if fail {
			return catalog.MediaProperties{}, errors.New("ffprobe failed")
		}
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, err := c.List("", 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("initial catalog = %#v, %v", items, err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.Select(profile.ID, "")
	if err != nil || h.Progress(session, items[0].ID, 123) != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatalf("partial rescan: %v", err)
	}
	if _, ok := c.Item(items[0].ID); !ok {
		t.Fatal("failed rescan discarded known catalog row")
	}
	position, err := h.Position(session, items[0].ID)
	if err != nil || position != 123 {
		t.Fatalf("progress after failed rescan = %d, %v", position, err)
	}
}

func TestUnchangedTenThousandPathsDoNotProbe(t *testing.T) {
	root := t.TempDir()
	for i := range 10_000 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%05d.mp4", i)), []byte(fmt.Sprintf("media-%d", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var probes atomic.Int32
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		probes.Add(1)
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(context.Background(), 8) != nil || c.Scan(context.Background(), 8) != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := probes.Load(); got != 10_000 {
		t.Fatalf("unchanged paths caused %d probes, want 10000", got)
	}
}

func TestUnavailableRootCannotEraseCatalog(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "mounted-media")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	before, err := c.List("", 0, 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("initial catalog: %+v, %v", before, err)
	}
	if err := os.Rename(root, filepath.Join(parent, "disconnected")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err == nil {
		t.Fatal("unavailable mount was accepted as empty")
	}
	after, err := c.List("", 0, 10)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("catalog lost: %+v, %v", after, err)
	}
}
