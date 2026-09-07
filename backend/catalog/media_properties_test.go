package catalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	_ "modernc.org/sqlite"
)

func TestPre011CatalogDatabaseMigratesLegacyTrackIndexes(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "flixr.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	// Build a real version-10 database, including all earlier artwork tables.
	migrations, err := os.ReadDir("../sqlite/migrations")
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	for _, migration := range migrations {
		var version int
		if _, err := fmt.Sscanf(migration.Name(), "%d_", &version); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		if version > 10 {
			continue
		}
		body, err := os.ReadFile(filepath.Join("../sqlite/migrations", migration.Name()))
		if err != nil {
			raw.Close()
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			raw.Close()
			t.Fatalf("legacy migration %s: %v", migration.Name(), err)
		}
		if _, err := raw.Exec("INSERT INTO schema_migrations(version) VALUES(?)", version); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	_, err = raw.Exec(`INSERT INTO catalog_items(id,kind,title,relative_path,audio_json,subtitle_json) VALUES('legacy','film','Legacy','film.mkv','[{"codec":"aac","channels":2}]','[{"codec":"subrip"}]')`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var revision, primary int
	if err := db.QueryRow("SELECT probe_revision,primary_video_stream_index FROM catalog_items WHERE id='legacy'").Scan(&revision, &primary); err != nil || revision != 0 || primary != -1 {
		t.Fatalf("011 migration values = revision %d, primary %d, err %v", revision, primary, err)
	}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	item, ok := c.Item("legacy")
	if !ok || item.Audio[0].Index != -1 || item.Subtitles[0].Index != -1 {
		t.Fatalf("legacy track indexes = %#v / %#v", item.Audio, item.Subtitles)
	}
}

func TestMediaPropertiesPersistAndRevisionZeroReprobes(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "film.mkv"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	properties := catalog.MediaProperties{Container: "matroska", DurationMS: 2_000, VideoCodec: "h264", VideoProfile: "High", PrimaryVideoStreamIndex: 0, Width: 320, Height: 180, Bitrate: 12_000, HDR: "smpte2084", Audio: []catalog.AudioTrack{{Index: 1, Codec: "aac", Channels: 2, Language: "eng", Title: "English", Default: true}}, Subtitles: []catalog.SubtitleTrack{{Index: 2, Codec: "subrip", Language: "fra", Title: "French", Forced: true}}}
	var probes atomic.Int32
	prober := catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		probes.Add(1)
		return properties, nil
	})
	c, err := catalog.OpenWithProber(db, prober)
	if err != nil || c.SetRoots(root, "") != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("initial scan: %v", err)
	}
	items, err := c.List("", 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	item, ok := c.Item(items[0].ID)
	if !ok || item.PrimaryVideoStreamIndex != 0 || item.DurationMS != 2_000 || item.Audio[0].Title != "English" || !item.Subtitles[0].Forced {
		t.Fatalf("properties = %#v", item)
	}
	if _, err := db.Exec("UPDATE catalog_items SET probe_revision=0"); err != nil {
		t.Fatal(err)
	}
	reopened, err := catalog.OpenWithProber(db, prober)
	if err != nil {
		t.Fatal(err)
	}
	if restored, ok := reopened.Item(item.ID); !ok || restored.Width != 320 || restored.DurationMS != 2_000 || restored.Audio[0].Index != 1 {
		t.Fatalf("reopened properties = %#v", restored)
	}
	if err := reopened.Scan(context.Background(), 1); err != nil {
		t.Fatalf("revision-zero scan: %v", err)
	}
	if got := probes.Load(); got != 2 {
		t.Fatalf("probes = %d, want 2", got)
	}
	if err := reopened.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := probes.Load(); got != 2 {
		t.Fatalf("unchanged revision-one scan probed %d times", got)
	}
}

func TestSeriesProjectsPersistedMediaProperties(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "Show", "Season 01")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Show S01E01.mkv"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	properties := catalog.MediaProperties{DurationMS: 2_000, VideoCodec: "h264", PrimaryVideoStreamIndex: 0, Width: 320, Height: 180, Bitrate: 12_000, HDR: "smpte2084", Audio: []catalog.AudioTrack{{Index: 1, Codec: "aac", Channels: 2}}, Subtitles: []catalog.SubtitleTrack{{Index: 2, Codec: "subrip", Language: "eng", Default: true}}}
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) { return properties, nil }))
	if err != nil || c.SetRoots("", root) != nil || c.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items, err := c.List("", 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	if _, err := db.Exec("INSERT INTO catalog_series(id,title) VALUES('series','Show')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO catalog_seasons(id,series_id,number) VALUES('season','series',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE catalog_items SET series_id='series',season_id='season' WHERE id=?", items[0].ID); err != nil {
		t.Fatal(err)
	}
	series, ok := c.Series("series")
	if !ok || len(series.Seasons) != 1 || len(series.Seasons[0].Episodes) != 1 {
		t.Fatalf("series = %#v", series)
	}
	episode := series.Seasons[0].Episodes[0]
	if episode.DurationMS != 2_000 || episode.PrimaryVideoStreamIndex != 0 || episode.Width != 320 || episode.Bitrate != 12_000 || episode.HDR != "smpte2084" || len(episode.Audio) != 1 || episode.Audio[0].Index != 1 || len(episode.Subtitles) != 1 || episode.Subtitles[0].Index != 2 {
		t.Fatalf("series episode properties = %#v", episode.MediaProperties)
	}
}
