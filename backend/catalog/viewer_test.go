package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewerPageCancellationWhileWriterBusy(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	library, err := catalog.Open(db)
	require.NoError(t, err)
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Cancellation", "")
	require.NoError(t, err)
	connection, err := db.Writer().Conn(t.Context())
	require.NoError(t, err)
	defer connection.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := library.ViewerPage(ctx, profile.ID, "all", "", "", 48, access.Unrestricted())
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("viewer page did not cancel while waiting for the writer")
	}
}

func TestMissingFilmRetainsProfileListAcrossReappearance(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	root := t.TempDir()
	path := filepath.Join(root, "Film.mp4")
	require.NoError(t, os.WriteFile(path, []byte("film"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Keep.mp4"), []byte("keep"), 0o600))
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

func TestViewerCursorAndFreshPageRemainUsableAfterRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c"} {
		_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,local_only,root_kind,updated_at,genres_json) VALUES(?, 'film', ?, ?, 1, 'film', 0, '[]')`, id, id, id+".mp4")
		require.NoError(t, err)
	}
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Restart", "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	_, err = library.SavePreference(profile.ID, "film", catalog.ViewPreference{View: "grid", Sort: "title"})
	require.NoError(t, err)
	first, err := library.ViewerPage(t.Context(), profile.ID, "film", "", "", 1, access.Unrestricted())
	require.NoError(t, err)
	require.Equal(t, "a", first.Items[0].ID)
	require.NotEmpty(t, first.NextCursor)
	require.NoError(t, db.Close())

	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	library, err = catalog.Open(db)
	require.NoError(t, err)
	second, err := library.ViewerPage(t.Context(), profile.ID, "film", "", first.NextCursor, 1, access.Unrestricted())
	require.NoError(t, err)
	require.Equal(t, "b", second.Items[0].ID)
	fresh, err := library.ViewerPage(t.Context(), profile.ID, "film", "", "", 1, access.Unrestricted())
	require.NoError(t, err)
	require.Equal(t, "a", fresh.Items[0].ID)
}

func TestViewerCursorPreservesSQLiteUnicodeOrder(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	for _, title := range []string{"Éclair", "Örn", "Zulu"} {
		_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,genres_json,year) VALUES(?, 'film', ?, ?, '[]', 2020)`, title, title, title+".mp4")
		require.NoError(t, err)
	}
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Unicode", "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	for _, mode := range []string{"title", "year"} {
		_, err := library.SavePreference(profile.ID, "film", catalog.ViewPreference{View: "grid", Sort: mode})
		require.NoError(t, err)
		cursor := ""
		var titles []string
		for range 3 {
			page, err := library.ViewerPage(t.Context(), profile.ID, "film", "", cursor, 1, access.Unrestricted())
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			titles = append(titles, page.Items[0].Title)
			cursor = page.NextCursor
		}
		require.Equal(t, []string{"Zulu", "Éclair", "Örn"}, titles, mode)
		require.Empty(t, cursor, mode)
	}
}

func TestViewerGenresFindAllowedTitleAfterDeniedDescriptors(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	for index := range 110 {
		genre := fmt.Sprintf("Genre %03d", index)
		_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,genres_json) VALUES(?, 'film', ?, ?, ?)`, genre, genre, genre+".mp4", fmt.Sprintf(`[%q]`, genre))
		require.NoError(t, err)
		if index < 109 {
			_, err = db.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film',?,'tags','hidden','local',1)`, genre)
			require.NoError(t, err)
		}
	}
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Genres", "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	page, err := library.ViewerPage(t.Context(), profile.ID, "film", "", "", 2, access.Policy{DenyTags: []string{"hidden"}})
	require.NoError(t, err)
	var names []string
	for _, section := range page.Sections {
		names = append(names, section.Name)
	}
	require.Contains(t, names, "Genre 109")
	require.NotContains(t, names, "Genre 000")
}

func TestViewerPagePolicyMatchesAccessContent(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,genres_json) VALUES('film','film','Family','film.mp4','["Été"]')`)
	require.NoError(t, err)
	for field, value := range map[string]string{"tags": " Comédie , Family ", "content_rating": " TV-14 "} {
		_, err := db.Exec(`INSERT INTO catalog_metadata_fields(catalog_kind,catalog_id,field,value,source,locked) VALUES('film','film',?,?,'local',1)`, field, value)
		require.NoError(t, err)
	}
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Parity", "")
	require.NoError(t, err)
	library, err := catalog.Open(db)
	require.NoError(t, err)
	content, found, err := library.AccessContent("film", "film")
	require.NoError(t, err)
	require.True(t, found)
	for name, policy := range map[string]access.Policy{
		"unrestricted":           access.Unrestricted(),
		"unicode genre allow":    {AllowTags: []string{"été"}},
		"unicode metadata allow": {AllowTags: []string{"comédie"}},
		"unicode deny":           {DenyTags: []string{"COMÉDIE"}},
		"unrated denied":         {RatingRegion: "US", MaxRating: "PG-13", Unrated: access.UnratedDeny},
		"rating allowed":         {RatingRegion: "US", MaxRating: "TV-14", Unrated: access.UnratedDeny},
		"wrong library":          {LibraryIDs: []string{"not-present"}},
	} {
		t.Run(name, func(t *testing.T) {
			page, err := library.ViewerPage(t.Context(), profile.ID, "film", "New", "", 1, policy)
			require.NoError(t, err)
			require.Len(t, page.Sections, 1)
			assert.Equal(t, policy.Allows(content), len(page.Sections[0].Items) == 1)
		})
	}
}
