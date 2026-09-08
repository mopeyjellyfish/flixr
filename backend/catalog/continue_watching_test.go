package catalog_test

import (
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinueWatchingDismissalPersistsPerProfileWithoutDeletingViewerState(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	require.NoError(t, err)
	require.NoError(t, seedContinueWatchingCatalog(db))
	house, err := household.Open(db)
	require.NoError(t, err)
	one, err := house.CreateProfile("One", "")
	require.NoError(t, err)
	two, err := house.CreateProfile("Two", "")
	require.NoError(t, err)
	require.NoError(t, house.RecordProgress(one.ID, "film", 500, 1, false))
	require.NoError(t, house.RecordProgress(two.ID, "film", 500, 1, false))
	require.NoError(t, house.RecordViewingEvent(one.ID, household.ViewingEvent{CatalogID: "film", Title: "Film", Kind: "film", Type: household.EventCompleted, Provenance: household.ProvenanceLocal}))
	library, err := catalog.Open(db)
	require.NoError(t, err)
	require.NoError(t, library.SetListed(one.ID, "film", "film", true))

	require.NoError(t, library.SetContinueWatchingDismissed(one.ID, "film", "film", true))
	assertContinueWatchingIDs(t, library, one.ID)
	assertContinueWatchingIDs(t, library, two.ID, "film")
	assertViewerItemState(t, library, one.ID, "film", true, true)
	assertHistoryIDs(t, house, one.ID, "film")

	require.NoError(t, db.Close())
	db, err = sqlite.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	library, err = catalog.Open(db)
	require.NoError(t, err)
	house, err = household.Open(db)
	require.NoError(t, err)
	assertContinueWatchingIDs(t, library, one.ID)
	assertViewerItemState(t, library, one.ID, "film", true, true)
	assertHistoryIDs(t, house, one.ID, "film")

	require.NoError(t, library.SetContinueWatchingDismissed(one.ID, "film", "film", false))
	assertContinueWatchingIDs(t, library, one.ID, "film")
}

func assertHistoryIDs(t *testing.T, house *household.Manager, profileID string, expected ...string) {
	t.Helper()
	page, err := house.History(profileID, 25, "")
	require.NoError(t, err)
	ids := make([]string, 0, len(page.Events))
	for _, event := range page.Events {
		ids = append(ids, event.CatalogID)
	}
	assert.Equal(t, append([]string{}, expected...), ids)
}

func TestSeriesDismissalSurvivesEpisodeAdvancementUntilUserStartsViewing(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, seedContinueWatchingCatalog(db))
	house, err := household.Open(db)
	require.NoError(t, err)
	profile, err := house.CreateProfile("Ada", "")
	require.NoError(t, err)
	require.NoError(t, house.RecordProgress(profile.ID, "episode-1", 500, 1, false))
	library, err := catalog.Open(db)
	require.NoError(t, err)
	require.NoError(t, library.SetContinueWatchingDismissed(profile.ID, "series", "series", true))

	require.NoError(t, house.RecordProgress(profile.ID, "episode-2", 250, 2, false))
	require.NoError(t, library.AcceptContinueWatching(profile.ID, "episode-2", false))
	assertContinueWatchingIDs(t, library, profile.ID)

	require.NoError(t, library.AcceptContinueWatching(profile.ID, "episode-2", true))
	assertContinueWatchingIDs(t, library, profile.ID, "series")
}

func TestContinueWatchingDismissalsFollowIdentityMergeAndUnmerge(t *testing.T) {
	for _, kind := range []string{"film", "series"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db, err := sqlite.Open(dir)
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			if kind == "film" {
				_, err = db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,duration_ms) VALUES
					('a','film','A','a.mp4',1000),('b','film','B','b.mp4',1000)`)
			} else {
				_, err = db.Exec(`INSERT INTO catalog_series(id,title) VALUES('a','A'),('b','B');
					INSERT INTO catalog_items(id,kind,title,relative_path,duration_ms,series_id,season_id) VALUES
					('episode-a','episode','A1','a.mkv',1000,'a','season-a'),('episode-b','episode','B1','b.mkv',1000,'b','season-b')`)
			}
			require.NoError(t, err)
			house, err := household.Open(db)
			require.NoError(t, err)
			profiles := map[string]household.Profile{}
			for _, name := range []string{"source", "both", "survivor", "restore", "hide", "untouched"} {
				profiles[name], err = house.CreateProfile(name, "")
				require.NoError(t, err)
			}
			library, err := catalog.Open(db)
			require.NoError(t, err)
			for _, name := range []string{"source", "both", "restore"} {
				require.NoError(t, library.SetContinueWatchingDismissed(profiles[name].ID, kind, "b", true))
			}
			for _, name := range []string{"both", "survivor"} {
				require.NoError(t, library.SetContinueWatchingDismissed(profiles[name].ID, kind, "a", true))
			}

			merge, err := library.MergeIdentity(kind, "a", "b")
			require.NoError(t, err)
			for _, name := range []string{"source", "both", "survivor", "restore"} {
				assertDismissedStates(t, library, profiles[name].ID, map[string]bool{"a": true})
			}
			assertDismissedStates(t, library, profiles["untouched"].ID, map[string]bool{"a": false})
			require.NoError(t, db.Close())
			db, err = sqlite.Open(dir)
			require.NoError(t, err)
			library, err = catalog.Open(db)
			require.NoError(t, err)

			require.NoError(t, library.SetContinueWatchingDismissed(profiles["restore"].ID, kind, "a", false))
			require.NoError(t, library.SetContinueWatchingDismissed(profiles["hide"].ID, kind, "a", true))
			_, err = library.UnmergeIdentity(merge.ID)
			require.NoError(t, err)

			assertDismissedStates(t, library, profiles["source"].ID, map[string]bool{"a": false, "b": true})
			assertDismissedStates(t, library, profiles["both"].ID, map[string]bool{"a": true, "b": true})
			assertDismissedStates(t, library, profiles["survivor"].ID, map[string]bool{"a": true, "b": false})
			assertDismissedStates(t, library, profiles["restore"].ID, map[string]bool{"a": false, "b": false})
			assertDismissedStates(t, library, profiles["hide"].ID, map[string]bool{"a": true, "b": true})
			assertDismissedStates(t, library, profiles["untouched"].ID, map[string]bool{"a": false, "b": false})
		})
	}
}

func seedContinueWatchingCatalog(db *sqlite.DB) error {
	if _, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,duration_ms) VALUES('film','film','Film','film.mp4',1000)`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO catalog_series(id,title) VALUES('series','Series')`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,duration_ms,series_id,season_id) VALUES
		('episode-1','episode','One','one.mkv',1000,'series','season-1'),
		('episode-2','episode','Two','two.mkv',1000,'series','season-1')`)
	return err
}

func assertContinueWatchingIDs(t *testing.T, library *catalog.Catalog, profileID string, expected ...string) {
	t.Helper()
	view, err := library.Viewer(profileID, "all")
	require.NoError(t, err)
	require.NotEmpty(t, view.Sections)
	ids := make([]string, 0, len(view.Sections[0].Items))
	for _, item := range view.Sections[0].Items {
		ids = append(ids, item.ID)
	}
	assert.Equal(t, append([]string{}, expected...), ids)
}

func assertViewerItemState(t *testing.T, library *catalog.Catalog, profileID, id string, listed, dismissed bool) {
	t.Helper()
	view, err := library.Viewer(profileID, "all")
	require.NoError(t, err)
	for _, section := range view.Sections {
		for _, item := range section.Items {
			if item.ID == id {
				assert.Equal(t, listed, item.Listed)
				assert.Equal(t, dismissed, item.ContinueWatchingDismissed)
				return
			}
		}
	}
	t.Fatalf("viewer item %q not found", id)
}

func assertDismissedStates(t *testing.T, library *catalog.Catalog, profileID string, expected map[string]bool) {
	t.Helper()
	view, err := library.Viewer(profileID, "all")
	require.NoError(t, err)
	actual := map[string]bool{}
	for _, item := range view.Sections[1].Items {
		actual[item.ID] = item.ContinueWatchingDismissed
	}
	assert.Equal(t, expected, actual)
}
