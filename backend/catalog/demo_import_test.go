package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/stretchr/testify/require"
)

func TestDemoSnapshotIsExplicitStableAndNonPlayable(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	snapshot := demoSnapshot{Source: "Test snapshot"}
	for i := 0; i < 50; i++ {
		snapshot.Films = append(snapshot.Films, demoTitle{Title: fmt.Sprintf("Movie %d", i), Year: 2000 + i%20, Genres: []string{"Drama"}})
		snapshot.Series = append(snapshot.Series, demoTitle{Title: fmt.Sprintf("Show %d", i), Year: 2010, Genres: []string{"Drama"}, Episodes: []demoEpisode{{Title: "Pilot", Season: 1, Number: 1}, {Title: "Return", Season: 2, Number: 1}}})
	}
	dir := filepath.Join(db.DataDir(), "demo")
	require.NoError(t, os.MkdirAll(dir, 0700))
	save := func() {
		data, err := json.Marshal(snapshot)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "catalog.json"), data, 0600))
	}
	save()
	normal, err := Open(db)
	require.NoError(t, err)
	require.False(t, normal.Demo())
	_, count, err := normal.Browse("", 0, 100)
	require.NoError(t, err)
	require.Zero(t, count)
	for attempt := 0; attempt < 2; attempt++ {
		demo, err := OpenDemo(db)
		require.NoError(t, err)
		require.True(t, demo.Demo())
		require.Equal(t, "Test snapshot", demo.DemoSource())
		items, count, err := demo.Browse("", 0, 100)
		require.NoError(t, err)
		require.Equal(t, 100, count)
		for _, item := range items {
			require.True(t, item.Demo)
			require.False(t, item.Playable)
			if item.Kind == "series" {
				show, ok := demo.Series(item.ID)
				require.True(t, ok)
				require.Len(t, show.Seasons, 2)
				require.False(t, show.Seasons[1].Episodes[0].Playable)
			}
		}
	}
	snapshot.Films[0].Poster = "../secret"
	save()
	_, err = OpenDemo(db)
	require.ErrorContains(t, err, "asset name")
	var countAfter int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM catalog_items WHERE kind='film'").Scan(&countAfter))
	require.Equal(t, 50, countAfter)
}
