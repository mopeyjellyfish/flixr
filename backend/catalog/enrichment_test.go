package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type metadataFake struct {
	mu    sync.Mutex
	calls map[string]int
}

func (f *metadataFake) Lookup(_ context.Context, token, kind, title string) (catalog.Enrichment, error) {
	if token != "secret" {
		return catalog.Enrichment{}, os.ErrPermission
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[kind+":"+title]++
	return catalog.Enrichment{ProviderID: kind + "-id", Title: kind + " title", Year: 2024, Synopsis: kind + " summary", Poster: "/poster.jpg", Backdrop: "/backdrop.jpg"}, nil
}

func (f *metadataFake) FetchArtwork(_ context.Context, imagePath string) (catalog.Artwork, error) {
	if imagePath != "/poster.jpg" && imagePath != "/backdrop.jpg" {
		return catalog.Artwork{}, os.ErrInvalid
	}
	return catalog.Artwork{Bytes: []byte("image:" + imagePath), ContentType: "image/jpeg"}, nil
}

func (f *metadataFake) Validate(_ context.Context, token string) error {
	if token != "secret" {
		return catalog.ErrInvalidCredential
	}
	return nil
}

func (f *metadataFake) LookupEpisode(_ context.Context, token, seriesID string, season, episode int) (catalog.Enrichment, error) {
	if token != "secret" || seriesID != "series-id" {
		return catalog.Enrichment{}, os.ErrPermission
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[fmt.Sprintf("episode:%d:%d", season, episode)]++
	return catalog.Enrichment{ProviderID: fmt.Sprintf("episode-%d-%d", season, episode), Title: fmt.Sprintf("Episode %d", episode), Year: 2024, Synopsis: "episode summary", Backdrop: "/backdrop.jpg"}, nil
}

type outcomeProvider struct {
	lookup func(kind, title string) (catalog.Enrichment, error)
}

func (p outcomeProvider) Lookup(_ context.Context, _ string, kind, title string) (catalog.Enrichment, error) {
	return p.lookup(kind, title)
}

type metadataBlockingProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p metadataBlockingProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	close(p.started)
	<-p.release
	return catalog.Enrichment{}, nil
}

func TestScanRecordsProviderOutcomes(t *testing.T) {
	t.Run("film and series no match", func(t *testing.T) {
		films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
		writeMedia(t, filepath.Join(films, "Film.mp4"))
		writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mp4"))
		db, c := openCatalog(t, data)
		defer db.Close()
		c.SetProvider(outcomeProvider{lookup: func(string, string) (catalog.Enrichment, error) { return catalog.Enrichment{}, nil }})
		if err := c.SetTMDBToken("secret"); err != nil {
			t.Fatal(err)
		}
		if err := c.SetRoots(films, tv); err != nil {
			t.Fatal(err)
		}
		if err := c.Scan(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if status := c.ScanStatus(); status.Status != "complete" || status.Unmatched != 2 || status.Failed != 0 {
			t.Fatalf("status = %#v", status)
		}
		outcomes := scanOutcomes(t, db, c.ScanStatus().ID)
		if outcomes["unmatched"] != 2 {
			t.Fatalf("outcomes = %#v", outcomes)
		}
	})

	t.Run("failure is partial and redacted", func(t *testing.T) {
		films, data := t.TempDir(), t.TempDir()
		writeMedia(t, filepath.Join(films, "Film.mp4"))
		db, c := openCatalog(t, data)
		defer db.Close()
		c.SetProvider(outcomeProvider{lookup: func(string, string) (catalog.Enrichment, error) {
			return catalog.Enrichment{}, errors.New("https://provider.example/?token=secret " + films)
		}})
		if err := c.SetTMDBToken("secret"); err != nil {
			t.Fatal(err)
		}
		if err := c.SetRoots(films, ""); err != nil {
			t.Fatal(err)
		}
		if err := c.Scan(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if status := c.ScanStatus(); status.Status != "partial" || status.Failed != 1 || status.Unmatched != 0 {
			t.Fatalf("status = %#v", status)
		}
		if status := c.MetadataStatus(); status.State != "failed" || !status.Configured {
			t.Fatalf("metadata status = %#v", status)
		}
		rows, err := db.Query("SELECT relative_path, outcome, message FROM scan_files WHERE scan_id=?", c.ScanStatus().ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("missing provider failure observation")
		}
		var identifier, outcome, message string
		if err := rows.Scan(&identifier, &outcome, &message); err != nil {
			t.Fatal(err)
		}
		if outcome != "provider_failed" || strings.Contains(identifier, films) || strings.Contains(message, "secret") || strings.Contains(message, "https://") {
			t.Fatalf("unsafe provider observation = %q %q %q", identifier, outcome, message)
		}
	})

	t.Run("no configured provider is not unmatched", func(t *testing.T) {
		films, data := t.TempDir(), t.TempDir()
		writeMedia(t, filepath.Join(films, "Film.mp4"))
		db, c := openCatalog(t, data)
		defer db.Close()
		if err := c.SetRoots(films, ""); err != nil {
			t.Fatal(err)
		}
		if err := c.Scan(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if status := c.ScanStatus(); status.Unmatched != 0 || status.Failed != 0 {
			t.Fatalf("status = %#v", status)
		}
		if status := c.MetadataStatus(); status.State != "unavailable" || status.Configured {
			t.Fatalf("metadata status = %#v", status)
		}
	})

	t.Run("configured provider reports a running enrichment", func(t *testing.T) {
		films, data := t.TempDir(), t.TempDir()
		writeMedia(t, filepath.Join(films, "Film.mp4"))
		db, c := openCatalog(t, data)
		defer db.Close()
		provider := metadataBlockingProvider{started: make(chan struct{}), release: make(chan struct{})}
		c.SetProvider(provider)
		if err := c.SetTMDBToken("secret"); err != nil {
			t.Fatal(err)
		}
		if err := c.SetRoots(films, ""); err != nil {
			t.Fatal(err)
		}
		if err := c.StartScan(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		<-provider.started
		if status := c.MetadataStatus(); status.State != "running" || !status.Configured {
			t.Fatalf("metadata status = %#v", status)
		}
		close(provider.release)
	})

	t.Run("outage retains known cached metadata", func(t *testing.T) {
		films, data := t.TempDir(), t.TempDir()
		writeMedia(t, filepath.Join(films, "Known.mp4"))
		db, c := openCatalog(t, data)
		defer db.Close()
		c.SetProvider(outcomeProvider{lookup: func(kind, title string) (catalog.Enrichment, error) {
			return catalog.Enrichment{ProviderID: "known-id", Synopsis: "cached metadata"}, nil
		}})
		if err := c.SetTMDBToken("secret"); err != nil {
			t.Fatal(err)
		}
		if err := c.SetRoots(films, ""); err != nil {
			t.Fatal(err)
		}
		if err := c.Scan(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		initial, _, err := c.Browse("", 0, 10)
		if err != nil || len(initial) != 1 || initial[0].ProviderID != "known-id" || initial[0].Synopsis != "cached metadata" {
			t.Fatalf("initial items = %#v, %v", initial, err)
		}
		c.SetProvider(outcomeProvider{lookup: func(string, string) (catalog.Enrichment, error) { return catalog.Enrichment{}, errors.New("outage") }})
		writeMedia(t, filepath.Join(films, "New.mp4"))
		if err := c.Scan(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		items, _, err := c.Browse("", 0, 10)
		if err != nil || len(items) != 2 || items[0].ProviderID != "known-id" || items[0].Synopsis != "cached metadata" {
			t.Fatalf("items after outage = %#v, %v", items, err)
		}
		if status := c.ScanStatus(); status.Status != "partial" || status.Failed != 1 {
			t.Fatalf("status = %#v", status)
		}
	})
}

func openCatalog(t *testing.T, data string) (*sqlite.DB, *catalog.Catalog) {
	t.Helper()
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return db, c
}

func writeMedia(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(path), 0600); err != nil {
		t.Fatal(err)
	}
}

func scanOutcomes(t *testing.T, db *sqlite.DB, scanID string) map[string]int {
	t.Helper()
	rows, err := db.Query("SELECT outcome FROM scan_files WHERE scan_id=?", scanID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	outcomes := map[string]int{}
	for rows.Next() {
		var outcome string
		if err := rows.Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		outcomes[outcome]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return outcomes
}

func TestScanEnrichesFilmAndSeriesOnce(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("film"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Show/Show.S01E01.mp4", "Show/Show.S01E02.mp4"} {
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
	fake := &metadataFake{calls: map[string]int{}}
	c.SetProvider(fake)
	if err := c.SetTMDBToken("secret"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, _, err := c.Browse("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ProviderID != "film-id" || items[1].ProviderID != "series-id" || items[1].Synopsis != "series summary" || items[0].Poster != "/api/v1/catalog/artwork/"+items[0].ID+"/poster" {
		t.Fatalf("enriched browse items = %#v", items)
	}
	image, contentType, err := c.Artwork(items[0].ID, "poster")
	if err != nil || string(image) != "image:/poster.jpg" || contentType != "image/jpeg" {
		t.Fatalf("cached artwork = %q %q %v", image, contentType, err)
	}
	if fake.calls["film:Film"] != 1 || fake.calls["series:Show"] != 1 {
		t.Fatalf("provider calls = %#v", fake.calls)
	}
}

func TestNormalModeProviderSetupEnrichesUnchangedLibraryAndSurvivesRestart(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	writeMedia(t, filepath.Join(tv, "Show", "Season 01", "Show.S01E01.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, tv); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("local scan: %v", err)
	}
	before, err := c.List("", 0, 10)
	if err != nil || len(before) != 2 {
		t.Fatalf("local catalog = %#v, %v", before, err)
	}
	var filmID string
	for _, item := range before {
		if item.Kind == "film" {
			filmID = item.ID
		}
	}
	browse, _, err := c.Browse("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var seriesID string
	for _, item := range browse {
		if item.Kind == "series" {
			seriesID = item.ID
		}
	}
	seriesBefore, ok := c.Series(seriesID)
	if !ok || len(seriesBefore.Seasons) != 1 || len(seriesBefore.Seasons[0].Episodes) != 1 {
		t.Fatalf("local series = %#v", seriesBefore)
	}
	episodeID := seriesBefore.Seasons[0].Episodes[0].ID
	house, err := household.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := house.CreateProfile("Viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := house.Select(profile.ID, "")
	if err != nil || house.Progress(session, episodeID, 1234) != nil {
		t.Fatalf("save progress: %v", err)
	}
	fake := &metadataFake{calls: map[string]int{}}
	c.SetProvider(fake)
	if err := c.ValidateTMDBToken(context.Background(), "secret"); err != nil || c.SetTMDBToken("secret") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("configure and enrich: %v", err)
	}
	if got := fake.calls; got["film:Film"] != 1 || got["series:Show"] != 1 || got["episode:1:1"] != 1 {
		t.Fatalf("provider calls = %#v", got)
	}
	after, _, err := c.Browse("Film", 0, 10)
	if err != nil || len(after) != 1 || after[0].ID != filmID || after[0].ProviderID != "film-id" || after[0].Title != "film title" || after[0].Synopsis != "film summary" || after[0].Poster == "" {
		t.Fatalf("enriched film = %#v, %v", after, err)
	}
	seriesAfter, ok := c.Series(seriesBefore.ID)
	if !ok || seriesAfter.ProviderID != "series-id" || seriesAfter.Title != "series title" || seriesAfter.Synopsis != "series summary" {
		t.Fatalf("enriched series = %#v", seriesAfter)
	}
	episode := seriesAfter.Seasons[0].Episodes[0]
	if episode.ID != episodeID || episode.ProviderID != "episode-1-1" || episode.Title != "Episode 1" || episode.Synopsis != "episode summary" || episode.Backdrop == "" {
		t.Fatalf("enriched episode = %#v", episode)
	}
	if position, err := house.Position(session, episodeID); err != nil || position != 1234 {
		t.Fatalf("progress after enrichment = %d, %v", position, err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	reopened.SetProvider(outcomeProvider{lookup: func(string, string) (catalog.Enrichment, error) { return catalog.Enrichment{}, errors.New("offline") }})
	persisted, ok := reopened.Series(seriesBefore.ID)
	if !ok || persisted.Synopsis != "series summary" || persisted.Seasons[0].Episodes[0].Synopsis != "episode summary" {
		t.Fatalf("persisted series = %#v", persisted)
	}
	if data, contentType, err := reopened.Artwork(episodeID, "backdrop"); err != nil || len(data) == 0 || contentType != "image/jpeg" {
		t.Fatalf("persisted episode artwork = %q %q %v", data, contentType, err)
	}
}
