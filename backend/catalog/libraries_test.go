package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestNamedLibraryScansTwoLocationsWithoutDuplicatingIdenticalMedia(t *testing.T) {
	first, second, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(first, "First.mp4"), []byte("first"))
	writeReviewMedia(t, filepath.Join(second, "Second.mp4"), []byte("second"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(first, ""); err != nil {
		t.Fatal(err)
	}
	location, err := c.AddLibraryLocation("films", second)
	if err != nil {
		t.Fatal(err)
	}
	if location.ID == "" || location.LibraryID != "films" {
		t.Fatalf("location = %#v", location)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("two locations = %#v, %v", items, err)
	}
	var physical int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_physical_files WHERE present=1 AND location_id<>''`).Scan(&physical); err != nil || physical != 2 {
		t.Fatalf("physical sources = %d, %v", physical, err)
	}

	writeReviewMedia(t, filepath.Join(second, "Duplicate.mp4"), []byte("first"))
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, err = c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("duplicate logical titles = %#v, %v", items, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_physical_files WHERE present=1`).Scan(&physical); err != nil || physical != 3 {
		t.Fatalf("duplicate physical sources = %d, %v", physical, err)
	}
}

func TestChangingPrimaryDuplicateSelectsHealthySourceAndPreservesHistory(t *testing.T) {
	for _, change := range []struct {
		name string
		path func(*testing.T) string
	}{
		{name: "remove", path: func(*testing.T) string { return "" }},
		{name: "move", path: func(t *testing.T) string { return t.TempDir() }},
	} {
		t.Run(change.name, func(t *testing.T) {
			first, second, data := t.TempDir(), t.TempDir(), t.TempDir()
			writeReviewMedia(t, filepath.Join(first, "Duplicate.mp4"), []byte("same media"))
			writeReviewMedia(t, filepath.Join(second, "Duplicate.mp4"), []byte("same media"))
			db, c := openCatalog(t, data)
			if err := c.SetRoots(first, ""); err != nil {
				t.Fatal(err)
			}
			secondLocation, err := c.AddLibraryLocation("films", second)
			if err != nil || c.Scan(t.Context(), 2) != nil {
				t.Fatalf("initial scan: %v", err)
			}
			items, err := c.List("", 0, 10)
			if err != nil || len(items) != 1 {
				t.Fatalf("logical duplicate = %#v, %v", items, err)
			}
			admitted, err := c.PlaybackItem(items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			oldKey := admitted.SourceKey()
			var primaryLocation string
			if err := db.QueryRow(`SELECT source_location_id FROM catalog_items WHERE id=?`, items[0].ID).Scan(&primaryLocation); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO profiles(id,name) VALUES('viewer','Viewer'); INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at) VALUES('viewer',?,42000,1)`, items[0].ID); err != nil {
				t.Fatal(err)
			}
			preview, err := c.PreviewLocationChange(primaryLocation, change.path(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := c.ConfirmLocationChange(t.Context(), preview.ID); err != nil {
				t.Fatal(err)
			}
			fresh, err := c.PlaybackItem(items[0].ID)
			if err != nil || fresh.ID != items[0].ID || fresh.SourceKey() == oldKey {
				t.Fatalf("immediate failover = %#v, %v", fresh, err)
			}
			if file, err := c.OpenSource(items[0].ID, oldKey); err == nil {
				file.Close()
				t.Fatal("old primary admission remained valid")
			}
			var selectedLocation string
			if err := db.QueryRow(`SELECT location_id FROM catalog_physical_files WHERE catalog_id=? AND selected=1 AND present=1`, items[0].ID).Scan(&selectedLocation); err != nil || selectedLocation == primaryLocation {
				t.Fatalf("selected healthy source = %q, %v", selectedLocation, err)
			}
			if selectedLocation != "films-root" && selectedLocation != secondLocation.ID {
				t.Fatalf("unexpected selected location %q", selectedLocation)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, c = openCatalog(t, data)
			defer db.Close()
			restarted, err := c.PlaybackItem(items[0].ID)
			if err != nil || restarted.SourceKey() != fresh.SourceKey() {
				t.Fatalf("restarted failover = %#v, %v", restarted, err)
			}
			var position int64
			if err := db.QueryRow(`SELECT position_ms FROM progress WHERE profile_id='viewer' AND catalog_id=?`, items[0].ID).Scan(&position); err != nil || position != 42000 {
				t.Fatalf("preserved history = %d, %v", position, err)
			}
		})
	}
}

func TestDuplicateFailoverDropsSidecarsUntilHealthyLocationIsScanned(t *testing.T) {
	first, second, data := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{first, second} {
		writeReviewMedia(t, filepath.Join(root, "Duplicate.mkv"), []byte("same video"))
		writeReviewMedia(t, filepath.Join(root, "Duplicate.eng.srt"), []byte("subtitle "+root))
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prober := catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		if filepath.Ext(file.Name()) == ".srt" {
			return catalog.MediaProperties{Subtitles: []catalog.SubtitleTrack{{Index: 0, Codec: "subrip"}}}, nil
		}
		return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264"}, nil
	})
	c, err := catalog.OpenWithProber(db, prober)
	if err != nil || c.SetRoots(first, "") != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := c.AddLibraryLocation("films", second); err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("scan duplicate locations: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 || len(items[0].Subtitles) != 1 {
		t.Fatalf("duplicate with sidecar = %#v, %v", items, err)
	}
	admitted, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	oldVideoKey, oldSubtitleKey := admitted.SourceKey(), items[0].Subtitles[0].SourceKey()
	var primaryLocation string
	if err := db.QueryRow(`SELECT source_location_id FROM catalog_items WHERE id=?`, items[0].ID).Scan(&primaryLocation); err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewLocationChange(primaryLocation, "")
	if err != nil || c.ConfirmLocationChange(t.Context(), preview.ID) != nil {
		t.Fatalf("remove selected duplicate: %v", err)
	}
	fresh, err := c.PlaybackItem(items[0].ID)
	item, ok := c.Item(items[0].ID)
	if err != nil || !ok || len(item.Subtitles) != 0 || fresh.SourceKey() == oldVideoKey {
		t.Fatalf("sidecar-safe failover item=%#v playback=%#v err=%v", item, fresh, err)
	}
	if file, err := c.OpenSubtitleSource(items[0].ID, oldVideoKey, oldSubtitleKey, 0, true); err == nil {
		file.Close()
		t.Fatal("sidecar admission crossed locations")
	}
}

func TestLibraryLocationsRejectOverlappingAndSymlinkedRoots(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddLibraryLocation("films", nested); !errors.Is(err, catalog.ErrOverlappingLocation) {
		t.Fatalf("nested overlap = %v", err)
	}
	if _, err := c.AddLibraryLocation("films", alias); !errors.Is(err, catalog.ErrOverlappingLocation) {
		t.Fatalf("symlink overlap = %v", err)
	}
}

func TestStartupMarksLegacySymlinkOverlapForTopologyReview(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(nested, alias); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO library_locations(id,library_id,root_path) VALUES('tv-root','tv',?)`, alias); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	locations, err := c.LibraryLocations()
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]catalog.LibraryLocation{}
	for _, location := range locations {
		states[location.ID] = location
	}
	if states["tv-root"].State != "topology_review" || !strings.Contains(states["tv-root"].Message, "films-root") {
		t.Fatalf("legacy overlap state = %#v", states["tv-root"])
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var lastScan string
	if err := db.QueryRow(`SELECT last_scan_id FROM library_locations WHERE id='tv-root'`).Scan(&lastScan); err != nil || lastScan != "" {
		t.Fatalf("topology-review location was scanned: %q, %v", lastScan, err)
	}
}

func TestUnavailableLocationDoesNotSuppressHealthyLocation(t *testing.T) {
	unavailable, healthy, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(unavailable, "Retained.mp4"), []byte("retained"))
	writeReviewMedia(t, filepath.Join(healthy, "Existing.mp4"), []byte("existing"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(unavailable, ""); err != nil {
		t.Fatal(err)
	}
	healthyLocation, err := c.AddLibraryLocation("films", healthy)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(unavailable, unavailable+"-offline"); err != nil {
		t.Fatal(err)
	}
	writeReviewMedia(t, filepath.Join(healthy, "New.mp4"), []byte("new"))
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatalf("partial scan: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 3 {
		t.Fatalf("retained plus healthy catalog = %#v, %v", items, err)
	}
	locations, err := c.LibraryLocations()
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, location := range locations {
		states[location.ID] = location.State
	}
	if states["films-root"] != "unavailable" || states[healthyLocation.ID] != "available" {
		t.Fatalf("location states = %#v", states)
	}
}

func TestUnavailableLocationDoesNotRestoreAnAbsentDuplicateSource(t *testing.T) {
	first, second, data := t.TempDir(), t.TempDir(), t.TempDir()
	firstFile := filepath.Join(first, "Duplicate.mp4")
	writeReviewMedia(t, firstFile, []byte("same"))
	writeReviewMedia(t, filepath.Join(second, "Duplicate.mp4"), []byte("same"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(first, ""); err != nil {
		t.Fatal(err)
	}
	secondLocation, err := c.AddLibraryLocation("films", second)
	if err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial duplicate scan: %v", err)
	}
	items, _ := c.List("", 0, 10)
	if err := os.Remove(firstFile); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); !errors.Is(err, catalog.ErrRemovalReviewRequired) {
		t.Fatalf("missing source review = %v", err)
	}
	locations, err := c.LibraryLocations()
	if err != nil {
		t.Fatal(err)
	}
	var scanID string
	for _, location := range locations {
		if location.ID == "films-root" {
			scanID = location.PendingScanID
		}
	}
	if err := c.ConfirmRemovals(t.Context(), scanID, "films-root"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(first, first+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatalf("scan with unavailable prior location: %v", err)
	}
	var firstPresent, secondPresent, secondSelected int
	if err := db.QueryRow(`SELECT present FROM catalog_physical_files WHERE catalog_id=? AND location_id='films-root'`, items[0].ID).Scan(&firstPresent); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT present,selected FROM catalog_physical_files WHERE catalog_id=? AND location_id=?`, items[0].ID, secondLocation.ID).Scan(&secondPresent, &secondSelected); err != nil {
		t.Fatal(err)
	}
	if firstPresent != 0 || secondPresent != 1 || secondSelected != 1 {
		t.Fatalf("physical state first=%d second=%d selected=%d", firstPresent, secondPresent, secondSelected)
	}
	if _, err := c.PlaybackItem(items[0].ID); err != nil {
		t.Fatalf("healthy duplicate lost playback: %v", err)
	}
}

func TestLocationMoveKeepsIDAndRequiresFreshSourceAdmission(t *testing.T) {
	oldRoot, newRoot, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(oldRoot, "Move.mp4"), []byte("same bytes"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(oldRoot, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("initial items = %#v, %v", items, err)
	}
	admitted, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := admitted.SourceKey()
	writeReviewMedia(t, filepath.Join(newRoot, "Move.mp4"), []byte("same bytes"))
	preview, err := c.PreviewLocationChange("films-root", newRoot)
	if err != nil || preview.AffectedSources != 1 || preview.AffectedTitles != 1 {
		t.Fatalf("move preview = %#v, %v", preview, err)
	}
	if err := c.ConfirmLocationChange(context.Background(), preview.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenSource(items[0].ID, oldKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old admitted source = %v", err)
	}
	if _, err := c.PlaybackItem(items[0].ID); !errors.Is(err, catalog.ErrNotPlayable) {
		t.Fatalf("unverified moved source = %v", err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	fresh, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SourceKey() == oldKey {
		t.Fatal("moved source retained its old admission key")
	}
	var root string
	canonicalNewRoot, err := filepath.EvalSymlinks(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT root_path FROM library_locations WHERE id='films-root'`).Scan(&root); err != nil || root != canonicalNewRoot {
		t.Fatalf("stable location root = %q, %v", root, err)
	}
	films, _ := c.Roots()
	if films != canonicalNewRoot {
		t.Fatalf("moved compatibility root = %q", films)
	}
}

func TestLegacySetRootsRejectsPopulatedChangesWithoutInvalidatingAdmission(t *testing.T) {
	oldRoot, newRoot, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(oldRoot, "Safe.mp4"), []byte("safe"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(oldRoot, ""); err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	admitted, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(newRoot, ""); !errors.Is(err, catalog.ErrLocationChangeReviewRequired) {
		t.Fatalf("populated legacy root change = %v", err)
	}
	films, _ := c.Roots()
	canonicalOldRoot, _ := filepath.EvalSymlinks(oldRoot)
	if films != canonicalOldRoot {
		t.Fatalf("rejected change mutated root to %q", films)
	}
	file, err := c.OpenSource(items[0].ID, admitted.SourceKey())
	if err != nil {
		t.Fatalf("rejected change invalidated admission: %v", err)
	}
	file.Close()
}

func TestEnvironmentRootChangeStagesOwnerPreviewBeforeInvalidatingSources(t *testing.T) {
	oldRoot, newRoot, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(oldRoot, "Safe.mp4"), []byte("safe"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(oldRoot, ""); err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, _ := c.List("", 0, 10)
	admitted, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StageEnvironmentRoots(newRoot, ""); err != nil {
		t.Fatal(err)
	}
	films, _ := c.Roots()
	canonicalOldRoot, _ := filepath.EvalSymlinks(oldRoot)
	if films != canonicalOldRoot {
		t.Fatalf("staged environment root changed active root to %q", films)
	}
	libraries, err := c.Libraries()
	if err != nil || len(libraries) == 0 || len(libraries[0].Locations) != 1 {
		t.Fatalf("libraries = %#v, %v", libraries, err)
	}
	location := libraries[0].Locations[0]
	canonicalNewRoot, _ := filepath.EvalSymlinks(newRoot)
	if location.PendingChangeID == "" || location.PendingRootPath != canonicalNewRoot {
		t.Fatalf("pending environment change = %#v", location)
	}
	file, err := c.OpenSource(items[0].ID, admitted.SourceKey())
	if err != nil {
		t.Fatalf("staged change invalidated current admission: %v", err)
	}
	file.Close()
	if err := c.CancelLocationChange(location.PendingChangeID); err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmLocationChange(t.Context(), location.PendingChangeID); !errors.Is(err, catalog.ErrRemovalReviewNotFound) {
		t.Fatalf("discarded preview confirmed: %v", err)
	}
	if err := c.StageEnvironmentRoots(newRoot, ""); err != nil {
		t.Fatal(err)
	}
	libraries, err = c.Libraries()
	if err != nil {
		t.Fatal(err)
	}
	location = libraries[0].Locations[0]
	if err := c.ConfirmLocationChange(t.Context(), location.PendingChangeID); err != nil {
		t.Fatal(err)
	}
	if file, err := c.OpenSource(items[0].ID, admitted.SourceKey()); err == nil {
		file.Close()
		t.Fatal("confirmed environment move retained old admission")
	}
}

func TestLocationChangePreviewIsDurableAndRejectsChangedSources(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(root, "First.mp4"), []byte("first"))
	db, c := openCatalog(t, data)
	if err := c.SetRoots(root, ""); err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	preview, err := c.PreviewLocationChange("films-root", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	writeReviewMedia(t, filepath.Join(root, "First.mp4"), []byte("changed identity"))
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmLocationChange(t.Context(), preview.ID); !errors.Is(err, catalog.ErrRemovalReviewNotFound) {
		t.Fatalf("changed preview confirmation = %v", err)
	}
	locations, err := c.LibraryLocations()
	if err != nil || len(locations) != 1 || locations[0].ID != "films-root" {
		t.Fatalf("stale preview removed location: %#v, %v", locations, err)
	}
}

func TestConfirmedLocationRemovalSurvivesRestartWithoutDeletingLogicalTitle(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(root, "Retained.mp4"), []byte("retained"))
	db, c := openCatalog(t, data)
	if err := c.SetRoots(root, ""); err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("initial items = %#v, %v", items, err)
	}
	preview, err := c.PreviewLocationChange("films-root", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	if err := c.ConfirmLocationChange(t.Context(), preview.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Item(items[0].ID); !ok {
		t.Fatal("confirmation deleted logical title")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	item, ok := c.Item(items[0].ID)
	if !ok || item.Playable {
		t.Fatalf("restarted logical title = %#v, exists=%v", item, ok)
	}
	locations, err := c.LibraryLocations()
	if err != nil || len(locations) != 0 {
		t.Fatalf("removed location after restart = %#v, %v", locations, err)
	}
	films, _ := c.Roots()
	if films != "" {
		t.Fatalf("removed compatibility root = %q", films)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	locations, err = c.LibraryLocations()
	if err != nil || len(locations) != 0 {
		t.Fatalf("scan recreated removed location = %#v, %v", locations, err)
	}
}

func TestSecondLocationMoveRefreshesPrimaryAndSidecarAdmissions(t *testing.T) {
	first, oldRoot, newRoot, data := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	videoName := "Sidecars.mkv"
	for name, contents := range map[string]string{videoName: "video", "Sidecars.fra.m4a": "audio", "Sidecars.eng.srt": "subtitle"} {
		if err := os.WriteFile(filepath.Join(oldRoot, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prober := catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		switch filepath.Ext(file.Name()) {
		case ".m4a":
			return catalog.MediaProperties{Audio: []catalog.AudioTrack{{Index: 0, Codec: "aac"}}}, nil
		case ".srt":
			return catalog.MediaProperties{Subtitles: []catalog.SubtitleTrack{{Index: 0, Codec: "subrip"}}}, nil
		default:
			return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264"}, nil
		}
	})
	c, err := catalog.OpenWithProber(db, prober)
	if err != nil || c.SetRoots(first, "") != nil {
		t.Fatalf("open: %v", err)
	}
	location, err := c.AddLibraryLocation("films", oldRoot)
	if err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("scan second location: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 || len(items[0].Audio) != 1 || len(items[0].Subtitles) != 1 {
		t.Fatalf("second-location sidecars = %#v, %v", items, err)
	}
	admitted, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPrimary, oldSubtitle := admitted.SourceKey(), items[0].Subtitles[0].SourceKey()
	for name, contents := range map[string]string{videoName: "video", "Sidecars.fra.m4a": "audio", "Sidecars.eng.srt": "subtitle"} {
		if err := os.WriteFile(filepath.Join(newRoot, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := c.PreviewLocationChange(location.ID, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmLocationChange(t.Context(), preview.ID); err != nil {
		t.Fatal(err)
	}
	if file, err := c.OpenAudioSource(items[0].ID, oldPrimary, 0, true); err == nil {
		file.Close()
		t.Fatal("old root audio admission remained valid")
	}
	if file, err := c.OpenSubtitleSource(items[0].ID, oldPrimary, oldSubtitle, 0, true); err == nil {
		file.Close()
		t.Fatal("old root subtitle admission remained valid")
	}
	if oldSubtitle == "" {
		t.Fatal("sidecars lacked admission keys")
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	fresh, err := c.PlaybackItem(items[0].ID)
	item, ok := c.Item(items[0].ID)
	if err != nil || !ok || fresh.SourceKey() == oldPrimary || len(item.Audio) != 1 || len(item.Subtitles) != 1 || item.Subtitles[0].SourceKey() == oldSubtitle {
		t.Fatalf("fresh moved admissions: item=%#v playback=%#v err=%v", item, fresh, err)
	}
	audio, err := c.OpenAudioSource(items[0].ID, fresh.SourceKey(), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	audio.Close()
	subtitle, err := c.OpenSubtitleSource(items[0].ID, fresh.SourceKey(), item.Subtitles[0].SourceKey(), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	subtitle.Close()
}

func TestLocationMoveConfirmationRejectsNewOverlappingLocation(t *testing.T) {
	oldRoot, newRoot, data := t.TempDir(), t.TempDir(), t.TempDir()
	nested := filepath.Join(newRoot, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(oldRoot, ""); err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewLocationChange("films-root", newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddLibraryLocation("tv", nested); err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmLocationChange(t.Context(), preview.ID); !errors.Is(err, catalog.ErrOverlappingLocation) {
		t.Fatalf("overlapping stale topology confirmation = %v", err)
	}
}
