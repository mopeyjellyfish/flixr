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
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
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
	locations, err := c.LibraryLocations()
	if err != nil || len(locations) != 1 || locations[0].State != "unavailable" || locations[0].ScanComplete || locations[0].LastScanID != c.ScanStatus().ID {
		t.Fatalf("unavailable location state = %#v, %v", locations, err)
	}
	after, err := c.List("", 0, 10)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("catalog lost: %+v, %v", after, err)
	}
}

func TestCancelledScanKeepsLastCompleteLocationAndCatalog(t *testing.T) {
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
	var block atomic.Bool
	started := make(chan struct{})
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(ctx context.Context, _ *os.File) (catalog.MediaProperties, error) {
		if block.Load() {
			close(started)
			<-ctx.Done()
			return catalog.MediaProperties{}, ctx.Err()
		}
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	locations, err := c.LibraryLocations()
	if err != nil || len(locations) != 1 || !locations[0].ScanComplete {
		t.Fatalf("initial location state = %#v, %v", locations, err)
	}
	lastComplete := locations[0].LastScanID
	if err := os.WriteFile(path, []byte("changed media bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	block.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	if err := c.StartScan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	if err := c.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	locations, err = c.LibraryLocations()
	if err != nil || len(locations) != 1 || !locations[0].ScanComplete || locations[0].State != "available" || locations[0].LastScanID != lastComplete {
		t.Fatalf("location after cancellation = %#v, %v", locations, err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 || !items[0].Playable {
		t.Fatalf("catalog after cancellation = %#v, %v", items, err)
	}
}

func TestEmptyMountedRootRequiresReviewWithoutChangingCatalog(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	paths := []string{filepath.Join(root, "film.mp4"), filepath.Join(root, "second.mp4")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	before, err := c.List("", 0, 10)
	if err != nil || len(before) != 2 {
		t.Fatalf("initial catalog: %#v, %v", before, err)
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
	if err != nil || h.Progress(session, before[0].ID, 123) != nil {
		t.Fatalf("save progress: %v", err)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Scan(t.Context(), 1); err == nil {
		t.Fatal("empty mounted root was accepted as a trusted deletion")
	}
	var state, pending string
	var complete, missing, candidates int
	if err := db.QueryRow(`SELECT state,scan_complete,missing_count,pending_scan_id FROM library_locations WHERE id='films-root'`).Scan(&state, &complete, &missing, &pending); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM library_removal_candidates WHERE location_id='films-root' AND scan_id=?`, pending).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if state != "review_required" || complete != 0 || missing != 2 || pending == "" || candidates != 2 {
		t.Fatalf("empty-root review = state:%q complete:%d missing:%d pending:%q candidates:%d", state, complete, missing, pending, candidates)
	}
	after, err := c.List("", 0, 10)
	if err != nil || len(after) != 2 || after[0].ID != before[0].ID || !after[0].Playable || !after[1].Playable {
		t.Fatalf("catalog changed before owner review: %#v, %v", after, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	restarted, ok := reopened.Item(before[0].ID)
	if !ok || !restarted.Playable {
		t.Fatalf("restart changed catalog before owner review: %#v, exists=%v", restarted, ok)
	}
	locations, err := reopened.LibraryLocations()
	if err != nil || len(locations) != 1 || locations[0].State != "review_required" || locations[0].PendingScanID != pending || locations[0].Missing != 2 {
		t.Fatalf("restarted location review = %#v, %v", locations, err)
	}
	if err := reopened.ConfirmRemovals(t.Context(), pending, "film"); err != nil {
		t.Fatalf("confirm removals: %v", err)
	}
	removed, ok := reopened.Item(before[0].ID)
	if !ok || removed.Playable {
		t.Fatalf("confirmed removal = %#v, exists=%v", removed, ok)
	}
	h, err = household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if position, err := h.Position(session, before[0].ID); err != nil || position != 123 {
		t.Fatalf("progress after confirmed cleanup = %d, %v", position, err)
	}
	if err := db.QueryRow(`SELECT present FROM catalog_physical_files WHERE catalog_id=?`, before[0].ID).Scan(&complete); err != nil || complete != 0 {
		t.Fatalf("confirmed physical source present = %d, %v", complete, err)
	}
}

func TestMassRemovalRequiresReviewWithoutChangingCatalog(t *testing.T) {
	root := t.TempDir()
	for index := range 20 {
		path := filepath.Join(root, fmt.Sprintf("film-%02d.mp4", index))
		if err := os.WriteFile(path, []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 2) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	for index := range 10 {
		if err := os.Remove(filepath.Join(root, fmt.Sprintf("film-%02d.mp4", index))); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Scan(t.Context(), 2); err == nil {
		t.Fatal("mass removal was published without owner review")
	}
	items, err := c.List("", 0, 30)
	if err != nil || len(items) != 20 {
		t.Fatalf("catalog after mass removal = %d items, %v", len(items), err)
	}
	for _, item := range items {
		if !item.Playable {
			t.Fatalf("catalog item %s became unavailable before review", item.ID)
		}
	}
	locations, err := c.LibraryLocations()
	if err != nil || len(locations) != 1 || locations[0].State != "review_required" || locations[0].Items != 10 || locations[0].Missing != 10 {
		t.Fatalf("mass-removal review = %#v, %v", locations, err)
	}
}

func TestReconnectedRootClearsPendingRemovalReview(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"one.mp4": "one", "two.mp4": "two"}
	writeFiles := func() {
		t.Helper()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeFiles()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	for name := range files {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Scan(t.Context(), 1); !errors.Is(err, catalog.ErrRemovalReviewRequired) {
		t.Fatalf("empty-root scan error = %v", err)
	}
	locations, err := c.LibraryLocations()
	if err != nil || len(locations) != 1 {
		t.Fatalf("pending review = %#v, %v", locations, err)
	}
	pending := locations[0].PendingScanID
	writeFiles()
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatalf("reconnected scan: %v", err)
	}
	locations, err = c.LibraryLocations()
	if err != nil || len(locations) != 1 || locations[0].State != "available" || !locations[0].ScanComplete || locations[0].PendingScanID != "" {
		t.Fatalf("reconnected location = %#v, %v", locations, err)
	}
	if err := c.ConfirmRemovals(t.Context(), pending, "film"); !errors.Is(err, catalog.ErrRemovalReviewNotFound) {
		t.Fatalf("stale reconnect review error = %v", err)
	}
}
