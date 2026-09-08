//go:build media_integration

package preview

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRealMediaFrameAndChapters(t *testing.T) {
	for _, path := range []string{"../testdata/media/films/Blue Horizon 2026.mp4", "../testdata/media/tv/Signal/Season 01/Signal S01E01.mkv"} {
		t.Run(path, func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			service := New()
			data, err := service.Frame(context.Background(), file, path, 0)
			if err != nil {
				t.Fatal(err)
			}
			pic, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil || pic.Bounds().Dx() != 320 || pic.Bounds().Dy() != 180 {
				t.Fatalf("frame dimensions/decode: %v", err)
			}
			chapters, err := service.Chapters(context.Background(), file, path)
			if err != nil {
				t.Fatal(err)
			}
			for _, chapter := range chapters {
				if chapter.EndMS <= chapter.StartMS {
					t.Fatal(chapter)
				}
			}
		})
	}
}

func TestRealChapterTimesAndDistinctSeekFrames(t *testing.T) {
	root := t.TempDir()
	metadata := filepath.Join(root, "chapters.txt")
	media := filepath.Join(root, "chapters.mp4")
	if err := os.WriteFile(metadata, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=10000\ntitle=Opening\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=10000\nEND=12000\ntitle=Arrival\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=5:duration=12", "-i", metadata, "-map_metadata", "1", "-map_chapters", "1", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-y", media)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	file, err := os.Open(media)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	service := New()
	chapters, err := service.Chapters(context.Background(), file, "fixture")
	if err != nil || len(chapters) != 2 || chapters[1].StartMS != 10000 || chapters[1].Title != "Arrival" {
		t.Fatalf("chapters: %#v %v", chapters, err)
	}
	first, err := service.Frame(context.Background(), file, "fixture", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Frame(context.Background(), file, "fixture", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("seek frames did not advance")
	}
	cached, err := service.Frame(context.Background(), file, "fixture", 11999)
	if err != nil || !bytes.Equal(cached, second) {
		t.Fatal("wrong preview interval", err)
	}
}
