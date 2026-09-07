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

func TestSetRootsRollsBackBothDatabaseAndMemoryOnWriteFailure(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prober := catalog.ProberFunc(func(_ context.Context, _ *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	})
	c, err := catalog.OpenWithProber(db, prober)
	if err != nil {
		t.Fatal(err)
	}
	oldFilms, oldTV, newFilms, newTV := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := c.SetRoots(oldFilms, oldTV); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_tv_root BEFORE INSERT ON settings WHEN NEW.key='tv_root' BEGIN SELECT RAISE(ABORT, 'tv root rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(newFilms, newTV); err == nil {
		t.Fatal("accepted partial root update")
	}
	if films, tv := c.Roots(); films != oldFilms || tv != oldTV {
		t.Fatalf("memory roots = %q %q", films, tv)
	}
	reopened, err := catalog.OpenWithProber(db, prober)
	if err != nil {
		t.Fatal(err)
	}
	if films, tv := reopened.Roots(); films != oldFilms || tv != oldTV {
		t.Fatalf("persisted roots = %q %q", films, tv)
	}
}
