package catalog

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

type localArtworkProvider struct{ bytes []byte }

func (p localArtworkProvider) Lookup(context.Context, string, string, string) (Enrichment, error) {
	return Enrichment{ProviderID: "42", Title: "Provider", Poster: "poster"}, nil
}
func (p localArtworkProvider) FetchArtwork(context.Context, string) (Artwork, error) {
	return Artwork{Bytes: p.bytes, ContentType: "image/png"}, nil
}
func (p localArtworkProvider) Candidates(context.Context, string, string, string, string, string) ([]Candidate, error) {
	return nil, nil
}
func (p localArtworkProvider) ByID(context.Context, string, string, string, string, string) (Enrichment, error) {
	return Enrichment{ProviderID: "77", Title: "Matched without artwork"}, nil
}

func TestParseLocalNFOAllowsBuiltInEntitiesAndReportsUnsupportedFields(t *testing.T) {
	got, err := parseLocalNFO(context.Background(), "film", strings.NewReader(`<movie><title>Fish &amp; Chips</title><plot>Offline</plot><year>2024</year><mpaa>PG</mpaa><tag>Family</tag><genre>Comedy</genre><studio>Ignored</studio><uniqueid type="tmdb">42</uniqueid></movie>`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields["title"] != "Fish & Chips" || got.Fields["synopsis"] != "Offline" || got.Fields["year"] != "2024" || got.Fields["content_rating"] != "PG" || got.Fields["tags"] != "Family" || got.ProviderID != "42" {
		t.Fatalf("parsed = %#v", got)
	}
	if len(got.Ignored) != 2 || got.Ignored[0] != "genre" || got.Ignored[1] != "studio" {
		t.Fatalf("ignored = %#v", got.Ignored)
	}
}

func TestReadLocalArtworkRejectsUnsafeDimensionsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, maxArtworkWidth*4+1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "wide.png"), encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLocalArtwork(context.Background(), root, "wide.png"); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("wide artwork error=%v", err)
	}
	outside := filepath.Join(t.TempDir(), "poster.png")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.png")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := readLocalArtwork(context.Background(), root, "link.png"); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink artwork error=%v", err)
	}
}

func TestParseLocalNFORejectsDocumentTypesAndCustomEntities(t *testing.T) {
	for _, document := range []string{
		`<!DOCTYPE movie SYSTEM "https://example.invalid/remote.dtd"><movie><title>x</title></movie>`,
		`<!DOCTYPE movie [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><movie><title>&xxe;</title></movie>`,
	} {
		if _, err := parseLocalNFO(context.Background(), "film", strings.NewReader(document)); err == nil || !strings.Contains(err.Error(), "document type") {
			t.Fatalf("parse error = %v", err)
		}
	}
}

func TestParseLocalNFORejectsWrongRootAndConflictingProviderIDs(t *testing.T) {
	if _, err := parseLocalNFO(context.Background(), "episode", strings.NewReader(`<movie><title>x</title></movie>`)); err == nil {
		t.Fatal("accepted movie document for episode")
	}
	if _, err := parseLocalNFO(context.Background(), "film", strings.NewReader(`<movie><uniqueid type="tmdb">1</uniqueid><uniqueid type="tmdb">2</uniqueid></movie>`)); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting IDs error = %v", err)
	}
	if got, err := parseLocalNFO(context.Background(), "episode", strings.NewReader(`<episodedetails><title>Episode</title><plot>Plot</plot><aired>2025-03-01</aired><tag>One</tag></episodedetails>`)); err != nil || got.Fields["year"] != "2025" {
		t.Fatalf("episode = %#v, %v", got, err)
	}
}

func TestParseLocalNFORejectsMultipleRootsAndContentOutsideDocument(t *testing.T) {
	for _, document := range []string{`<movie><title>A</title></movie><movie><title>B</title></movie>`, `<movie><title>A</title></movie>junk`, `junk<movie><title>A</title></movie>`, `<movie/><!---->`, `<?outside?><movie/>`} {
		if _, err := parseLocalNFO(context.Background(), "film", strings.NewReader(document)); err == nil {
			t.Fatalf("accepted %q", document)
		}
	}
	if _, err := parseLocalNFO(context.Background(), "film", strings.NewReader(`<?xml version="1.0"?> <movie/>`)); err != nil {
		t.Fatalf("rejected XML declaration: %v", err)
	}
}

func TestLocalSidecarCandidatesGuardFlatMovieFolders(t *testing.T) {
	one := localSidecarCandidates("film", "Film/Film.mp4", map[string]int{"Film": 1})
	if !hasCandidate(one.nfo, "Film/Film.nfo") || !hasCandidate(one.nfo, "Film/movie.nfo") {
		t.Fatalf("single movie candidates = %#v", one)
	}
	flat := localSidecarCandidates("film", "Film.mp4", map[string]int{".": 2})
	if hasCandidate(flat.nfo, "movie.nfo") || hasCandidate(flat.poster, "poster.jpg") {
		t.Fatalf("flat folder used directory fallback: %#v", flat)
	}
	series := localSidecarCandidates("series", "Show", nil)
	if !hasCandidate(series.nfo, "Show/tvshow.nfo") || !hasCandidate(series.backdrop, "Show/fanart.jpg") {
		t.Fatalf("series candidates = %#v", series)
	}
}

func TestGroupedVersionAnchorsKeepIndependentLocalMetadataAcrossUngroup(t *testing.T) {
	c, db, films, _ := versionCatalogFixture(t)
	defer db.Close()
	var canonical, member string
	for id, item := range c.items {
		switch item.Title {
		case "Film 1080":
			canonical = id
		case "Film 4K":
			member = id
		}
	}
	if canonical == "" || member == "" {
		t.Fatalf("film ids=%q %q", canonical, member)
	}
	if _, err := c.CreateMediaVersionGroup(context.Background(), "film", canonical, []string{member}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(films, "Film 1080.nfo"), []byte(`<movie><title>Canonical Local</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(films, "Film 4K.nfo"), []byte(`<movie><title>Member Local</title></movie>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := c.items[canonical].Title; got != "Canonical Local" {
		t.Fatalf("canonical title=%q", got)
	}
	if got := c.items[member].Title; got != "Member Local" {
		t.Fatalf("member title=%q", got)
	}
	if _, err := c.UngroupMediaVersion(context.Background(), "film", canonical, member); err != nil {
		t.Fatal(err)
	}
	if got := c.items[member].Title; got != "Member Local" {
		t.Fatalf("ungrouped member title=%q", got)
	}
}

func TestPartialRetryProcessesSeriesNFOOnceWithoutTreatingAbsenceAsRemoval(t *testing.T) {
	tv := t.TempDir()
	show := filepath.Join(tv, "Show")
	if err := os.MkdirAll(show, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Show.S01E01.mp4", "Show.S01E02.mp4"} {
		if err := os.WriteFile(filepath.Join(show, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	nfo := filepath.Join(show, "tvshow.nfo")
	if err := os.WriteFile(nfo, []byte(`<tvshow><title>First</title></tvshow>`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { return MediaProperties{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots("", tv); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, []byte(`<tvshow><title>Retry Title</title></tvshow>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.startScan(context.Background(), 1, scanRequest{retry: map[string]bool{"tv-root\x00Show/Show.S01E02.mp4": true}}); err != nil {
		t.Fatal(err)
	}
	c.mu.RLock()
	done := c.done
	c.mu.RUnlock()
	<-done
	for _, series := range c.series {
		if series.Title != "Retry Title" {
			t.Fatalf("series after retry=%#v", series)
		}
	}
	if err := os.Remove(nfo); err != nil {
		t.Fatal(err)
	}
	if err := c.startScan(context.Background(), 1, scanRequest{retry: map[string]bool{"tv-root\x00Show/Show.S01E01.mp4": true}}); err != nil {
		t.Fatal(err)
	}
	c.mu.RLock()
	done = c.done
	c.mu.RUnlock()
	<-done
	for _, series := range c.series {
		if series.Title != "Retry Title" {
			t.Fatalf("partial absence removed metadata=%#v", series)
		}
	}
}

func TestProviderArtworkFallbackSurvivesLocalObjectCollectionAndRemoval(t *testing.T) {
	films := t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	providerBytes := testPNG(t, 3, 3)
	localBytes := testPNG(t, 4, 4)
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { return MediaProperties{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	c.SetProvider(localArtworkProvider{providerBytes})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	if err := os.WriteFile(filepath.Join(films, "Film-poster.png"), localBytes, 0600); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	var fallback string
	if err := db.QueryRow(`SELECT fallback_object_name FROM catalog_local_artwork WHERE catalog_kind='film' AND catalog_id=? AND artwork_kind='poster'`, item.ID).Scan(&fallback); err != nil || fallback == "" {
		t.Fatalf("fallback=%q %v", fallback, err)
	}
	c.artworkMu.Lock()
	for c.artworkObjectsDir != nil || fallback != "" {
		if err := c.cleanupArtworkObjectsLocked(); err != nil {
			c.artworkMu.Unlock()
			t.Fatal(err)
		}
		if c.artworkObjectsDir == nil {
			break
		}
	}
	c.artworkMu.Unlock()
	if _, err := os.Stat(filepath.Join(db.DataDir(), "artwork", "objects", fallback)); err != nil {
		t.Fatalf("fallback collected: %v", err)
	}
	if err := os.Remove(filepath.Join(films, "Film-poster.png")); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	got, _, err := c.Artwork(item.ID, "poster")
	if err != nil || !bytes.Equal(got, providerBytes) {
		t.Fatalf("restored provider artwork=%d %v", len(got), err)
	}
}

func TestFailedScanTransactionRemovesUnpublishedLocalArtworkObject(t *testing.T) {
	films := t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { return MediaProperties{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(db.DataDir(), "artwork", "objects")
	before, _ := os.ReadDir(dir)
	if err := os.WriteFile(filepath.Join(films, "Film-poster.png"), testPNG(t, 4, 4), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_local_artwork BEFORE UPDATE ON catalog_items BEGIN SELECT RAISE(FAIL,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err == nil {
		t.Fatal("scan succeeded through injected DB failure")
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("unpublished objects before=%d after=%d", len(before), len(after))
	}
	if got := c.MetadataTargets()[0]; got.Poster != "" {
		t.Fatalf("failed scan changed memory=%#v", got)
	}
}

func TestLockedCachedArtworkBytesCannotBeReplacedByLocalSidecar(t *testing.T) {
	films := t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	providerBytes, localBytes := testPNG(t, 3, 3), testPNG(t, 5, 5)
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { return MediaProperties{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	c.SetProvider(localArtworkProvider{providerBytes})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.EditMetadata("film", item.ID, MetadataEdit{Fields: []MetadataField{{Field: "poster", Value: item.Poster, Source: "owner", Locked: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(films, "Film-poster.png"), localBytes, 0600); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatal(err)
	}
	got, _, err := c.Artwork(item.ID, "poster")
	if err != nil || !bytes.Equal(got, providerBytes) {
		t.Fatalf("locked artwork bytes replaced=%d %v", len(got), err)
	}
	var localRows int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_local_artwork WHERE catalog_id=?`, item.ID).Scan(&localRows); err != nil || localRows != 0 {
		t.Fatalf("locked local rows=%d %v", localRows, err)
	}
}

func TestAcceptedIdentityChangeClearsHiddenArtworkFallback(t *testing.T) {
	changes := []struct {
		name  string
		apply func(*Catalog, string) error
	}{
		{"unmatch", func(c *Catalog, id string) error { _, err := c.Unmatch("film", id); return err }},
		{"match without artwork", func(c *Catalog, id string) error {
			_, err := c.Match(context.Background(), "film", id, "77", "", "")
			return err
		}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			films := t.TempDir()
			if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("media"), 0600); err != nil {
				t.Fatal(err)
			}
			providerBytes, localBytes := testPNG(t, 3, 3), testPNG(t, 5, 5)
			db, err := sqlite.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c, err := OpenWithProber(db, ProberFunc(func(context.Context, *os.File) (MediaProperties, error) { return MediaProperties{}, nil }))
			if err != nil {
				t.Fatal(err)
			}
			c.SetProvider(localArtworkProvider{providerBytes})
			if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
				t.Fatal(err)
			}
			item := c.MetadataTargets()[0]
			localPath := filepath.Join(films, "Film-poster.png")
			if err := os.WriteFile(localPath, localBytes, 0600); err != nil || c.Scan(context.Background(), 1) != nil {
				t.Fatal(err)
			}
			if err := change.apply(c, item.ID); err != nil {
				t.Fatal(err)
			}
			var fallback string
			if err := db.QueryRow(`SELECT fallback_object_name FROM catalog_local_artwork WHERE catalog_id=? AND artwork_kind='poster'`, item.ID).Scan(&fallback); err != nil || fallback != "" {
				t.Fatalf("fallback after identity change=%q %v", fallback, err)
			}
			if err := os.Remove(localPath); err != nil || c.Scan(context.Background(), 1) != nil {
				t.Fatal(err)
			}
			if _, _, err := c.Artwork(item.ID, "poster"); err == nil {
				t.Fatal("old provider artwork resurrected")
			}
		})
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func hasCandidate(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
