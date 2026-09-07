package catalog_test

import (
	"context"
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
