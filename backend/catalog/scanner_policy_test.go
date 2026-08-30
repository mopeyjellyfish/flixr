package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func TestUnchangedScanSkipsProbeAndNestedSymlinkCannotOpen(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.mp4"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		probes.Add(1)
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("unchanged scan probed %d times, want 1", got)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(root, "nested-link.mp4")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("outside symlink entered catalog: %#v, %v", items, err)
	}
	contained, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer contained.Close()
	if _, err := contained.Open("nested-link.mp4"); err == nil {
		t.Fatal("os.Root opened nested symlink outside root")
	}
}

func TestScanCancellationLeavesLastCatalog(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.mp4"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(ctx context.Context, _ *os.File) (catalog.MediaProperties, error) {
		close(started)
		<-ctx.Done()
		return catalog.MediaProperties{}, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.StartScan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := c.ScanStatus(); status.Status != "failed" {
		t.Fatalf("status = %#v", status)
	}
	_ = time.Second // documents this proof uses cancellation, not a timing sleep.
}
