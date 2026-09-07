package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

type ownerMatchProvider struct {
	outage bool
	poster string
}

func (p ownerMatchProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	if p.outage {
		return catalog.Enrichment{}, errors.New("offline")
	}
	return catalog.Enrichment{}, nil
}
func (p ownerMatchProvider) Candidates(context.Context, string, string, string, string, string) ([]catalog.Candidate, error) {
	return []catalog.Candidate{{Provider: "tmdb", ID: "42", Title: "The Right Film", Year: 2024, Confidence: 1}}, nil
}
func (p ownerMatchProvider) ByID(context.Context, string, string, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "42", Year: 2024, Synopsis: "owner choice", Poster: "/poster"}, nil
}
func (p ownerMatchProvider) FetchArtwork(context.Context, string) (catalog.Artwork, error) {
	return catalog.Artwork{Bytes: []byte(p.poster), ContentType: "image/jpeg"}, nil
}

func TestOwnerMatchSurvivesRescanOutageAndRestart(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	writeMedia(t, path)
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	queue := c.Unmatched()
	if len(queue) != 1 {
		t.Fatalf("unmatched queue = %#v", queue)
	}
	candidates, err := c.Candidates(context.Background(), "film", queue[0].ID, "", "en", "GB")
	if err != nil || len(candidates) != 1 || candidates[0].ID != "42" {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	matched, err := c.Match(context.Background(), "film", queue[0].ID, "42", "en", "GB")
	if err != nil || !matched.OwnerMatch || matched.ProviderID != "42" || matched.Language != "en" || matched.Region != "GB" {
		t.Fatalf("match = %#v, %v", matched, err)
	}
	writeMedia(t, filepath.Join(films, "Other.mp4"))
	c.SetProvider(ownerMatchProvider{outage: true})
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, _, err := c.Browse("", 0, 1)
	if err != nil || len(items) != 1 || items[0].ProviderID != "42" || !items[0].OwnerMatch {
		t.Fatalf("rescan = %#v, %v", items, err)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := reopened.Browse("", 0, 1)
	if err != nil || len(persisted) != 1 || persisted[0].ProviderID != "42" || !persisted[0].OwnerMatch {
		t.Fatalf("restart = %#v, %v", persisted, err)
	}
}

func TestOwnerUnmatchBlocksAutomaticRescan(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.Unmatched()[0]
	if _, err := c.Unmatch("film", item.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, _, err := c.Browse("", 0, 1)
	if err != nil || len(items) != 1 || items[0].ProviderID != "" || !items[0].OwnerUnmatch {
		t.Fatalf("automatic rematch = %#v, %v", items, err)
	}
}

func TestMatchDoesNotMutateMemoryWhenPersistenceFails(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err == nil {
		t.Fatal("closed database accepted match")
	}
	if got := c.MetadataTargets()[0]; got.ProviderID != "" || got.OwnerMatch {
		t.Fatalf("failed write changed memory: %#v", got)
	}
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p blockingProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	close(p.started)
	<-p.release
	return catalog.Enrichment{}, nil
}

func TestMetadataRepairWaitsForConcurrentScan(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	writeMedia(t, path)
	db, c := openCatalog(t, data)
	defer db.Close()
	if err := c.SetRoots(films, ""); err != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	p := blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	c.SetProvider(p)
	if err := c.SetTMDBToken("secret"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.StartScan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not reach provider")
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Unmatch("film", item.ID); !errors.Is(err, catalog.ErrMetadataBusy) {
		t.Fatalf("concurrent repair error = %v", err)
	}
	close(p.release)
	for deadline := time.Now().Add(time.Second); c.ScanStatus().Status == "running" && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	if c.ScanStatus().Status == "running" {
		t.Fatal("scan did not finish")
	}
}

func TestOwnerSeriesMatchSurvivesRestart(t *testing.T) {
	tv, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots("", tv) != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	var series catalog.Item
	for _, item := range c.MetadataTargets() {
		if item.Kind == "series" {
			series = item
		}
	}
	if series.ID == "" {
		t.Fatal("missing series target")
	}
	if _, err := c.Match(context.Background(), "series", series.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range reopened.MetadataTargets() {
		if item.ID == series.ID && item.OwnerMatch && item.ProviderID == "42" {
			found = true
		}
	}
	if !found {
		t.Fatal("series owner match did not persist")
	}
}

type busyMatchProvider struct {
	ownerMatchProvider
	started chan struct{}
	release chan struct{}
}

func (p busyMatchProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	close(p.started)
	<-p.release
	return catalog.Enrichment{}, nil
}

func TestBusyMatchDoesNotReplaceExistingArtwork(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	writeMedia(t, path)
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "old"})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); err != nil {
		t.Fatal(err)
	}
	before, _, err := c.Artwork(item.ID, "poster")
	if err != nil || string(before) != "old" {
		t.Fatalf("initial artwork = %q, %v", before, err)
	}
	p := busyMatchProvider{ownerMatchProvider: ownerMatchProvider{poster: "new"}, started: make(chan struct{}), release: make(chan struct{})}
	c.SetProvider(p)
	writeMedia(t, filepath.Join(films, "Other.mp4"))
	if err := c.StartScan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	<-p.started
	if _, err := c.Match(context.Background(), "film", item.ID, "42", "en", "GB"); !errors.Is(err, catalog.ErrMetadataBusy) {
		t.Fatalf("busy match = %v", err)
	}
	after, _, err := c.Artwork(item.ID, "poster")
	if err != nil || string(after) != "old" {
		t.Fatalf("busy match replaced artwork = %q, %v", after, err)
	}
	close(p.release)
}

func TestCancelledMatchDoesNotApply(t *testing.T) {
	films, data := t.TempDir(), t.TempDir()
	writeMedia(t, filepath.Join(films, "Film.mp4"))
	db, c := openCatalog(t, data)
	defer db.Close()
	c.SetProvider(ownerMatchProvider{poster: "new"})
	if err := c.SetTMDBToken("secret"); err != nil || c.SetRoots(films, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	item := c.MetadataTargets()[0]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Match(ctx, "film", item.ID, "42", "en", "GB"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled match = %v", err)
	}
	if got := c.MetadataTargets()[0]; got.ProviderID != "" {
		t.Fatalf("cancelled match changed metadata: %#v", got)
	}
}
