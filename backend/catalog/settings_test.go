package catalog_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestTMDBTokenSurvivesRestartWithoutCatalogLeak(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(_ context.Context, _ *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetTMDBToken("tmdb-secret"); err != nil {
		t.Fatal(err)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(_ context.Context, _ *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil || !reopened.TMDBConfigured() {
		t.Fatalf("configured after restart = %v, %v", reopened.TMDBConfigured(), err)
	}
	data, err := json.Marshal(reopened.ScanStatus())
	if err != nil || string(data) == "" || string(data) == "tmdb-secret" {
		t.Fatalf("token leaked in public state: %s", data)
	}
}
