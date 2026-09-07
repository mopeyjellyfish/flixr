//go:build media_integration

package catalog_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestCatalogPersistsRealMultitrackCorpusProperties(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "testdata", "media", "tv")
	ctx, cancel := context.WithTimeout(context.Background(), 15_000_000_000)
	defer cancel()
	if err := c.SetRoots("", root); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("List = %#v, %v", items, err)
	}
	var want catalog.Item
	for _, item := range items {
		if item.Episode == 1 {
			want = item
		}
	}
	if want.ID == "" {
		t.Fatalf("episode one missing from %#v", items)
	}
	if want.DurationMS != 2021 || want.PrimaryVideoStreamIndex != 0 || want.Width != 320 || want.Height != 180 || want.Bitrate != 0 || want.HDR != "" || len(want.Audio) != 2 || len(want.Subtitles) != 2 {
		t.Fatalf("decoded properties = %#v", want.MediaProperties)
	}
	if want.Audio[0].Index != 1 || want.Audio[0].Language != "eng" || want.Audio[0].Channels != 1 || want.Audio[1].Index != 2 || want.Audio[1].Language != "fra" || want.Subtitles[0].Index != 3 || want.Subtitles[0].Language != "eng" || !want.Subtitles[0].Default || !want.Subtitles[0].Forced || want.Subtitles[1].Index != 4 || want.Subtitles[1].Language != "fra" || want.Subtitles[1].Default || want.Subtitles[1].Forced {
		t.Fatalf("decoded tracks = %#v / %#v", want.Audio, want.Subtitles)
	}
	reopened, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := reopened.Item(want.ID)
	if !ok || restored.DurationMS != want.DurationMS || restored.PrimaryVideoStreamIndex != 0 || len(restored.Subtitles) != 2 || !restored.Subtitles[0].Forced {
		t.Fatalf("reopened properties = %#v", restored)
	}
}
