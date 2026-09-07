package catalog_test

import (
	"context"
	"errors"
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
