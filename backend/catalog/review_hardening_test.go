package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchTreatsWildcardsAsLiterals(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeReviewMedia(t, filepath.Join(films, "100% Real.mp4"))
	writeReviewMedia(t, filepath.Join(films, "100x Real.mp4"))
	writeReviewMedia(t, filepath.Join(tv, "Show", "Show.S01E01.mp4"))
	writeReviewMedia(t, filepath.Join(tv, "Other", "Other.S01E01.mp4"))
	db, err := sqlite.Open(data)
	require.NoError(t, err)
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	require.NoError(t, err)
	require.NoError(t, c.SetRoots(films, tv))
	require.NoError(t, c.Scan(context.Background(), 1))
	_, err = db.Exec("UPDATE catalog_items SET title='Under_score' WHERE root_kind='episode' AND title LIKE 'Show%'")
	require.NoError(t, err)
	_, err = db.Exec("UPDATE catalog_items SET title=? WHERE root_kind='film' AND title LIKE '100x%'", "C:\\Film")
	require.NoError(t, err)
	_, err = db.Exec("UPDATE catalog_series SET title='Under_score' WHERE title='Show'")
	require.NoError(t, err)

	items, total, err := c.Browse("%", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "100% Real", items[0].Title)

	items, total, err = c.Browse("_", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "Under_score", items[0].Title)
	items, total, err = c.Browse("\\", 0, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "C:\\Film", items[0].Title)

	items, err = c.List("%", 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "100% Real", items[0].Title)
	items, err = c.List("_", 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Under_score", items[0].Title)
	items, err = c.List("\\", 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "C:\\Film", items[0].Title)
}

func TestFingerprintIncludesFileSize(t *testing.T) {
	films := t.TempDir()
	const sample = 64 << 10
	writeReviewMedia(t, filepath.Join(films, "first.mp4"), append(append(make([]byte, sample), []byte("middle")...), make([]byte, sample)...))
	writeReviewMedia(t, filepath.Join(films, "second.mp4"), append(append(make([]byte, sample), []byte("longer middle")...), make([]byte, sample)...))
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	require.NoError(t, err)
	require.NoError(t, c.SetRoots(films, ""))
	require.NoError(t, c.Scan(context.Background(), 1))
	items, err := c.List("", 0, 10)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func writeReviewMedia(t *testing.T, path string, data ...[]byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	if len(data) == 0 {
		data = [][]byte{[]byte(path)}
	}
	require.NoError(t, os.WriteFile(path, data[0], 0600))
}
