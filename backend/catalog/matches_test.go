package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

type ownerMatchProvider struct{ outage bool }

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
	return catalog.Enrichment{ProviderID: "42", Year: 2024, Synopsis: "owner choice"}, nil
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
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
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
