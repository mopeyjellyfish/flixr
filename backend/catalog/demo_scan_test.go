package catalog_test

import (
	"context"
	"os"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestScanKeepsDemoCatalogVisibleWithoutRestart(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenDemo(db)
	if err != nil {
		t.Fatal(err)
	}
	films, tv := t.TempDir(), t.TempDir()
	if err := c.SetRoots(films, tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, total, err := c.Browse("The Bear", 0, 10)
	if err != nil || total != 1 || len(items) != 1 || !items[0].Demo {
		t.Fatalf("browse after scan = %#v total=%d err=%v", items, total, err)
	}
	item, ok := c.Item(items[0].ID)
	if !ok || !item.Demo || item.Playable {
		t.Fatalf("item after scan = %#v exists=%v", item, ok)
	}
	series, ok := c.Series(items[0].ID)
	if !ok || !series.Demo || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 2 {
		t.Fatalf("series after scan = %#v exists=%v", series, ok)
	}
	_ = os.RemoveAll(films)
}
