package catalog_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

func TestLocalMovieNFOOverridesOfflineAndRemovalRestoresFilename(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Filename.Title.2020.mp4"))
	nfo := filepath.Join(films, "Filename.Title.2020.nfo")
	if err := os.WriteFile(nfo, []byte(`<movie><title>Local Title</title><plot>Local plot</plot><year>2024</year><mpaa>PG</mpaa><tag>Family</tag></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if item.Title != "Local Title" || item.Synopsis != "Local plot" || item.Year != 2024 {
		t.Fatalf("local item = %#v", item)
	}
	fields, err := c.MetadataFields("film", item.ID)
	if err != nil || fieldValue(fields, "title") != "Local Title" || fieldValue(fields, "content_rating") != "PG" || fieldValue(fields, "tags") != "Family" {
		t.Fatalf("local fields = %#v, %v", fields, err)
	}
	if err := os.WriteFile(nfo, []byte(`<movie><title>Changed Locally</title></movie>`), 0600); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("rescan changed NFO: %v", err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Changed Locally" {
		t.Fatalf("unchanged-media rescan = %#v", got)
	}
	if err := os.Remove(nfo); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("remove/rescan: %v", err)
	}
	if got := c.MetadataTargets()[0]; got.Title == "Changed Locally" || !strings.Contains(got.Title, "Filename Title") {
		t.Fatalf("removed NFO did not reveal filename fallback: %#v", got)
	}
}

func TestInvalidLocalNFORetainsLastGoodMetadataAndIsActionable(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	nfo := filepath.Join(films, "Film.nfo")
	if err := os.WriteFile(nfo, []byte(`<movie><title>Last Good</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, []byte(`<!DOCTYPE movie [<!ENTITY x SYSTEM "https://example.invalid/x">]><movie><title>&x;</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Last Good" {
		t.Fatalf("invalid NFO replaced good state: %#v", got)
	}
	status := c.ScanStatus()
	if status.Failed == 0 {
		t.Fatalf("invalid NFO status = %#v", status)
	}
}

func TestPartiallyInvalidNFOUpdatesValidFieldsAndRetainsInvalidField(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	nfo := filepath.Join(films, "Film.nfo")
	if err := os.WriteFile(nfo, []byte(`<movie><title>First</title><year>2020</year></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, []byte(`<movie><title>Second</title><year>twenty</year></movie>`), 0600); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Second" || got.Year != 2020 {
		t.Fatalf("partial update = %#v", got)
	}
}

func TestEpisodeLocalMetadataIsEditableAndPersists(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(tv, "Show"), 0700); err != nil {
		t.Fatal(err)
	}
	writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mkv"))
	if err := os.WriteFile(filepath.Join(tv, "Show", "Show.S01E01.nfo"), []byte(`<episodedetails><title>Local Episode</title><plot>Episode plot</plot></episodedetails>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots("", tv); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 || items[0].Title != "Local Episode" {
		t.Fatalf("episodes = %#v, %v", items, err)
	}
	fields, err := c.MetadataFields("episode", items[0].ID)
	if err != nil || fieldValue(fields, "synopsis") != "Episode plot" {
		t.Fatalf("episode fields = %#v, %v", fields, err)
	}
}

func TestLocalArtworkPublishesAtomicallyAndCorruptReplacementRetainsGoodBytes(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	poster := filepath.Join(films, "Film-poster.png")
	good := encodePNG(t, 4, 5, color.RGBA{R: 0xcc, A: 0xff})
	if err := os.WriteFile(poster, good, 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	got, contentType, err := c.Artwork(item.ID, "poster")
	if err != nil || contentType != "image/png" || !bytes.Equal(got, good) {
		t.Fatalf("artwork = %d %q %v", len(got), contentType, err)
	}
	if err := os.WriteFile(poster, []byte("corrupt"), 0600); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	got, _, err = c.Artwork(item.ID, "poster")
	if err != nil || !bytes.Equal(got, good) {
		t.Fatalf("corrupt replacement changed artwork: %d %v", len(got), err)
	}
	if err := os.Remove(poster); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Poster != "" {
		t.Fatalf("removed local artwork reference = %q", got.Poster)
	}
	if _, _, err := c.Artwork(item.ID, "poster"); err == nil {
		t.Fatal("removed local-only artwork remained published")
	}
}

func TestLocalMetadataWinsRefreshAndRemovalRevealsRefreshedProviderFallback(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	nfo := filepath.Join(films, "Film.nfo")
	if err := os.WriteFile(nfo, []byte(`<movie><title>Local</title><plot>Local plot</plot></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "42", Title: "Provider", Synopsis: "Provider old", Year: 2020}})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	c.SetProvider(ownerMatchProvider{enrichment: catalog.Enrichment{ProviderID: "42", Title: "Provider refreshed", Synopsis: "Provider refreshed plot", Year: 2025}})
	if _, err := c.Refresh(context.Background(), "film", item.ID); err != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Local" || got.Synopsis != "Local plot" || got.Year != 2025 {
		t.Fatalf("local refresh precedence = %#v", got)
	}
	if err := os.Remove(nfo); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Provider refreshed" || got.Synopsis != "Provider refreshed plot" || got.Year != 2025 {
		t.Fatalf("provider fallback = %#v", got)
	}
}

func TestLockedOwnerWinsLocalAndConfirmedMatchUpdatesHiddenFallback(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	nfo := filepath.Join(films, "Film.nfo")
	if err := os.WriteFile(nfo, []byte(`<movie><title>Local</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Owner", Source: "owner", Locked: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, []byte(`<movie><title>Local changed</title></movie>`), 0600); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Owner" {
		t.Fatalf("locked precedence=%#v", got)
	}
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Owner" {
		t.Fatalf("match overwrote owner/local=%#v", got)
	}
	if _, err := c.EditMetadata("film", item.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "title", Value: "Owner", Source: "owner", Locked: false}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := c.MetadataTargets()[0]; got.Title != "Local changed" {
		t.Fatalf("unlock did not reveal local=%#v", got)
	}
}

func TestSeriesNFOAndFlatMovieDirectoryGuard(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "One.mp4"))
	writeMedia(t, filepath.Join(films, "Two.mp4"))
	if err := os.WriteFile(filepath.Join(films, "movie.nfo"), []byte(`<movie><title>Wrong shared title</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tv, "Show"), 0700); err != nil {
		t.Fatal(err)
	}
	writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mkv"))
	if err := os.WriteFile(filepath.Join(tv, "Show", "tvshow.nfo"), []byte(`<tvshow><title>Local Show</title><plot>Show plot</plot><mpaa>TV-PG</mpaa></tvshow>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, tv); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	filmsList, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range filmsList {
		if item.Kind == "film" && item.Title == "Wrong shared title" {
			t.Fatalf("flat movie.nfo applied: %#v", filmsList)
		}
	}
	series := c.MetadataTargets()
	found := false
	for _, item := range series {
		if item.Kind == "series" && item.Title == "Local Show" {
			found = true
		}
	}
	if !found {
		t.Fatalf("series metadata targets=%#v", series)
	}
}

func TestLocalMetadataCancellationAndRestartPreserveCommittedState(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	nfo := filepath.Join(films, "Film.nfo")
	if err := os.WriteFile(nfo, []byte(`<movie><title>Committed</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, []byte(`<movie><title>Cancelled</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = c.Scan(ctx, 1)
	if got := c.MetadataTargets()[0]; got.Title != "Committed" {
		t.Fatalf("cancelled state=%#v", got)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	if got := reopened.MetadataTargets()[0]; got.Title != "Committed" {
		t.Fatalf("restarted state=%#v", got)
	}
}

func encodePNG(t *testing.T, width, height int, fill color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, fill)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
