package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

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
