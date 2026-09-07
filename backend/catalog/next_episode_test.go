package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestEpisodeAfterKeepsSequenceContextAndSkipsMissingOrCompletedEpisodes(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{
		"Signal/Director Cut/Season 01/Signal S01E01 Director Cut.mkv",
		"Signal/Director Cut/Season 01/Signal S01E02 Director Cut.mkv",
		"Signal/Director Cut/Season 02/Signal S02E04 Director Cut.mkv",
		"Signal/Theatrical Cut/Season 01/Signal S01E02 Theatrical Cut.mkv",
	})
	insertProfile(t, db, "ada")
	insertProfile(t, db, "lin")
	markCompleted(t, db, "ada", ids["Signal/Director Cut/Season 01/Signal S01E02 Director Cut.mkv"])

	got, err := c.EpisodeAfter("ada", ids["Signal/Director Cut/Season 01/Signal S01E01 Director Cut.mkv"], false)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != catalog.EpisodeSequenceNext || got.Episode == nil || got.Episode.ID != ids["Signal/Director Cut/Season 02/Signal S02E04 Director Cut.mkv"] {
		t.Fatalf("Ada next episode = %#v", got)
	}

	got, err = c.EpisodeAfter("lin", ids["Signal/Director Cut/Season 01/Signal S01E01 Director Cut.mkv"], false)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != catalog.EpisodeSequenceNext || got.Episode == nil || got.Episode.ID != ids["Signal/Director Cut/Season 01/Signal S01E02 Director Cut.mkv"] {
		t.Fatalf("Lin next episode = %#v", got)
	}
}

func TestEpisodeAfterAllowsEpisodeSpecificTitlesWithinOneSequence(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{
		"Signal/Season 01/Signal S01E01 Pilot.mkv",
		"Signal/Season 01/Signal S01E02 The Return.mkv",
	})
	insertProfile(t, db, "ada")

	got, err := c.EpisodeAfter("ada", ids["Signal/Season 01/Signal S01E01 Pilot.mkv"], false)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != catalog.EpisodeSequenceNext || got.Episode == nil || got.Episode.ID != ids["Signal/Season 01/Signal S01E02 The Return.mkv"] {
		t.Fatalf("next titled episode = %#v", got)
	}
}

func TestEpisodeAfterRequiresSpecialsOptIn(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{
		"Signal/Season 00/Signal S00E01.mkv",
		"Signal/Season 00/Signal S00E02.mkv",
		"Signal/Season 01/Signal S01E01.mkv",
	})
	insertProfile(t, db, "ada")

	withoutSpecials, err := c.EpisodeAfter("ada", ids["Signal/Season 00/Signal S00E01.mkv"], false)
	if err != nil {
		t.Fatal(err)
	}
	if withoutSpecials.State != catalog.EpisodeSequenceContextUnavailable || withoutSpecials.Episode != nil {
		t.Fatalf("specials-disabled result = %#v", withoutSpecials)
	}

	withSpecials, err := c.EpisodeAfter("ada", ids["Signal/Season 00/Signal S00E01.mkv"], true)
	if err != nil {
		t.Fatal(err)
	}
	if withSpecials.State != catalog.EpisodeSequenceNext || withSpecials.Episode == nil || withSpecials.Episode.ID != ids["Signal/Season 00/Signal S00E02.mkv"] {
		t.Fatalf("specials-enabled result = %#v", withSpecials)
	}
}

func TestEpisodeAfterEndsInsteadOfCrossingSequenceContext(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{
		"Signal/Archive Cut/Season 01/Signal S01E01 Archive Cut.mkv",
		"Signal/Director Cut/Season 01/Signal S01E02 Director Cut.mkv",
	})
	insertProfile(t, db, "ada")

	got, err := c.EpisodeAfter("ada", ids["Signal/Archive Cut/Season 01/Signal S01E01 Archive Cut.mkv"], false)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != catalog.EpisodeSequenceContextUnavailable || got.Episode != nil {
		t.Fatalf("cross-context result = %#v", got)
	}
}

func TestEpisodeAfterRejectsAmbiguousFilesInOneSequence(t *testing.T) {
	c, db, ids := scanEpisodeSequences(t, []string{
		"Signal/Season 01/Signal S01E01.mkv",
		"Signal/Season 01/Signal S01E02.mkv",
		"Signal/Season 01/Signal S01E02.mp4",
	})
	insertProfile(t, db, "ada")

	got, err := c.EpisodeAfter("ada", ids["Signal/Season 01/Signal S01E01.mkv"], false)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != catalog.EpisodeSequenceContextUnavailable || got.Episode != nil {
		t.Fatalf("ambiguous result = %#v", got)
	}
}

func scanEpisodeSequences(t *testing.T, paths []string) (*catalog.Catalog, *sqlite.DB, map[string]string) {
	t.Helper()
	tv, data := t.TempDir(), t.TempDir()
	for index, name := range paths {
		path := filepath.Join(tv, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte{byte(index + 1)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264", Audio: []catalog.AudioTrack{{Codec: "aac"}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Shutdown(context.Background()) })
	if err := c.SetRoots("", tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]string, len(paths))
	rows, err := db.Query("SELECT id,relative_path FROM catalog_items")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			t.Fatal(err)
		}
		ids[path] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return c, db, ids
}

func insertProfile(t *testing.T, db *sqlite.DB, id string) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO profiles(id,name) VALUES(?,?)", id, id); err != nil {
		t.Fatal(err)
	}
}

func markCompleted(t *testing.T, db *sqlite.DB, profileID, catalogID string) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO progress(profile_id,catalog_id,position_ms,completed) VALUES(?,?,1,1)", profileID, catalogID); err != nil {
		t.Fatal(err)
	}
}
