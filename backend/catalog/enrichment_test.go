package catalog_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type legacyArtworkProvider struct {
	exactCalls   map[string]int
	episodeCalls int
	artworkCalls map[string]int
	exactErr     error
	failArtwork  map[string]bool
}

func (p *legacyArtworkProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{}, errors.New("unexpected title lookup")
}

func (p *legacyArtworkProvider) Candidates(context.Context, string, string, string, string, string) ([]catalog.Candidate, error) {
	return nil, nil
}

func (p *legacyArtworkProvider) ByID(_ context.Context, _, kind, providerID, _, _ string) (catalog.Enrichment, error) {
	p.exactCalls[kind+":"+providerID]++
	if p.exactErr != nil {
		return catalog.Enrichment{}, p.exactErr
	}
	switch kind {
	case "film":
		if providerID == "legacy-empty" {
			return catalog.Enrichment{ProviderID: providerID}, nil
		}
		return catalog.Enrichment{ProviderID: providerID, Poster: "/film-poster.jpg", Backdrop: "/film-backdrop.jpg"}, nil
	case "series":
		return catalog.Enrichment{ProviderID: providerID, Backdrop: "/locked-series-backdrop.jpg"}, nil
	default:
		return catalog.Enrichment{}, errors.New("unexpected exact lookup kind")
	}
}

func (p *legacyArtworkProvider) LookupEpisode(_ context.Context, _ string, seriesID string, season, episode int) (catalog.Enrichment, error) {
	p.episodeCalls++
	if p.exactErr != nil {
		return catalog.Enrichment{}, p.exactErr
	}
	if seriesID != "legacy-series" || season != 1 || episode != 1 {
		return catalog.Enrichment{}, fmt.Errorf("episode parent/number = %q S%02dE%02d", seriesID, season, episode)
	}
	return catalog.Enrichment{ProviderID: "legacy-episode", Backdrop: "/episode-still.jpg"}, nil
}

func (p *legacyArtworkProvider) FetchArtwork(_ context.Context, path string) (catalog.Artwork, error) {
	p.artworkCalls[path]++
	if p.failArtwork[path] && p.artworkCalls[path] == 1 {
		return catalog.Artwork{}, errors.New("artwork temporarily unavailable")
	}
	return catalog.Artwork{Bytes: []byte("legacy:" + path), ContentType: "image/jpeg"}, nil
}

func TestPre020MatchedArtworkIsReconciledOnceAfterUpgrade(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	writeMedia(t, filepath.Join(films, "No Artwork.mp4"))
	writeMedia(t, filepath.Join(tv, "Show", "Season 01", "Show.S01E01.mp4"))
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || c.SetRoots(films, tv) != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("seed local catalog: %v", err)
	}
	items := c.MetadataTargets()
	var filmID, emptyFilmID, seriesID string
	for _, item := range items {
		if item.Title == "Film" {
			filmID = item.ID
		} else if item.Title == "No Artwork" {
			emptyFilmID = item.ID
		} else if item.Kind == "series" {
			seriesID = item.ID
		}
	}
	series, ok := c.Series(seriesID)
	if !ok || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 1 {
		t.Fatalf("seed series = %#v", series)
	}
	episodeID := series.Seasons[0].Episodes[0].ID
	if _, err := db.Exec(`UPDATE catalog_items SET provider_id='legacy-film',poster='cached-film-poster',backdrop='',owner_matched=1 WHERE id=?`, filmID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_items SET provider_id='legacy-empty',poster='',backdrop='' WHERE id=?`, emptyFilmID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_series SET provider_id='legacy-series',poster='',backdrop='' WHERE id=?`, seriesID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_items SET provider_id='legacy-episode',poster='',backdrop='' WHERE id=?`, episodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('series',?,'backdrop','','owner',1)`, seriesID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('tmdb_token','secret') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Restore the authentic pre-020 schema state while retaining legacy matched
	// catalog rows, then exercise the embedded migration through sqlite.Open.
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(data, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE catalog_artwork_reconciliations; DROP TABLE catalog_artwork_retries; DELETE FROM schema_migrations WHERE version=20`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err = catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	provider := &legacyArtworkProvider{exactCalls: map[string]int{}, artworkCalls: map[string]int{}, failArtwork: map[string]bool{"/film-backdrop.jpg": true}}
	c.SetProvider(provider)
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	film, ok := c.Item(filmID)
	if !ok || !film.OwnerMatch || film.Poster != "cached-film-poster" || film.Backdrop != "" || c.ScanStatus().Status != "partial" {
		t.Fatalf("reconciled legacy film = %#v", film)
	}
	series, ok = c.Series(seriesID)
	if !ok || series.Backdrop != "" || series.Seasons[0].Episodes[0].Backdrop == "" {
		t.Fatalf("reconciled legacy series = %#v", series)
	}
	if provider.artworkCalls["/film-poster.jpg"] != 0 || provider.artworkCalls["/locked-series-backdrop.jpg"] != 0 {
		t.Fatalf("cached or locked artwork fetched = %#v", provider.artworkCalls)
	}
	if provider.exactCalls["film:legacy-film"] != 1 || provider.exactCalls["film:legacy-empty"] != 1 || provider.exactCalls["series:legacy-series"] != 1 || provider.episodeCalls != 1 {
		t.Fatalf("legacy exact requests = %#v episodes=%d", provider.exactCalls, provider.episodeCalls)
	}
	var retries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_artwork_retries WHERE catalog_kind='film' AND catalog_id=? AND artwork_kind='backdrop'`, filmID).Scan(&retries); err != nil || retries != 1 {
		t.Fatalf("legacy failed-artwork retry = %d, %v", retries, err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	film, ok = c.Item(filmID)
	if !ok || film.Backdrop == "" || c.ScanStatus().Status != "complete" || provider.artworkCalls["/film-backdrop.jpg"] != 2 {
		t.Fatalf("retried legacy artwork = %#v calls=%#v status=%#v", film, provider.artworkCalls, c.ScanStatus())
	}
	if provider.exactCalls["film:legacy-film"] != 1 || provider.exactCalls["film:legacy-empty"] != 1 || provider.exactCalls["series:legacy-series"] != 1 || provider.episodeCalls != 1 {
		t.Fatalf("completed reconciliation repeated provider requests = %#v episodes=%d", provider.exactCalls, provider.episodeCalls)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if provider.exactCalls["film:legacy-film"] != 1 || provider.exactCalls["film:legacy-empty"] != 1 || provider.exactCalls["series:legacy-series"] != 1 || provider.episodeCalls != 1 || provider.artworkCalls["/film-backdrop.jpg"] != 2 {
		t.Fatalf("settled artwork made more provider requests: exact=%#v artwork=%#v episodes=%d", provider.exactCalls, provider.artworkCalls, provider.episodeCalls)
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_artwork_reconciliations`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("reconciliation queue = %d, %v", pending, err)
	}
}

func TestLegacyArtworkReconciliationSurvivesTemporaryProviderUnavailability(t *testing.T) {
	// This covers an unavailable exact metadata request while the local scan is
	// healthy. The separate offline acceptance flow covers total network loss.
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("seed local catalog: %v", err)
	}
	filmID := c.MetadataTargets()[0].ID
	if _, err := db.Exec(`UPDATE catalog_items SET provider_id='legacy-film',poster='',backdrop='' WHERE id=?`, filmID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_artwork_reconciliations(catalog_kind,catalog_id,provider_id) VALUES('film',?,'legacy-film')`, filmID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('tmdb_token','secret') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
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
	provider := &legacyArtworkProvider{exactCalls: map[string]int{}, artworkCalls: map[string]int{}, exactErr: errors.New("provider temporarily unavailable")}
	reopened.SetProvider(provider)
	if err := reopened.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if status := reopened.ScanStatus(); status.Status != "partial" || status.Failed != 1 {
		t.Fatalf("provider-unavailable scan status = %#v", status)
	}
	provider.exactErr = nil
	if err := reopened.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	film, ok := reopened.Item(filmID)
	if !ok || film.Backdrop == "" || provider.exactCalls["film:legacy-film"] != 2 {
		t.Fatalf("reconciled after provider recovery = %#v calls=%#v", film, provider.exactCalls)
	}
	if err := reopened.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if provider.exactCalls["film:legacy-film"] != 2 {
		t.Fatalf("completed recovery repeated exact request: %#v", provider.exactCalls)
	}
}

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

type retryEpisodeArtworkProvider struct {
	episodeCalls int
	exactCalls   map[string]int
	artworkCalls map[string]int
}

type staleArtworkProvider struct {
	fetchCalls int
}

func (p *staleArtworkProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "same-id", Title: "Automatic title", Poster: "/automatic-poster.jpg"}, nil
}

func (p *staleArtworkProvider) Candidates(context.Context, string, string, string, string, string) ([]catalog.Candidate, error) {
	return nil, nil
}

func (p *staleArtworkProvider) ByID(context.Context, string, string, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "same-id", Title: "Owner title"}, nil
}

func (p *staleArtworkProvider) FetchArtwork(context.Context, string) (catalog.Artwork, error) {
	p.fetchCalls++
	if p.fetchCalls == 1 {
		return catalog.Artwork{}, errors.New("temporary artwork outage")
	}
	return catalog.Artwork{Bytes: []byte("stale automatic artwork"), ContentType: "image/jpeg"}, nil
}

type staleEpisodeArtworkProvider struct {
	staleArtworkProvider
}

type ownerMatchArtworkRetryProvider struct {
	fetchCalls int
}

func (p *ownerMatchArtworkRetryProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{}, nil
}

func (p *ownerMatchArtworkRetryProvider) Candidates(context.Context, string, string, string, string, string) ([]catalog.Candidate, error) {
	return nil, nil
}

func (p *ownerMatchArtworkRetryProvider) ByID(context.Context, string, string, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "selected-id", Title: "Owner selection", Poster: "/selected-poster.jpg"}, nil
}

func (p *ownerMatchArtworkRetryProvider) FetchArtwork(context.Context, string) (catalog.Artwork, error) {
	p.fetchCalls++
	if p.fetchCalls == 1 {
		return catalog.Artwork{}, errors.New("temporary artwork outage")
	}
	return catalog.Artwork{Bytes: []byte("selected poster"), ContentType: "image/jpeg"}, nil
}

func (p *staleEpisodeArtworkProvider) Lookup(_ context.Context, _ string, kind, _ string) (catalog.Enrichment, error) {
	if kind == "series" {
		return catalog.Enrichment{ProviderID: "same-series-id", Title: "Automatic series"}, nil
	}
	return catalog.Enrichment{}, nil
}

func (p *staleEpisodeArtworkProvider) ByID(context.Context, string, string, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "same-series-id"}, nil
}

func (p *staleEpisodeArtworkProvider) LookupEpisode(context.Context, string, string, int, int) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "episode-id", Title: "Automatic episode", Backdrop: "/automatic-still.jpg"}, nil
}

func (p *retryEpisodeArtworkProvider) Lookup(_ context.Context, _ string, kind, _ string) (catalog.Enrichment, error) {
	return p.enrichment(kind), nil
}

func (p *retryEpisodeArtworkProvider) Candidates(context.Context, string, string, string, string, string) ([]catalog.Candidate, error) {
	return nil, nil
}

func (p *retryEpisodeArtworkProvider) ByID(_ context.Context, _, kind, _, _, _ string) (catalog.Enrichment, error) {
	if p.exactCalls == nil {
		p.exactCalls = map[string]int{}
	}
	p.exactCalls[kind]++
	enrichment := p.enrichment(kind)
	enrichment.Title = "Updated " + enrichment.Title
	return enrichment, nil
}

func (p *retryEpisodeArtworkProvider) enrichment(kind string) catalog.Enrichment {
	switch kind {
	case "film":
		return catalog.Enrichment{ProviderID: "film-id", Title: "Accurate Film", Poster: "/film-poster.jpg", Backdrop: "/film-backdrop.jpg"}
	case "series":
		return catalog.Enrichment{ProviderID: "series-id", Title: "Accurate Show", Poster: "/series-poster.jpg", Backdrop: "/series-backdrop.jpg"}
	}
	return catalog.Enrichment{}
}

func (p *retryEpisodeArtworkProvider) LookupEpisode(context.Context, string, string, int, int) (catalog.Enrichment, error) {
	p.episodeCalls++
	return catalog.Enrichment{ProviderID: "episode-id", Title: "Accurate Episode", Poster: "/episode-poster.jpg", Backdrop: "/still.jpg"}, nil
}

func (p *retryEpisodeArtworkProvider) FetchArtwork(_ context.Context, imagePath string) (catalog.Artwork, error) {
	if p.artworkCalls == nil {
		p.artworkCalls = map[string]int{}
	}
	p.artworkCalls[imagePath]++
	call := p.artworkCalls[imagePath]
	if (strings.Contains(imagePath, "backdrop") || strings.Contains(imagePath, "still")) && call == 1 {
		return catalog.Artwork{}, errors.New("temporary artwork outage")
	}
	return catalog.Artwork{Bytes: []byte(fmt.Sprintf("%s:%d", imagePath, call)), ContentType: "image/jpeg"}, nil
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
		if status := c.MetadataStatus(); status.State != "unavailable" || !status.Configured || !strings.Contains(status.Message, "retry the scan") {
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

func TestTMDBRateLimitStopsAfterOneRetryAndReportsFailedStatus(t *testing.T) {
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer provider.Close()
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(catalog.NewTMDBWithOrigins(provider.Client(), provider.URL, provider.URL))
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("rate-limited scan: %v", err)
	}
	if requests != 2 {
		t.Fatalf("provider requests = %d, want one request and one retry", requests)
	}
	if status := c.MetadataStatus(); status.State != "rate_limited" || !status.Configured || !strings.Contains(status.Message, "retry the scan later") {
		t.Fatalf("metadata status = %#v", status)
	}
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
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if fake.calls["film:Film"] != 1 || fake.calls["series:Show"] != 1 || fake.calls["episode:1:1"] != 1 || fake.calls["episode:1:2"] != 1 {
		t.Fatalf("unchanged provider calls = %#v", fake.calls)
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

func TestUnchangedProviderMatchesRetryEachMissingArtworkField(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	provider := &retryEpisodeArtworkProvider{}
	c.SetProvider(provider)
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, tv) != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("first scan: %v", err)
	}
	items, _, err := c.Browse("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("first browse = %#v, %v", items, err)
	}
	var film, seriesSummary catalog.Item
	for _, item := range items {
		if item.Kind == "film" {
			film = item
		} else if item.Kind == "series" {
			seriesSummary = item
		}
	}
	if film.Title != "Accurate Film" || film.Poster == "" || film.Backdrop != "" || seriesSummary.Title != "Accurate Show" || seriesSummary.Poster == "" || seriesSummary.Backdrop != "" {
		t.Fatalf("first artwork = film %#v, series %#v", film, seriesSummary)
	}
	if _, err := c.EditMetadata("film", film.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{
		{Field: "title", Value: "Owner Film", Source: "owner", Locked: true},
		{Field: "poster", Value: film.Poster, Source: "owner", Locked: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EditMetadata("series", seriesSummary.ID, catalog.MetadataEdit{Fields: []catalog.MetadataField{
		{Field: "title", Value: "Owner Show", Source: "owner", Locked: true},
		{Field: "poster", Value: seriesSummary.Poster, Source: "owner", Locked: true},
	}}); err != nil {
		t.Fatal(err)
	}
	seriesID := seriesSummary.ID
	series, ok := c.Series(seriesID)
	if !ok || series.Seasons[0].Episodes[0].Backdrop != "" || c.ScanStatus().Status != "partial" || c.ScanStatus().Failed != 3 {
		t.Fatalf("first enrichment = %#v status=%#v", series, c.ScanStatus())
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
	c = reopened
	c.SetProvider(provider)
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	series, ok = c.Series(seriesID)
	if !ok || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 1 {
		t.Fatalf("retried series = %#v", series)
	}
	episode := series.Seasons[0].Episodes[0]
	items, _, err = c.Browse("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == "film" {
			film = item
		} else if item.Kind == "series" {
			seriesSummary = item
		}
	}
	if film.Title != "Owner Film" || film.Poster == "" || film.Backdrop == "" || seriesSummary.Title != "Owner Show" || seriesSummary.Poster == "" || seriesSummary.Backdrop == "" || episode.Title != "Accurate Episode" || episode.Poster == "" || episode.Backdrop == "" || provider.episodeCalls != 1 || c.ScanStatus().Status != "complete" {
		t.Fatalf("retried enrichment = %#v calls=%d status=%#v", series, provider.episodeCalls, c.ScanStatus())
	}
	for _, check := range []struct{ id, kind, want string }{
		{film.ID, "poster", "/film-poster.jpg:1"},
		{film.ID, "backdrop", "/film-backdrop.jpg:2"},
		{series.ID, "poster", "/series-poster.jpg:1"},
		{series.ID, "backdrop", "/series-backdrop.jpg:2"},
		{episode.ID, "poster", "/episode-poster.jpg:1"},
		{episode.ID, "backdrop", "/still.jpg:2"},
	} {
		if data, _, err := c.Artwork(check.id, check.kind); err != nil || string(data) != check.want {
			t.Fatalf("%s %s artwork = %q, %v", check.id, check.kind, data, err)
		}
	}
	for _, imagePath := range []string{"/film-poster.jpg", "/series-poster.jpg", "/episode-poster.jpg"} {
		if provider.artworkCalls[imagePath] != 1 {
			t.Fatalf("cached %s fetched %d times", imagePath, provider.artworkCalls[imagePath])
		}
	}
	if provider.exactCalls["film"] != 0 || provider.exactCalls["series"] != 0 {
		t.Fatalf("pending artwork refetched metadata: %#v", provider.exactCalls)
	}
}

func TestScanFailsWhenArtworkRetryCannotBeRecorded(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(&retryEpisodeArtworkProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_artwork_retry_insert BEFORE INSERT ON catalog_artwork_retries BEGIN SELECT RAISE(ABORT, 'forced retry insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "remember backdrop artwork retry") {
		t.Fatalf("scan error = %v", err)
	}
	if status := c.ScanStatus(); status.Status != "failed" || !strings.Contains(status.Message, "forced retry insert failure") {
		t.Fatalf("scan status = %#v", status)
	}
}

func TestOwnerRematchClearsPendingArtworkForSameProviderID(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	provider := &staleArtworkProvider{}
	c.SetProvider(provider)
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if item.ProviderID != "same-id" || item.Poster != "" || c.ScanStatus().Status != "partial" {
		t.Fatalf("initial metadata = %#v, status=%#v", item, c.ScanStatus())
	}
	if _, err := c.Match(context.Background(), "film", item.ID, "same-id", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	item = c.MetadataTargets()[0]
	if !item.OwnerMatch || item.Poster != "" || provider.fetchCalls != 1 {
		t.Fatalf("old pending artwork applied after owner rematch: %#v, fetches=%d", item, provider.fetchCalls)
	}
}

func TestOwnerMatchRetriesOfferedArtworkAfterTransientFailure(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	provider := &ownerMatchArtworkRetryProvider{}
	c.SetProvider(provider)
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	matched, err := c.Match(context.Background(), "film", item.ID, "selected-id", "en", "GB")
	if err != nil {
		t.Fatal(err)
	}
	if !matched.OwnerMatch || matched.Poster != "" || provider.fetchCalls != 1 {
		t.Fatalf("matched item = %#v, fetches=%d", matched, provider.fetchCalls)
	}
	var retries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_artwork_retries WHERE catalog_kind='film' AND catalog_id=? AND provider_id='selected-id' AND artwork_kind='poster'`, item.ID).Scan(&retries); err != nil || retries != 1 {
		t.Fatalf("match retries = %d, %v", retries, err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	matched = c.MetadataTargets()[0]
	if !matched.OwnerMatch || matched.Poster == "" || provider.fetchCalls != 2 || c.ScanStatus().Status != "complete" {
		t.Fatalf("retried match = %#v, fetches=%d, status=%#v", matched, provider.fetchCalls, c.ScanStatus())
	}
}

func TestOwnerSeriesRematchClearsChildEpisodeArtworkRetries(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	provider := &staleEpisodeArtworkProvider{}
	c.SetProvider(provider)
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots("", tv) != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	seriesSummary := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "series", seriesSummary.ID, "same-series-id", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	series, ok := c.Series(seriesSummary.ID)
	if !ok || series.Seasons[0].Episodes[0].Backdrop != "" || provider.fetchCalls != 1 {
		t.Fatalf("old episode artwork applied after series rematch: %#v, fetches=%d", series, provider.fetchCalls)
	}
}

func TestArtworkRetryCompletionIsAtomic(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	provider := &retryEpisodeArtworkProvider{}
	c.SetProvider(provider)
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, tv) != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_artwork_retry_completion BEFORE DELETE ON catalog_artwork_retries BEGIN SELECT RAISE(ABORT, 'forced retry failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if status := c.ScanStatus(); status.Status != "partial" || status.Failed != 3 {
		t.Fatalf("failed completion status = %#v", status)
	}
	var retries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_artwork_retries`).Scan(&retries); err != nil || retries != 3 {
		t.Fatalf("pending retries = %d, %v", retries, err)
	}
	items, _, err := c.Browse("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Poster == "" {
			t.Fatalf("cached poster was lost: %#v", item)
		}
		if bytes, _, err := c.Artwork(item.ID, "poster"); err != nil || !strings.HasSuffix(string(bytes), ":1") {
			t.Fatalf("cached poster changed: %q, %v", bytes, err)
		}
	}
	if _, err := db.Exec(`DROP TRIGGER reject_artwork_retry_completion`); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil || c.ScanStatus().Status != "complete" {
		t.Fatalf("retry after database recovery: %v, status=%#v", err, c.ScanStatus())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_artwork_retries`).Scan(&retries); err != nil || retries != 0 {
		t.Fatalf("completed retries = %d, %v", retries, err)
	}
}

func TestCompletedArtworkRetrySurvivesLaterScanPersistenceFailure(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	provider := &retryEpisodeArtworkProvider{}
	c.SetProvider(provider)
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_later_scan_persist BEFORE UPDATE OF title ON catalog_items BEGIN SELECT RAISE(ABORT, 'forced scan persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "forced scan persistence failure") {
		t.Fatalf("retry scan error = %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_later_scan_persist`); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	if item.Backdrop == "" || provider.artworkCalls["/film-backdrop.jpg"] != 2 {
		t.Fatalf("completed retry lost after later persistence failure: %#v, calls=%#v", item, provider.artworkCalls)
	}
	if data, _, err := c.Artwork(item.ID, "backdrop"); err != nil || string(data) != "/film-backdrop.jpg:2" {
		t.Fatalf("completed backdrop = %q, %v", data, err)
	}
}
