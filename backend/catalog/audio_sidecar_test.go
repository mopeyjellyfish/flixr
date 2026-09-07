package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestAudioSidecarIsSelectablePathFreeAndDurable(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	video := filepath.Join(root, "Film 2026.mkv")
	sidecar := filepath.Join(root, "Film 2026.fra.Director Commentary.m4a")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte("external-audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prober := catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		info, err := file.Stat()
		if err != nil {
			return catalog.MediaProperties{}, err
		}
		if info.Size() == int64(len("external-audio")) {
			return catalog.MediaProperties{Audio: []catalog.AudioTrack{{Index: 0, Codec: "aac", Channels: 2, Language: "und"}}}, nil
		}
		return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264", PrimaryVideoStreamIndex: 0, Audio: []catalog.AudioTrack{{Index: 1, Codec: "aac", Channels: 2, Language: "eng", Default: true}}}, nil
	})
	library, err := catalog.OpenWithProber(db, prober)
	if err != nil || library.SetRoots(root, "") != nil || library.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items, err := library.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	tracks := items[0].Audio
	if len(tracks) != 2 || tracks[1].Index != 2 || !tracks[1].External || tracks[1].Language != "fra" || tracks[1].Title != "Director Commentary" {
		t.Fatalf("audio tracks = %#v", tracks)
	}
	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if containsPathJSON(string(encoded)) {
		t.Fatalf("sidecar path leaked: %s", encoded)
	}
	playbackItem, err := library.PlaybackItemContext(context.Background(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := library.OpenAudioSource(items[0].ID, playbackItem.SourceKey(), 2, true)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(contents) != "external-audio" {
		t.Fatalf("sidecar contents = %q, %v", contents, err)
	}
	if file, err := library.OpenAudioSource(items[0].ID, "changed-source", 2, true); err == nil {
		file.Close()
		t.Fatal("sidecar opened for a different physical source")
	}

	reopened, err := catalog.OpenWithProber(db, prober)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := reopened.Item(items[0].ID)
	if !ok || len(item.Audio) != 2 || !item.Audio[1].External {
		t.Fatalf("reopened audio = %#v, exists=%v", item.Audio, ok)
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	item, _ = reopened.Item(items[0].ID)
	if len(item.Audio) != 1 || item.Audio[0].External {
		t.Fatalf("removed sidecar remained cached: %#v", item.Audio)
	}
}
