package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingFilmRetainsProfileListAcrossReappearance(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	root := t.TempDir()
	path := filepath.Join(root, "Film.mp4")
	require.NoError(t, os.WriteFile(path, []byte("film"), 0o600))
	library, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	require.NoError(t, err)
	require.NoError(t, library.SetRoots(root, ""))
	require.NoError(t, library.Scan(t.Context(), 1))
	items, _, err := library.Browse("", 0, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Ada", "")
	require.NoError(t, err)
	require.NoError(t, library.SetListed(profile.ID, "film", items[0].ID, true))
	assertListed := func(playable bool) {
		t.Helper()
		view, err := library.Viewer(profile.ID, "film")
		require.NoError(t, err)
		for _, section := range view.Sections {
			if section.Name != "My List" {
				continue
			}
			require.Len(t, section.Items, 1)
			assert.Equal(t, items[0].ID, section.Items[0].ID)
			assert.True(t, section.Items[0].Listed)
			assert.Equal(t, playable, section.Items[0].Playable)
			return
		}
		t.Fatal("viewer has no My List section")
	}
	assertListed(true)

	require.NoError(t, os.Remove(path))
	require.NoError(t, library.Scan(t.Context(), 1))
	var membership int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM profile_film_list WHERE profile_id=? AND catalog_id=?`, profile.ID, items[0].ID).Scan(&membership))
	require.Equal(t, 1, membership, "an unavailable physical file must retain the logical title's list membership")
	unavailable, ok := library.Item(items[0].ID)
	require.True(t, ok)
	assert.False(t, unavailable.Playable)
	assertListed(false)

	require.NoError(t, os.WriteFile(path, []byte("film"), 0o600))
	require.NoError(t, library.Scan(t.Context(), 1))
	restored, ok := library.Item(items[0].ID)
	require.True(t, ok)
	assert.True(t, restored.Playable)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM profile_film_list WHERE profile_id=? AND catalog_id=?`, profile.ID, items[0].ID).Scan(&membership))
	assert.Equal(t, 1, membership)
	assertListed(true)
}

func TestSetListedDoesNotHideDatabaseFailureAsMissingCatalog(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = library.SetListed("profile", "film", "film", true)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, catalog.ErrCatalogNotFound))
}

func TestViewerPreferenceSurvivesDatabaseReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Ada", "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	for media, preference := range map[string]catalog.ViewPreference{
		"all":    {View: "rows", Sort: "watched"},
		"film":   {View: "grid", Sort: "watched"},
		"series": {View: "grid", Sort: "year"},
	} {
		_, err = library.SavePreference(profile.ID, media, preference)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())

	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	library, err = catalog.Open(db)
	require.NoError(t, err)
	for media, expected := range map[string]catalog.ViewPreference{
		"all":    {View: "rows", Sort: "watched"},
		"film":   {View: "grid", Sort: "watched"},
		"series": {View: "grid", Sort: "year"},
	} {
		preference, err := library.Preference(profile.ID, media)
		require.NoError(t, err)
		assert.Equal(t, expected, preference, media)
	}
}
