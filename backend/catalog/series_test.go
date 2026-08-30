package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestScanCreatesOrderedSeriesWithoutEpisodeTiles(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "A Film.mp4"), []byte("film"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Show/S02/Show.S02E01.mkv", "Show/S01/Show.1x02.mkv", "Show/S01/Show.S01E01.mkv"} {
		path := filepath.Join(tv, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
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
	if err := c.SetRoots(films, tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, total, err := c.Browse("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 || items[0].Kind != "film" || items[1].Kind != "series" {
		t.Fatalf("browse = %#v total %d", items, total)
	}
	series, ok := c.Series(items[1].ID)
	if !ok || len(series.Seasons) != 2 || series.Seasons[0].Number != 1 || len(series.Seasons[0].Episodes) != 2 || series.Seasons[0].Episodes[0].Episode != 1 {
		t.Fatalf("series = %#v ok=%v", series, ok)
	}
	summary, ok := c.Item(items[1].ID)
	if !ok || summary.Kind != "series" || summary.Title != series.Title {
		t.Fatalf("series summary = %#v ok=%v", summary, ok)
	}
}
