//go:build media_integration

package playback

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestRealFFmpegRemuxAndTranscodeProduceH264AACFMP4HLS(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("FLIXR_REQUIRE_MEDIA_INTEGRATION") == "1" {
				t.Fatalf("%s is required for media integration", tool)
			}
			t.Skipf("%s is unavailable", tool)
		}
	}
	tests := []struct {
		name    string
		kind    Kind
		fixture string
	}{
		{"remux", Remux, "../testdata/media/tv/Signal/Season 01/Signal S01E01.mkv"},
		{"transcode", Transcode, "../testdata/media/films/Compatibility Check 2026.avi"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, err := filepath.Abs(test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				file, err := os.Open(fixture)
				if err != nil {
					http.Error(w, "missing fixture", http.StatusNotFound)
					return
				}
				defer file.Close()
				info, err := file.Stat()
				if err != nil {
					http.Error(w, "missing fixture", http.StatusNotFound)
					return
				}
				http.ServeContent(w, r, info.Name(), info.ModTime(), file)
			}))
			defer server.Close()
			dir := t.TempDir()
			name, args, err := ffmpegCommand(test.kind, server.URL, dir, 0, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			process, err := (OSExecutor{}).Start(name, args, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("ffmpeg: %v\n%s", err, stderr.String())
				}
			case <-time.After(20 * time.Second):
				_ = process.Kill()
				t.Fatal("ffmpeg did not finish within 20 seconds")
			}
			manifest, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(manifest, []byte("#EXT-X-MAP")) {
				t.Fatalf("manifest is not fMP4 HLS:\n%s", manifest)
			}
			assertPlaylistCodecs(t, filepath.Join(dir, "index.m3u8"))
		})
	}
}

func assertPlaylistCodecs(t *testing.T, manifest string) {
	t.Helper()
	command := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-of", "json", manifest)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Streams))
	for _, stream := range result.Streams {
		got = append(got, stream.CodecType+":"+stream.CodecName)
	}
	sort.Strings(got)
	want := []string{"audio:aac", "video:h264"}
	if len(got) != len(want) {
		t.Fatalf("codecs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("codecs = %v, want %v", got, want)
		}
	}
}
