package catalog_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestScanProbesMediaAndKeepsIdentityOnMove(t *testing.T) {
	films, tv, data := t.TempDir(), t.TempDir(), t.TempDir()
	film := filepath.Join(films, "Film.Title.2024.mp4")
	if err := os.WriteFile(film, []byte("same media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tv, "Show"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tv, "Show", "Show.1x02.mkv"), []byte("episode"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264", Audio: []catalog.AudioTrack{{Codec: "aac", Channels: 2}}, Subtitles: []catalog.SubtitleTrack{{Codec: "subrip", Language: "en"}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, tv); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Season != 1 || items[1].Episode != 2 || items[0].VideoCodec != "h264" || items[0].Audio[0].Channels != 2 || items[0].Subtitles[0].Language != "en" {
		t.Fatalf("got %#v", items)
	}
	before := items[0].ID
	if err := os.Rename(film, filepath.Join(films, "Moved.Film.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	items, err = c.List("moved", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != before {
		t.Fatalf("move changed identity: %#v, want %s", items, before)
	}
	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsPathJSON(string(encoded)) {
		t.Fatalf("catalog JSON leaked path: %s", encoded)
	}
	reopened, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Item(before); !ok || got.VideoCodec != "h264" || len(got.Subtitles) != 1 {
		t.Fatalf("persisted item = %#v, exists = %v", got, ok)
	}
}

func TestScanUsesContentFingerprint(t *testing.T) {
	films := t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "One.mp4"), []byte("same-size"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.OpenWithProber(nil, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("initial items = %#v", items)
	}
	before := items[0].ID
	if err := os.WriteFile(filepath.Join(films, "One.mp4"), []byte("diff-size"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, err = c.List("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID == before {
		t.Fatalf("same-size changed content retained identity: %#v", items)
	}
}

func TestBrowseEmptyPersistentCatalogReturnsAnEmptyArray(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := c.Browse("", 0, 48)
	if err != nil {
		t.Fatal(err)
	}
	if items == nil || len(items) != 0 || total != 0 {
		t.Fatalf("empty browse = %#v, total %d", items, total)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("empty browse JSON = %s, want []", encoded)
	}
}

func containsPathJSON(value string) bool {
	return strings.Contains(value, `"path"`)
}
