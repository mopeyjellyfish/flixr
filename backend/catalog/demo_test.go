package catalog_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestOpenDemoSeedsStableFilenameOnlyCatalog(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	normal, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if items, total, err := normal.Browse("", 0, 100); err != nil || len(items) != 0 || total != 0 {
		t.Fatalf("normal browse = %#v, total %d, err %v", items, total, err)
	}

	seeded, err := catalog.OpenDemo(db)
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := seeded.Browse("", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	films, series := 0, 0
	var film, show catalog.Item
	for _, item := range items {
		switch item.Kind {
		case "film":
			films++
			if item.Title == "Project Hail Mary" {
				film = item
			}
		case "series":
			series++
			if item.Title == "The Bear" {
				show = item
			}
		}
	}
	if total != 40 || films != 20 || series != 20 {
		t.Fatalf("browse total=%d films=%d series=%d, want 40/20/20", total, films, series)
	}
	if film.ID != "086ba9c182077a3164558e108b408f45" || !film.Demo || film.Playable || len(film.Genres) == 0 || film.Genres[0] != "Science Fiction" {
		t.Fatalf("Project Hail Mary = %#v", film)
	}
	if show.ID != "403cb2d07ab4a59992f8a420f0b8250e" || !show.Demo || show.Playable || len(show.Genres) == 0 || show.Genres[0] != "Drama" {
		t.Fatalf("The Bear = %#v", show)
	}
	detail, ok := seeded.Series(show.ID)
	if !ok || len(detail.Seasons) != 1 || len(detail.Seasons[0].Episodes) != 2 || detail.Seasons[0].Episodes[0].Playable || !detail.Seasons[0].Episodes[0].Demo {
		t.Fatalf("The Bear detail = %#v exists=%v", detail, ok)
	}
	var seasons, episodes int
	if err := db.QueryRow("SELECT COUNT(*) FROM catalog_seasons").Scan(&seasons); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM catalog_items WHERE kind='episode'").Scan(&episodes); err != nil {
		t.Fatal(err)
	}
	if seasons != 20 || episodes != 40 {
		t.Fatalf("seeded seasons=%d episodes=%d, want 20/40", seasons, episodes)
	}

	if _, err := catalog.OpenDemo(db); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM catalog_items WHERE demo=1").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 60 {
		t.Fatalf("demo item rows after second open=%d, want 60", rows)
	}
}
