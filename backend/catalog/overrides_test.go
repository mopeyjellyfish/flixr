package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func TestOwnerMetadataEditIsAtomicAndPersistsFieldLock(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	updated, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Owner title", Source: "owner", Locked: true}, {Field: "tags", Value: "family,local", Source: "local", Locked: true}, {Field: "content_rating", Value: "PG", Source: "local", Locked: true}}})
	if err != nil || updated.Title != "Owner title" {
		t.Fatalf("edit = %#v, %v", updated, err)
	}
	fields, err := c.MetadataFields("film", item.ID)
	if err != nil || len(fields) != 3 {
		t.Fatalf("fields = %#v, %v", fields, err)
	}
	preview, err := c.PreviewMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Provider title", Source: "owner"}}})
	if err != nil || len(preview) != 3 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "year", Value: "not-a-year", Source: "owner"}}}); err == nil {
		t.Fatal("invalid edit committed")
	}
	if got := c.MetadataTargets()[0]; got.Title != "Owner title" {
		t.Fatalf("failed edit changed title: %#v", got)
	}
}

func TestLockedMetadataSurvivesRefreshRescanAndRestart(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{
		{Field: "title", Value: "Owner title", Source: "owner", Locked: true},
		{Field: "synopsis", Value: "Owner synopsis", Source: "owner", Locked: true},
		{Field: "year", Value: "1999", Source: "owner", Locked: true},
		{Field: "tags", Value: "family", Source: "local", Locked: true},
		{Field: "content_rating", Value: "PG", Source: "local", Locked: true},
	}}); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "77", Year: 2025, Synopsis: "Provider synopsis"}})
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	got := c.MetadataTargets()[0]
	if got.Title != "Owner title" || got.Synopsis != "Owner synopsis" || got.Year != 1999 {
		t.Fatalf("locked rescan = %#v", got)
	}
	if _, err := c.Match(context.Background(), "film", got.ID, "77", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	preview, err := c.RefreshPreview(context.Background(), "film", got.ID)
	if err != nil || fieldValue(preview, "synopsis") != "Owner synopsis" || fieldValue(preview, "year") != "1999" {
		t.Fatalf("locked preview = %#v, %v", preview, err)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	fields, err := reopened.MetadataFields("film", got.ID)
	if err != nil || fieldValue(fields, "tags") != "family" || fieldValue(fields, "content_rating") != "PG" {
		t.Fatalf("restarted fields = %#v, %v", fields, err)
	}
}

func TestRefreshFailureAndCancellationLeaveMetadataUnchanged(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	before := c.MetadataTargets()[0]
	c.SetProvider(ownerMatchProvider{outage: true})
	if _, err := c.Refresh(context.Background(), "film", item.ID); err == nil {
		t.Fatal("refresh succeeded during outage")
	}
	c.SetProvider(ownerMatchProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Refresh(ctx, "film", item.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh = %v", err)
	}
	after := c.MetadataTargets()[0]
	if after.Synopsis != before.Synopsis || after.Year != before.Year || after.Poster != before.Poster {
		t.Fatalf("failed refresh changed metadata: before=%#v after=%#v", before, after)
	}
}

func TestRefreshAppliesAllProviderSupportedFields(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{poster: "refreshed", enrichment: catalog.Enrichment{ProviderID: "42", Title: "Provider title", Year: 2025, Synopsis: "Provider synopsis", Poster: "/new-poster", Backdrop: "/new-backdrop"}})
	preview, err := c.RefreshPreview(context.Background(), "film", item.ID)
	if err != nil || fieldValue(preview, "title") != "Provider title" || fieldValue(preview, "poster") == "" || fieldValue(preview, "backdrop") == "" {
		t.Fatalf("refresh preview = %#v, %v", preview, err)
	}
	updated, err := c.Refresh(context.Background(), "film", item.ID)
	if err != nil || updated.Title != "Provider title" || updated.Synopsis != "Provider synopsis" || updated.Year != 2025 || updated.Poster == "" || updated.Backdrop == "" {
		t.Fatalf("refresh = %#v, %v", updated, err)
	}
	fields, err := c.MetadataFields("film", item.ID)
	if err != nil || fieldValue(fields, "title") != "Provider title" || fieldValue(fields, "tags") != "" {
		t.Fatalf("provider provenance = %#v, %v", fields, err)
	}
}

func TestMetadataPersistenceFailureDoesNotChangeMemory(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := db.Exec("DROP TABLE catalog_metadata_fields"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Never committed", Source: "owner", Locked: true}}}); err == nil {
		t.Fatal("edit succeeded after metadata persistence failed")
	}
	if got := c.MetadataTargets()[0]; got.Title != item.Title {
		t.Fatalf("failed edit changed memory: %#v", got)
	}
}

func TestRefreshDoesNotOverwriteNewerOwnerMatch(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	started, gate := make(chan struct{}, 1), make(chan struct{})
	c.SetProvider(ownerMatchProvider{started: started, gate: gate, enrichment: catalog.Enrichment{ProviderID: "42", Title: "stale", Year: 2025}})
	result := make(chan error, 1)
	go func() { _, err := c.Refresh(context.Background(), "film", item.ID); result <- err }()
	<-started
	if _, err := c.Unmatch("film", item.ID); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-result; !errors.Is(err, catalog.ErrMetadataStale) {
		t.Fatalf("stale refresh = %v", err)
	}
	if got := c.MetadataTargets()[0]; got.ProviderID != "" || !got.OwnerUnmatch {
		t.Fatalf("stale refresh overwrote owner choice: %#v", got)
	}
}

func fieldValue(fields []catalog.MetadataField, name string) string {
	for _, field := range fields {
		if field.Field == name {
			return field.Value
		}
	}
	return ""
}

func TestRefreshDatabaseFailurePreservesArtworkBytes(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "original", enrichment: catalog.Enrichment{ProviderID: "42", Poster: "/old"}})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	before := c.MetadataTargets()[0]
	original, contentType, err := c.Artwork(item.ID, "poster")
	if err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{poster: "replacement", enrichment: catalog.Enrichment{ProviderID: "42", Title: "Changed", Poster: "/new"}})
	if _, err := c.RefreshPreview(context.Background(), "film", item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_refresh BEFORE UPDATE ON catalog_items BEGIN SELECT RAISE(ABORT, 'forced metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Refresh(context.Background(), "film", item.ID); err == nil {
		t.Fatal("refresh committed despite forced failure")
	}
	actual, actualType, err := c.Artwork(item.ID, "poster")
	if err != nil || string(actual) != string(original) || actualType != contentType {
		t.Fatalf("failed refresh changed visible artwork: %q => %q (%v)", original, actual, err)
	}
	if after := c.MetadataTargets()[0]; after.Title != before.Title || after.Poster != before.Poster {
		t.Fatalf("failed refresh changed metadata: %#v", after)
	}
}

func TestRefreshPreviewPreservesArtworkOnLateChanges(t *testing.T) {
	for _, change := range []string{"unmatch", "cancel", "lock"} {
		t.Run(change, func(t *testing.T) {
			films, data := t.TempDir(), t.TempDir()
			writeMedia(t, filepath.Join(films, "Film.mp4"))
			db, c := openCatalog(t, data)
			defer db.Close()
			c.SetProvider(ownerMatchProvider{poster: "original"})
			if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
				t.Fatalf("scan: %v", err)
			}
			item := c.MetadataTargets()[0]
			if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
				t.Fatal(err)
			}
			original, _, err := c.Artwork(item.ID, "poster")
			if err != nil {
				t.Fatal(err)
			}
			c.SetProvider(ownerMatchProvider{poster: "replacement"})
			if _, err := c.RefreshPreview(context.Background(), "film", item.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var want error
			switch change {
			case "unmatch":
				if _, err := c.Unmatch("film", item.ID); err != nil {
					t.Fatal(err)
				}
				want = catalog.ErrMetadataStale
			case "cancel":
				cancel()
				want = context.Canceled
			case "lock":
				if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "poster", Value: "/api/v1/catalog/artwork/" + item.ID + "/poster", Source: "provider", Locked: true}}}); err == nil {
					t.Fatal("owner forged provider provenance")
				}
				if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "poster", Value: "/api/v1/catalog/artwork/" + item.ID + "/poster", Source: "owner", Locked: true}}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Refresh(ctx, "film", item.ID); !errors.Is(err, want) {
				t.Fatalf("refresh = %v, want %v", err, want)
			}
			actual, _, err := c.Artwork(item.ID, "poster")
			if err != nil || string(actual) != string(original) {
				t.Fatalf("late %s changed artwork: %q (%v)", change, actual, err)
			}
		})
	}
}

func TestRefreshArtworkCacheFailurePreservesMetadataAndBytes(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "original"})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	before := c.MetadataTargets()[0]
	original, _, err := c.Artwork(item.ID, "poster")
	if err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{poster: "replacement", enrichment: catalog.Enrichment{ProviderID: "42", Title: "Changed", Poster: "/new", Backdrop: "/new"}})
	if _, err := c.RefreshPreview(context.Background(), "film", item.ID); err != nil {
		t.Fatal(err)
	}
	// Fail the second artwork publication after the first object has been written.
	if _, err := db.Exec(`CREATE TRIGGER reject_backdrop BEFORE INSERT ON catalog_artwork WHEN NEW.kind='backdrop' BEGIN SELECT RAISE(ABORT, 'forced art cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Refresh(context.Background(), "film", item.ID); err == nil {
		t.Fatal("refresh committed after artwork failure")
	}
	actual, _, err := c.Artwork(item.ID, "poster")
	if err != nil || string(actual) != string(original) {
		t.Fatalf("art cache failure changed poster: %q (%v)", actual, err)
	}
	if got := c.MetadataTargets()[0]; got.Title != before.Title {
		t.Fatalf("art cache failure changed metadata: %#v", got)
	}
	entries, err := os.ReadDir(filepath.Join(data, "artwork", "objects"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("aborted refresh leaked objects: %d (%v)", len(entries), err)
	}
}

func TestRescanPreservesLockedArtworkBytes(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "original", enrichment: catalog.Enrichment{ProviderID: "42", Poster: "/old"}})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "poster", Value: item.Poster, Source: "owner", Locked: true}}}); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{poster: "replacement", enrichment: catalog.Enrichment{ProviderID: "42", Poster: "/new"}})
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("changed media fingerprint"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	dataBytes, _, err := c.Artwork(item.ID, "poster")
	if err != nil || string(dataBytes) != "original" {
		t.Fatalf("rescan changed locked artwork: %q (%v)", dataBytes, err)
	}
}

func TestRefreshRejectsPreviewAfterSameIdentityRematch(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "original"})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{poster: "stale preview"})
	if _, err := c.RefreshPreview(context.Background(), "film", item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Unmatch("film", item.ID); err != nil {
		t.Fatal(err)
	}
	c.SetProvider(ownerMatchProvider{poster: "new owner choice"})
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Refresh(context.Background(), "film", item.ID); !errors.Is(err, catalog.ErrMetadataStale) {
		t.Fatalf("refresh reused preview across rematch: %v", err)
	}
	actual, _, err := c.Artwork(item.ID, "poster")
	if err != nil || string(actual) != "new owner choice" {
		t.Fatalf("refresh changed new owner artwork: %q (%v)", actual, err)
	}
}

func TestRefreshPreviewEvictionRetainsRecentProviderSnapshot(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	for i := 0; i < 8; i++ {
		writeMedia(t, filepath.Join(films, fmt.Sprintf("Film %d.mp4", i)))
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "preview", enrichment: catalog.Enrichment{ProviderID: "42", Title: "Preview title", Poster: "/poster"}})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items := c.MetadataTargets()
	for _, item := range items {
		if _, err := c.RefreshPreview(context.Background(), "film", item.ID); err != nil {
			t.Fatal(err)
		}
	}
	c.SetProvider(ownerMatchProvider{outage: true})
	if _, err := c.Refresh(context.Background(), "film", items[0].ID); err == nil {
		t.Fatal("old preview was never evicted")
	}
	updated, err := c.Refresh(context.Background(), "film", items[len(items)-1].ID)
	if err != nil || updated.Title != "Preview title" {
		t.Fatalf("recent preview refetched provider: %#v (%v)", updated, err)
	}
}
