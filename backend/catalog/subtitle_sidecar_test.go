package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestSubtitleSidecarIsConfinedPathFreeAndDurable(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	video := filepath.Join(root, "Film 2026.mkv")
	sidecar := filepath.Join(root, "Film 2026.fra.Forced.SDH.vtt")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nBonjour\n"), 0o600); err != nil {
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
		if info.Size() == int64(len("video")) {
			return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264", Subtitles: []catalog.SubtitleTrack{{Index: 2, Codec: "subrip", Language: "eng", Default: true}}}, nil
		}
		return catalog.MediaProperties{Subtitles: []catalog.SubtitleTrack{{Index: 0, Codec: "webvtt", Language: "und"}}}, nil
	})
	library, err := catalog.OpenWithProber(db, prober)
	if err != nil || library.SetRoots(root, "") != nil || library.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items, err := library.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	tracks := items[0].Subtitles
	if len(tracks) != 2 || tracks[1].Index != 3 || !tracks[1].External || tracks[1].Language != "fra" || !tracks[1].Forced || !tracks[1].SDH {
		t.Fatalf("subtitle tracks = %#v", tracks)
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
	subtitleKey := tracks[1].SourceKey()
	file, err := library.OpenSubtitleSource(items[0].ID, playbackItem.SourceKey(), subtitleKey, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(file)
	file.Close()
	if err != nil || len(contents) == 0 {
		t.Fatalf("sidecar contents = %q, %v", contents, err)
	}

	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nChanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sidecar, time.Unix(0, info.ModTime().UnixNano()), time.Unix(0, info.ModTime().UnixNano())); err != nil {
		t.Fatal(err)
	}
	if file, err := library.OpenSubtitleSource(items[0].ID, playbackItem.SourceKey(), subtitleKey, 3, true); err == nil {
		file.Close()
		t.Fatal("changed subtitle sidecar retained its admitted identity")
	}

	reopened, err := catalog.OpenWithProber(db, prober)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := reopened.Item(items[0].ID)
	if !ok || len(item.Subtitles) != 2 || !item.Subtitles[1].External || item.Subtitles[1].SourceKey() == "" {
		t.Fatalf("reopened subtitles = %#v, exists=%v", item.Subtitles, ok)
	}
}

func TestSubtitleSidecarsMustMatchTheMediaDirectoryStemAndLanguage(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{
		"Film.mkv":                 "video",
		"Film.eng.srt":             "subtitle",
		"Film...srt":               "invalid language",
		"Other.eng.srt":            "wrong stem",
		"nested/Film.fra.srt":      "wrong directory",
		"Film.eng.unsupported.ass": "unsupported extension",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prober := catalog.ProberFunc(func(_ context.Context, file *os.File) (catalog.MediaProperties, error) {
		info, _ := file.Stat()
		if info.Size() == int64(len("video")) {
			return catalog.MediaProperties{Container: "matroska", VideoCodec: "h264"}, nil
		}
		return catalog.MediaProperties{Subtitles: []catalog.SubtitleTrack{{Index: 0, Codec: "subrip"}}}, nil
	})
	library, err := catalog.OpenWithProber(db, prober)
	if err != nil || library.SetRoots(root, "") != nil || library.Scan(context.Background(), 1) != nil {
		t.Fatalf("scan: %v", err)
	}
	items, err := library.List("", 0, 10)
	if err != nil || len(items) != 1 || len(items[0].Subtitles) != 1 || items[0].Subtitles[0].Language != "eng" {
		t.Fatalf("subtitle matches = %#v, %v", items, err)
	}
}
