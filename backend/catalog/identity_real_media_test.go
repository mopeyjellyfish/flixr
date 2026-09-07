package catalog_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestLogicalIdentityWithRealFFmpegMedia(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	films, data := t.TempDir(), t.TempDir()
	path := filepath.Join(films, "Film.mp4")
	makeMedia := func(color string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c="+color+":s=64x64:r=5:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture %v: %s", err, out)
		}
	}
	makeMedia("red")
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("real scan %#v %v", items, err)
	}
	original, err := c.PlaybackItem(items[0].ID)
	if err != nil || original.VideoCodec != "h264" || original.DurationMS <= 0 {
		t.Fatalf("real probe %#v %v", original, err)
	}
	makeMedia("blue")
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	replacement, err := c.PlaybackItem(original.ID)
	if err != nil || replacement.SourceKey() == original.SourceKey() {
		t.Fatalf("replacement %#v %v", replacement, err)
	}
	if file, err := c.OpenSource(original.ID, original.SourceKey()); err == nil {
		file.Close()
		t.Fatal("old real-media session changed source")
	}
	if err := os.Rename(path, filepath.Join(films, "Moved.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	moved, err := c.PlaybackItem(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := c.OpenSource(moved.ID, moved.SourceKey())
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}
