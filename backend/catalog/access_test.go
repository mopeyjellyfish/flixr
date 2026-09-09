package catalog_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/access"
	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessContentUsesAllPhysicalLibrariesAndLocalPolicyMetadata(t *testing.T) {
	adult, kids, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(adult, "Shared.mp4"), []byte("same media"))
	writeReviewMedia(t, filepath.Join(kids, "Shared.mp4"), []byte("same media"))
	db, c := openCatalog(t, data)
	defer db.Close()
	require.NoError(t, c.SetRoots(adult, ""))
	kidsLibrary, err := c.CreateLibrary("Kids", "film")
	require.NoError(t, err)
	_, err = c.AddLibraryLocation(kidsLibrary.ID, kids)
	require.NoError(t, err)
	require.NoError(t, c.Scan(context.Background(), 2))
	items, _, err := c.Browse("", 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, err = c.EditMetadata("film", items[0].ID, MetadataEditWithPolicy("family,local", "PG"))
	require.NoError(t, err)

	content, ok, err := c.AccessContent("film", items[0].ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"films", kidsLibrary.ID}, content.LibraryIDs)
	assert.ElementsMatch(t, []string{"family", "local"}, content.Tags)
	assert.Equal(t, "PG", content.Rating)
}

func TestRestrictedPlaybackSelectsAnAllowedPhysicalSource(t *testing.T) {
	adult, kids, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(adult, "Shared.mp4"), []byte("same media"))
	writeReviewMedia(t, filepath.Join(kids, "Shared.mp4"), []byte("same media"))
	db, c := openCatalog(t, data)
	defer db.Close()
	require.NoError(t, c.SetRoots(adult, ""))
	kidsLibrary, err := c.CreateLibrary("Kids", "film")
	require.NoError(t, err)
	_, err = c.AddLibraryLocation(kidsLibrary.ID, kids)
	require.NoError(t, err)
	require.NoError(t, c.Scan(context.Background(), 2))
	items, _, err := c.Browse("", 0, 10)
	require.NoError(t, err)
	unrestricted, err := c.PlaybackItem(items[0].ID)
	require.NoError(t, err)
	var selectedLibrary string
	require.NoError(t, db.QueryRow(`SELECT x.library_id FROM catalog_physical_files f JOIN library_locations x ON x.id=f.location_id WHERE f.catalog_id=? AND f.selected=1`, items[0].ID).Scan(&selectedLibrary))
	allowedLibrary := kidsLibrary.ID
	if selectedLibrary == kidsLibrary.ID {
		allowedLibrary = "films"
	}

	restricted, err := c.PlaybackItemForPolicy(context.Background(), items[0].ID, access.Policy{LibraryIDs: []string{allowedLibrary}})
	require.NoError(t, err)
	assert.NotEqual(t, unrestricted.SourceKey(), restricted.SourceKey())
	file, err := c.OpenSource(items[0].ID, restricted.SourceKey())
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func MetadataEditWithPolicy(tags, rating string) catalog.MetadataEdit {
	return catalog.MetadataEdit{Fields: []catalog.MetadataField{{Field: "tags", Value: tags, Source: "local", Locked: true}, {Field: "content_rating", Value: rating, Source: "local", Locked: true}}}
}
