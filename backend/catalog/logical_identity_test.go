package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
)

func TestSamePathReplacementKeepsLogicalIdentityProgressAndLockedMetadata(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("first physical file"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatalf("set roots: %v", err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	before := c.MetadataTargets()[0]
	if _, err := c.EditMetadata("film", before.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Locked title", Source: "owner", Locked: true}}}); err != nil {
		t.Fatal(err)
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
	if err != nil || h.Progress(session, before.ID, 123) != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement physical bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatalf("replacement scan: %v", err)
	}
	after := c.MetadataTargets()[0]
	if after.ID != before.ID || after.Title != "Locked title" {
		t.Fatalf("replacement identity = %#v, want id %q with locked title", after, before.ID)
	}
	if position, err := h.Position(session, before.ID); err != nil || position != 123 {
		t.Fatalf("replacement progress = %d, %v", position, err)
	}
}

func TestMissingPhysicalFileRetainsUnavailableLogicalIdentityAndProgress(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("physical file"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatalf("set roots: %v", err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	before := c.MetadataTargets()[0]
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.Select(profile.ID, "")
	if err != nil || h.Progress(session, before.ID, 123) != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatalf("missing-file scan: %v", err)
	}
	after, ok := c.Item(before.ID)
	if !ok || after.Playable {
		t.Fatalf("missing source item = %#v, exists=%v", after, ok)
	}
	if position, err := h.Position(session, before.ID); err != nil || position != 123 {
		t.Fatalf("missing source progress = %d, %v", position, err)
	}
	var present, available int
	if err := db.QueryRow("SELECT present FROM catalog_physical_files WHERE catalog_id=? AND relative_path='Film.mp4'", before.ID).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT available FROM catalog_items WHERE id=?", before.ID).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if present != 0 || available != 0 {
		t.Fatalf("missing source state = present:%d available:%d", present, available)
	}
}

func TestFingerprintSeparatesFilesThatOnlyDifferInTheMiddle(t *testing.T) {
	films := t.TempDir()
	const sample = 64 << 10
	first := append(append(make([]byte, sample), []byte("first middle")...), make([]byte, sample)...)
	second := append(append(make([]byte, sample), []byte("secondmiddle")...), make([]byte, sample)...)
	if err := os.WriteFile(filepath.Join(films, "First.mp4"), first, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(films, "Second.mp4"), second, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("middle-byte collision = %#v, %v", items, err)
	}
}

func TestByteIdenticalDuplicateFilesRetainBothPhysicalSources(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"A.mp4", "B.mp4"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte("identical physical bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("logical duplicates = %#v, %v", items, err)
	}
	var sources, selected int
	if err := db.QueryRow("SELECT COUNT(*),COALESCE(SUM(selected),0) FROM catalog_physical_files WHERE catalog_id=? AND present=1", items[0].ID).Scan(&sources, &selected); err != nil {
		t.Fatal(err)
	}
	if sources != 2 || selected != 1 {
		t.Fatalf("physical duplicate sources = %d selected=%d", sources, selected)
	}
}

// A sampled match after the original file disappears must not transfer history.
func TestMissingSourceSampleCollisionRemainsSeparate(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	first := make([]byte, 3*64<<10)
	second := append([]byte(nil), first...)
	second[70<<10] = 1
	oldPath := filepath.Join(films, "Old.mp4")
	if err := os.WriteFile(oldPath, first, 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	before := c.MetadataTargets()[0].ID
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(films, "New.mp4"), second, 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("collision must retain two anchors: %#v %v", items, err)
	}
	old, ok := c.Item(before)
	if !ok || old.Playable {
		t.Fatalf("old anchor changed: %#v", old)
	}
}

func TestIdentityMergeRoundTripPreservesProfilesAndNewerPlayback(t *testing.T) {
	for _, newPlayback := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "new playback"}[newPlayback], func(t *testing.T) {
			films, data := t.TempDir(), t.TempDir()
			for name, bytes := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
				if err := os.WriteFile(filepath.Join(films, name), []byte(bytes), 0600); err != nil {
					t.Fatal(err)
				}
			}
			db, c := openCatalog(t, data)
			defer db.Close()
			if err := c.SetRoots(films, ""); err != nil {
				t.Fatal(err)
			}
			if err := c.Scan(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			items, err := c.List("", 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			a, b := items[0].ID, items[1].ID
			h, err := household.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"One", "Two"} {
				p, err := h.CreateProfile(name, "")
				if err != nil {
					t.Fatal(err)
				}
				if err := h.ProgressForProfile(p.ID, a, 100); err != nil {
					t.Fatal(err)
				}
				if err := h.ProgressForProfile(p.ID, b, 200); err != nil {
					t.Fatal(err)
				}
			}
			merged, err := c.MergeIdentity("film", a, b)
			if err != nil {
				t.Fatal(err)
			}
			visible, err := c.List("", 0, 10)
			if err != nil || len(visible) != 1 || visible[0].ID != a {
				t.Fatalf("merged list %#v %v", visible, err)
			}
			if newPlayback {
				if _, err := db.Exec(`UPDATE progress SET position_ms=300,generation=generation+1 WHERE catalog_id=?`, a); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Scan(context.Background(), 2); err != nil {
				t.Fatal(err)
			}
			visible, err = c.List("", 0, 10)
			if err != nil || len(visible) != 1 {
				t.Fatalf("scan undid merge: %#v %v", visible, err)
			}
			undone, err := c.UnmergeIdentity(merged.ID)
			if err != nil {
				t.Fatal(err)
			}
			if newPlayback && len(undone.Decisions) == 0 {
				t.Fatal("newer playback reconciliation was not reported")
			}
			rows, err := db.Query(`SELECT catalog_id,position_ms FROM progress ORDER BY catalog_id,profile_id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var id string
				var position int64
				if err := rows.Scan(&id, &position); err != nil {
					t.Fatal(err)
				}
				want := int64(200)
				if id == a {
					want = 100
					if newPlayback {
						want = 300
					}
				}
				if position != want {
					t.Fatalf("%s position %d want %d", id, position, want)
				}
				count++
			}
			if count != 4 {
				t.Fatalf("lost profile progress rows: %d", count)
			}
		})
	}
}

func TestSelectedDuplicateSourceSurvivesEarlierFilenameAndRestart(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Z.mp4")
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	if err := os.WriteFile(filepath.Join(films, "A.mp4"), []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	var selected string
	if err := db.QueryRow("SELECT relative_path FROM catalog_physical_files WHERE catalog_id=? AND selected=1", item.ID).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if selected != "Z.mp4" {
		t.Fatalf("selected source silently changed to %s", selected)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	if err := os.Rename(path, filepath.Join(films, "Moved.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("restart/move identities: %#v %v", items, err)
	}
}

func TestContradictoryReplacementPreservesOriginalMetadataOwnership(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	bytes := make([]byte, 3*64<<10)
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "1"}})
	if err := c.SetTMDBToken("test"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	old := c.MetadataTargets()[0]
	if _, err := c.EditMetadata("film", old.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Owner title", Source: "owner", Locked: true}}}); err != nil {
		t.Fatal(err)
	}
	bytes[70<<10] = 1
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "2"}})
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	repairs, err := c.IdentityRepairs()
	if err != nil {
		t.Fatal(err)
	}
	if len(repairs.Conflicts) != 1 || repairs.Conflicts[0].Reason != "replacement_evidence" {
		t.Fatalf("replacement conflicts %#v", repairs)
	}
	original, ok := c.Item(old.ID)
	if !ok || original.Title != "Owner title" || original.Playable {
		t.Fatalf("original %#v", original)
	}
	fields, err := c.MetadataFields("film", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range fields {
		if field.Field == "title" && field.Locked && field.Value == "Owner title" {
			found = true
		}
	}
	if !found {
		t.Fatal("original title lock lost")
	}
}

func TestOwnerProviderMatchCreatesConflictImmediately(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("test"); err != nil {
		t.Fatal(err)
	}
	for _, item := range c.MetadataTargets() {
		if _, err := c.Match(t.Context(), "film", item.ID, "42", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	repairs, err := c.IdentityRepairs()
	if err != nil || len(repairs.Conflicts) != 1 {
		t.Fatalf("owner match conflicts %#v %v", repairs, err)
	}
	conflict := repairs.Conflicts[0]
	merged, err := c.MergeIdentity("film", conflict.Left.ID, conflict.Right.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.UnmergeIdentity(merged.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergeIdentity("film", conflict.Left.ID, conflict.Right.ID); err != nil {
		t.Fatalf("repeat repair after undo: %v", err)
	}
}

func TestMergedIdentityHiddenFromBrowseAndRestart(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	merge, err := c.MergeIdentity("film", items[0].ID, items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	visible, total, err := c.Browse("", 0, 10)
	if err != nil || len(visible) != 1 || total != 1 || visible[0].ID != merge.Survivor.ID {
		t.Fatalf("merged browse %#v total %d err %v", visible, total, err)
	}
	if _, err := c.UnmergeIdentity(merge.ID); err != nil {
		t.Fatal(err)
	}
	visible, total, err = c.Browse("", 0, 10)
	if err != nil || len(visible) != 2 || total != 2 {
		t.Fatalf("unmerged browse %#v %d %v", visible, total, err)
	}
}

func TestEpisodeDirectoryMoveKeepsLogicalIdentity(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	folder := filepath.Join(tv, "Old Show")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "S01E01.mp4"), []byte("episode bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	old := items[0]
	if err := os.Rename(folder, filepath.Join(tv, "New Show")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err = c.List("", 0, 10)
	if err != nil || len(items) != 1 || items[0].ID != old.ID {
		t.Fatalf("episode move %#v %v", items, err)
	}
}

func TestPlaybackSourceContextRejectsReplacement(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	key := item.SourceKey()
	file, err := c.OpenSource(item.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if file, err := c.OpenSource(item.ID, key); err == nil {
		file.Close()
		t.Fatal("unscanned replacement served to old playback")
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if file, err := c.OpenSource(item.ID, key); err == nil {
		file.Close()
		t.Fatal("scanned replacement served to old playback")
	}
}

func TestIdentityHistoryMappingIsImmutableAndReversible(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := h.RecordViewingEvent(p.ID, household.ViewingEvent{CatalogID: item.ID, Title: item.Title, Kind: "film", Type: household.EventCompleted, Provenance: household.ProvenanceLocal}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := h.History(p.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	merge, err := c.MergeIdentity("film", items[0].ID, items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := h.History(p.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Events) != 2 {
		t.Fatalf("duplicated/lost mapped events %#v", after)
	}
	for _, event := range after.Events {
		if event.CatalogID != items[0].ID {
			t.Fatalf("unmapped event %#v", event)
		}
	}
	var original string
	if err := db.QueryRow("SELECT catalog_id FROM viewing_events WHERE event_id=?", before.Events[0].ID).Scan(&original); err != nil || original != before.Events[0].CatalogID {
		t.Fatalf("immutable event changed %s %v", original, err)
	}
	if _, err := c.UnmergeIdentity(merge.ID); err != nil {
		t.Fatal(err)
	}
	undone, err := h.History(p.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range undone.Events {
		if event != before.Events[i] {
			t.Fatalf("history round trip changed: %#v != %#v", event, before.Events[i])
		}
	}
}

func TestMergeIntoUnavailableAnchorUsesSourceUntilUnmerge(t *testing.T) {
	t.Run("modern", func(t *testing.T) { testMergeIntoUnavailableAnchorUsesSourceUntilUnmerge(t, false) })
	t.Run("legacy", func(t *testing.T) { testMergeIntoUnavailableAnchorUsesSourceUntilUnmerge(t, true) })
}

func testMergeIntoUnavailableAnchorUsesSourceUntilUnmerge(t *testing.T, legacy bool) {
	t.Helper()
	films, data := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(films, "A.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if legacy {
		if _, err := db.Exec("UPDATE catalog_physical_files SET full_digest='',change_token=''"); err != nil {
			t.Fatal(err)
		}
		c, err = catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
			t.Fatal("legacy source admission reprobed media")
			return catalog.MediaProperties{}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
	merge, err := c.MergeIdentity("film", items[0].ID, items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.PlaybackItem(items[0].ID)
	if err != nil {
		t.Fatalf("source not playable on survivor: %v", err)
	}
	file, err := c.OpenSource(item.ID, item.SourceKey())
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := c.UnmergeIdentity(merge.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PlaybackItem(items[0].ID); err == nil {
		t.Fatal("missing survivor playable after undo")
	}
	if _, err := c.PlaybackItem(items[1].ID); err != nil {
		t.Fatal(err)
	}
}

func TestContradictoryReplacementRetainsOldContentProofAfterRestart(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "1"}})
	if err := c.SetTMDBToken("test"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	old := c.MetadataTargets()[0]
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "2"}})
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	if err := c.SetTMDBToken(""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(films, "Recovered.mp4"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("recovered old bytes lost proof %#v %v", items, err)
	}
	item, ok := c.Item(old.ID)
	if !ok || !item.Playable {
		t.Fatalf("old anchor not recovered %#v", item)
	}
}

func TestEqualSizePreservedMtimeReplacementInvalidatesSourceAndCache(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	item, err := c.PlaybackItem(c.MetadataTargets()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	after, err := c.PlaybackItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.SourceKey() == item.SourceKey() {
		t.Fatal("equal-size preserved-mtime replacement reused cached source")
	}
	if file, err := c.OpenSource(item.ID, item.SourceKey()); err == nil {
		file.Close()
		t.Fatal("old session opened changed source")
	}
}

func TestIdentityUnmergeRetainsManualResetAndCompletionToken(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"A.mp4": "first", "B.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(films, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.CreateProfile("One", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ProgressForProfile(p.ID, items[1].ID, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE progress SET completion_id='original-token' WHERE catalog_id=?", items[1].ID); err != nil {
		t.Fatal(err)
	}
	merge, err := c.MergeIdentity("film", items[0].ID, items[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetWatched(p.ID, []string{items[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	undone, err := c.UnmergeIdentity(merge.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(undone.Decisions) == 0 {
		t.Fatal("manual reset reconciliation unreported")
	}
	var position int
	var token string
	if err := db.QueryRow("SELECT position_ms FROM progress WHERE profile_id=? AND catalog_id=?", p.ID, items[0].ID).Scan(&position); err != nil || position != 0 {
		t.Fatalf("reset overwritten %d %v", position, err)
	}
	if err := db.QueryRow("SELECT completion_id FROM progress WHERE profile_id=? AND catalog_id=?", p.ID, items[1].ID).Scan(&token); err != nil || token != "original-token" {
		t.Fatalf("source completion token lost %s %v", token, err)
	}
}

func TestSeriesIdentityRepairRetainsEpisodesAcrossRescanRestartAndUnmerge(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"First Show", "Second Show"} {
		dir := filepath.Join(tv, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "S01E01.mp4"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "42"}})
	if err := c.SetTMDBToken("test"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	repairs, err := c.IdentityRepairs()
	if err != nil || len(repairs.Conflicts) != 1 {
		t.Fatalf("series conflicts %#v %v", repairs, err)
	}
	conflict := repairs.Conflicts[0]
	merge, err := c.MergeIdentity("series", conflict.Left.ID, conflict.Right.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	series, ok := c.Series(conflict.Left.ID)
	if !ok || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 2 {
		t.Fatalf("merged series %#v", series)
	}
	if _, err := c.UnmergeIdentity(merge.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{conflict.Left.ID, conflict.Right.ID} {
		series, ok := c.Series(id)
		if !ok || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 1 {
			t.Fatalf("unmerged series %#v", series)
		}
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{conflict.Left.ID, conflict.Right.ID} {
		series, ok := c.Series(id)
		if !ok || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 1 {
			t.Fatalf("rescanned unmerged series %#v", series)
		}
	}
}

func TestFailedIdentityTransactionDoesNotPublishSeriesOrPhysicalState(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(tv, "Original")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "S01E01.mp4"), []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	before := c.MetadataTargets()
	dir = filepath.Join(tv, "New")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "S01E01.mp4"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER reject_identity BEFORE INSERT ON catalog_items BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err == nil {
		t.Fatal("failed transaction accepted")
	}
	after := c.MetadataTargets()
	if len(after) != len(before) {
		t.Fatalf("failed transaction published series: %#v", after)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM catalog_physical_files WHERE present=1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed transaction changed source rows %d %v", count, err)
	}
}

func TestLegacyMissingDigestRequiresExplicitRepair(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Old.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE catalog_physical_files SET full_digest=''"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, c = openCatalog(t, data)
	defer db.Close()
	if err := os.Rename(path, filepath.Join(films, "New.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("legacy proof silently merged %#v %v", items, err)
	}
	repairs, err := c.IdentityRepairs()
	if err != nil || len(repairs.Conflicts) != 1 || repairs.Conflicts[0].Reason != "ambiguous_duplicate" {
		t.Fatalf("missing legacy repair %#v %v", repairs, err)
	}
}

func TestCancelledScanRetainsPersistedLogicalAndPhysicalState(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	id := c.MetadataTargets()[0].ID
	var digest string
	if err := db.QueryRow("SELECT full_digest FROM catalog_physical_files WHERE present=1").Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(ctx context.Context, _ *os.File) (catalog.MediaProperties, error) {
		close(started)
		<-ctx.Done()
		return catalog.MediaProperties{}, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := c.StartScan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	if err := c.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	var after, logical string
	if err := db.QueryRow("SELECT full_digest,catalog_id FROM catalog_physical_files WHERE present=1").Scan(&after, &logical); err != nil || after != digest || logical != id {
		t.Fatalf("cancel changed physical state %s %s %v", after, logical, err)
	}
	if _, ok := c.Item(id); !ok {
		t.Fatal("cancel lost logical anchor")
	}
}

func TestReplacementDuringProviderOutageRetainsAnchorMatchState(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "42", Synopsis: "Known title"}})
	if err := c.SetTMDBToken("test"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	old := c.MetadataTargets()[0]
	c.SetProvider(ownerMatchProvider{outage: true})
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	after, ok := c.Item(old.ID)
	if !ok || after.ProviderID != "42" || after.Provider != "tmdb" || after.Synopsis != "Known title" {
		t.Fatalf("anchor match state lost %#v", after)
	}
}

func TestMissingReplacementUsesLastObservedAnchorNotNewestFileMtime(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "1"}})
	if err := c.SetTMDBToken("test"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Unix(100, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "2"}})
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var replacementID string
	if err := db.QueryRow("SELECT catalog_id FROM catalog_physical_files WHERE present=1").Scan(&replacementID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTMDBToken(""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := db.QueryRow("SELECT catalog_id FROM catalog_physical_files WHERE present=1").Scan(&restored); err != nil || restored != replacementID {
		t.Fatalf("reappearance chose historical mtime anchor %s instead of %s: %v", restored, replacementID, err)
	}
}

func TestMissingSeriesIsUnavailableWithoutLosingItsIdentity(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(tv, "Show")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "S01E01.mp4")
	if err := os.WriteFile(path, []byte("episode"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	before := c.MetadataTargets()[0]
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	series, ok := c.Series(before.ID)
	if !ok || series.Playable || len(series.Seasons) != 1 {
		t.Fatalf("missing series %#v exists=%v", series, ok)
	}
}

func TestIdentitySeriesUnmergeNewEpisode(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"First Show", "Second Show"} {
		if err := os.Mkdir(filepath.Join(tv, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tv, name, "S01E01.mp4"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var survivor, source, sourcePath string
	rows, err := db.Query("SELECT id,relative_path,series_id FROM catalog_items ORDER BY relative_path")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, path, series string
		if err := rows.Scan(&id, &path, &series); err != nil {
			t.Fatal(err)
		}
		if survivor == "" {
			survivor = series
		} else {
			source = series
			sourcePath = filepath.Dir(path)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	merge, err := c.MergeIdentity("series", survivor, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tv, sourcePath, "S01E02.mp4"), []byte("new episode source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UnmergeIdentity(merge.ID); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow("SELECT series_id FROM catalog_items WHERE relative_path=?", filepath.ToSlash(filepath.Join(sourcePath, "S01E02.mp4"))).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != source {
		t.Fatalf("new source episode remains assigned to survivor %s, want original source %s", got, source)
	}
}

func TestIdentitySeriesMergeKeepsEpisodeArtworkRetryBoundToCurrentParent(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"First Show", "Second Show"} {
		if err := os.Mkdir(filepath.Join(tv, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tv, name, "S01E01.mp4"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil || c.Scan(t.Context(), 1) != nil {
		t.Fatalf("seed series: %v", err)
	}
	rows, err := db.Query("SELECT id,series_id FROM catalog_items ORDER BY relative_path")
	if err != nil {
		t.Fatal(err)
	}
	var survivor, source, sourceEpisode string
	for rows.Next() {
		var episode, series string
		if err := rows.Scan(&episode, &series); err != nil {
			t.Fatal(err)
		}
		if survivor == "" {
			survivor = series
		} else {
			source, sourceEpisode = series, episode
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE catalog_series SET provider_id='101' WHERE id=?", survivor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE catalog_series SET provider_id='202' WHERE id=?", source); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE catalog_items SET provider_id='303' WHERE id=?", sourceEpisode); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_artwork_retries(catalog_kind,catalog_id,artwork_kind,provider_id,parent_catalog_id,parent_provider_id,provider_path) VALUES('episode',?,'backdrop','303',?,'202','/still.jpg')`, sourceEpisode, source); err != nil {
		t.Fatal(err)
	}
	merge, err := c.MergeIdentity("series", survivor, source)
	if err != nil {
		t.Fatal(err)
	}
	var parent, provider string
	if err := db.QueryRow("SELECT parent_catalog_id,parent_provider_id FROM catalog_artwork_retries WHERE catalog_id=?", sourceEpisode).Scan(&parent, &provider); err != nil || parent != survivor || provider != "101" {
		t.Fatalf("merged retry parent = %q/%q, %v", parent, provider, err)
	}
	if _, err := c.UnmergeIdentity(merge.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT parent_catalog_id,parent_provider_id FROM catalog_artwork_retries WHERE catalog_id=?", sourceEpisode).Scan(&parent, &provider); err != nil || parent != source || provider != "202" {
		t.Fatalf("unmerged retry parent = %q/%q, %v", parent, provider, err)
	}
}

func TestIdentitySeriesMergeSafeEpisodeReplacement(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"First Show", "Second Show"} {
		if err := os.Mkdir(filepath.Join(tv, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tv, name, "S01E01.mp4"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var survivor, source, sourcePath, sourceEpisode string
	rows, err := db.Query("SELECT id,relative_path,series_id FROM catalog_items ORDER BY relative_path")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, path, series string
		if err := rows.Scan(&id, &path, &series); err != nil {
			t.Fatal(err)
		}
		if survivor == "" {
			survivor = series
		} else {
			source = series
			sourcePath = path
			sourceEpisode = id
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := c.MergeIdentity("series", survivor, source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tv, sourcePath), []byte("replacement of same episode"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow("SELECT catalog_id FROM catalog_physical_files WHERE relative_path=? AND present=1", sourcePath).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != sourceEpisode {
		t.Fatalf("safe replacement creates new anchor %s, want %s", got, sourceEpisode)
	}
}

func TestIdentityRenamedSeriesSafeReplacement(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(tv, "Original Show"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tv, "Original Show", "S01E01.mp4"), []byte("episode original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := db.QueryRow("SELECT id FROM catalog_items").Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(tv, "Original Show"), filepath.Join(tv, "Renamed Show")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tv, "Renamed Show", "S01E01.mp4"), []byte("replacement episode"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow("SELECT catalog_id FROM catalog_physical_files WHERE present=1").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf("replacement after directory rename creates anchor %s, want %s", got, original)
	}
}
