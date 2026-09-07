//go:build media_integration

package playback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type mediaFixtureManifest struct {
	Generator string         `json:"generator"`
	Fixtures  []mediaFixture `json:"fixtures"`
}

type mediaFixture struct {
	ID             string                `json:"id"`
	Path           string                `json:"path"`
	SHA256         string                `json:"sha256"`
	Classification string                `json:"classification"`
	Consumer       string                `json:"consumer"`
	Generation     string                `json:"generation"`
	License        string                `json:"license"`
	Reason         string                `json:"reason"`
	Streams        []string              `json:"streams"`
	MinimumSeconds int                   `json:"minimum_seconds"`
	M2TS           bool                  `json:"m2ts"`
	Chapters       int                   `json:"chapters"`
	Subtitles      []subtitleDisposition `json:"subtitle_dispositions"`
	VFR            bool                  `json:"vfr"`
	SeekStart      int                   `json:"seek_start_seconds"`
}

func TestMediaFixtureCorpusMatchesManifest(t *testing.T) {
	manifest := loadMediaFixtureManifest(t)
	if manifest.Generator == "" {
		t.Fatal("fixture manifest has no generator")
	}
	if len(manifest.Fixtures) < 12 {
		t.Fatalf("fixture count = %d, want corpus", len(manifest.Fixtures))
	}
	for _, fixture := range manifest.Fixtures {
		if fixture.ID == "" || fixture.Classification == "" || fixture.Consumer == "" || fixture.Generation == "" || fixture.License == "" {
			t.Fatalf("fixture %+v lacks classification, consumer, or streams", fixture)
		}
		switch fixture.Classification {
		case string(Direct), string(Remux), string(Transcode), "malformed", "version", "sidecar":
		default:
			t.Fatalf("fixture %s has unknown classification %q", fixture.ID, fixture.Classification)
		}
		if fixture.Path == "" {
			if fixture.Classification != "unsupported" || fixture.Reason == "" {
				t.Fatalf("fixture %+v has no file without an unsupported reason", fixture)
			}
			continue
		}
		if fixture.SHA256 == "" || len(fixture.Streams) == 0 {
			t.Fatalf("fixture %+v lacks hash or streams", fixture)
		}
		contents, err := os.ReadFile(filepath.Join("..", "testdata", "media", fixture.Path))
		if err != nil {
			t.Fatalf("read %s: %v", fixture.Path, err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != fixture.SHA256 {
			t.Fatalf("sha256(%s) = %s, want %s", fixture.Path, got, fixture.SHA256)
		}
		if fixture.Classification == "malformed" {
			continue
		}
		assertFixtureStreams(t, filepath.Join("..", "testdata", "media", fixture.Path), fixture.Streams)
		if fixture.MinimumSeconds > 0 {
			assertFixtureMinimumDuration(t, filepath.Join("..", "testdata", "media", fixture.Path), fixture.MinimumSeconds)
		}
		if fixture.M2TS {
			assertM2TSPackets(t, filepath.Join("..", "testdata", "media", fixture.Path))
		}
		if fixture.Chapters > 0 {
			assertFixtureChapters(t, filepath.Join("..", "testdata", "media", fixture.Path), fixture.Chapters)
		}
		if len(fixture.Subtitles) > 0 {
			assertSubtitleDispositions(t, filepath.Join("..", "testdata", "media", fixture.Path), fixture.Subtitles)
		}
		if fixture.VFR {
			assertVariableFrameRate(t, filepath.Join("..", "testdata", "media", fixture.Path))
		}
	}
}

func assertFixtureChapters(t *testing.T, fixture string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "chapter=id", "-of", "csv=p=0", fixture).Output()
	if err != nil {
		t.Fatalf("ffprobe chapters %s: %v", fixture, err)
	}
	if got := len(strings.Fields(string(output))); got != want {
		t.Fatalf("chapters(%s) = %d, want %d", fixture, got, want)
	}
}

type subtitleDisposition struct {
	Language string `json:"language"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
}

func assertSubtitleDispositions(t *testing.T, fixture string, want []subtitleDisposition) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "s", "-show_entries", "stream_disposition=default,forced:stream_tags=language", "-of", "json", fixture).Output()
	if err != nil {
		t.Fatalf("ffprobe subtitle disposition %s: %v", fixture, err)
	}
	var result struct {
		Streams []struct {
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
				Forced  int `json:"forced"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Streams))
	for _, stream := range result.Streams {
		got = append(got, fmt.Sprintf("%s:%t:%t", stream.Tags["language"], stream.Disposition.Default == 1, stream.Disposition.Forced == 1))
	}
	wantValues := make([]string, 0, len(want))
	for _, subtitle := range want {
		wantValues = append(wantValues, fmt.Sprintf("%s:%t:%t", subtitle.Language, subtitle.Default, subtitle.Forced))
	}
	sort.Strings(got)
	sort.Strings(wantValues)
	if fmt.Sprint(got) != fmt.Sprint(wantValues) {
		t.Fatalf("subtitle dispositions(%s) = %v, want %v", fixture, got, wantValues)
	}
}

func assertVariableFrameRate(t *testing.T, fixture string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "frame=best_effort_timestamp_time", "-of", "csv=p=0", fixture).Output()
	if err != nil {
		t.Fatalf("ffprobe timestamps %s: %v", fixture, err)
	}
	var timestamps []float64
	for _, value := range strings.Fields(string(output)) {
		timestamp, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatal(err)
		}
		timestamps = append(timestamps, timestamp)
		if len(timestamps) == 3 {
			break
		}
	}
	if len(timestamps) < 3 || timestamps[1]-timestamps[0] == timestamps[2]-timestamps[1] {
		t.Fatalf("timestamps(%s) = %v, want uneven frame intervals", fixture, timestamps)
	}
}

func assertFixtureMinimumDuration(t *testing.T, fixture string, minimum int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", fixture).Output()
	if err != nil {
		t.Fatalf("ffprobe duration %s: %v", fixture, err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		t.Fatal(err)
	}
	if duration < float64(minimum) {
		t.Fatalf("duration(%s) = %.3fs, want at least %ds", fixture, duration, minimum)
	}
}

func assertM2TSPackets(t *testing.T, fixture string) {
	t.Helper()
	contents, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 192 || len(contents)%192 != 0 {
		t.Fatalf("M2TS size = %d, want 192-byte packets", len(contents))
	}
	for offset := 4; offset < len(contents); offset += 192 {
		if contents[offset] != 0x47 {
			t.Fatalf("M2TS packet at %d has sync byte %x, want 47", offset, contents[offset])
		}
	}
}

func loadMediaFixtureManifest(t *testing.T) mediaFixtureManifest {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "testdata", "media", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest mediaFixtureManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func assertFixtureStreams(t *testing.T, fixture string, want []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name:stream_tags=language", "-of", "json", fixture).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", fixture, err)
	}
	var result struct {
		Streams []struct {
			CodecType string            `json:"codec_type"`
			CodecName string            `json:"codec_name"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Streams))
	for _, stream := range result.Streams {
		language := stream.Tags["language"]
		if language == "" {
			language = "und"
		}
		got = append(got, stream.CodecType+":"+stream.CodecName+":"+language)
	}
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("streams(%s) = %v, want %v", fixture, got, want)
	}
}

func TestRealFFmpegRemuxAndTranscodeProducePlayableHLS(t *testing.T) {
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
		start   time.Duration
	}{}
	for _, fixture := range loadMediaFixtureManifest(t).Fixtures {
		if fixture.Classification != string(Remux) && fixture.Classification != string(Transcode) {
			continue
		}
		tests = append(tests, struct {
			name    string
			kind    Kind
			fixture string
			start   time.Duration
		}{fixture.ID, Kind(fixture.Classification), filepath.Join("..", "testdata", "media", fixture.Path), time.Duration(fixture.SeekStart) * time.Second})
	}
	if len(tests) == 0 {
		t.Fatal("manifest has no remux or transcode fixtures")
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, err := filepath.Abs(test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			if test.start > 0 {
				assertFixtureSeek(t, fixture, test.start)
				return
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
			name, args, err := ffmpegCommand(test.kind, server.URL, "", -1, dir, test.start, time.Minute)
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
			assertPlaylistDecodes(t, filepath.Join(dir, "index.m3u8"))
		})
	}
}

func TestRealFFmpegMapsRequestedAudioStream(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	fixture, err := filepath.Abs(filepath.Join("..", "testdata", "media", "tv", "Signal", "Season 01", "Signal S01E01.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, fixture)
	}))
	defer server.Close()
	dir := t.TempDir()
	name, args, err := ffmpegCommand(Remux, server.URL, "", 2, dir, 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	process, err := (OSExecutor{}).Start(name, args, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, stderr.String())
	}
	assertPlaylistAudioLanguage(t, filepath.Join(dir, "index.m3u8"), "fra")

	sidecar, err := filepath.Abs(filepath.Join("..", "testdata", "media", "tv", "Signal", "Season 01", "Signal S01E01.jpn.Director Commentary.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	audioServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, sidecar)
	}))
	defer audioServer.Close()
	externalDir := t.TempDir()
	name, args, err = ffmpegCommand(Remux, server.URL, audioServer.URL, 0, externalDir, 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	process, err = (OSExecutor{}).Start(name, args, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("external audio ffmpeg: %v\n%s", err, stderr.String())
	}
	assertPlaylistAudioLanguage(t, filepath.Join(externalDir, "index.m3u8"), "jpn")
}

func assertPlaylistAudioLanguage(t *testing.T, manifest, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream_tags=language", "-of", "default=nw=1:nk=1", manifest).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != want {
		t.Fatalf("selected audio language = %q, want %q", got, want)
	}
}

func assertFixtureSeek(t *testing.T, fixture string, start time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-ss", strconv.FormatFloat(start.Seconds(), 'f', 0, 64), "-i", fixture, "-t", "1", "-map", "0:v:0", "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("seek %s at %s: %v\n%s", fixture, start, err, output)
	}
}

func TestRealFFmpegRejectsPartialFixture(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	var fixture string
	for _, candidate := range loadMediaFixtureManifest(t).Fixtures {
		if candidate.Classification == "malformed" {
			fixture = filepath.Join("..", "testdata", "media", candidate.Path)
			break
		}
	}
	if fixture == "" {
		t.Fatal("manifest has no malformed fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "ffprobe", "-v", "error", fixture).Run(); err == nil {
		t.Fatal("ffprobe accepted intentionally partial fixture")
	}
}

func TestRealFFmpegCancellationAllowsRestart(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	var fixturePath string
	for _, candidate := range loadMediaFixtureManifest(t).Fixtures {
		if candidate.ID == "vfr-webm" {
			fixturePath = candidate.Path
			break
		}
	}
	if fixturePath == "" {
		t.Fatal("manifest has no VFR fixture")
	}
	fixture, err := filepath.Abs(filepath.Join("..", "testdata", "media", fixturePath))
	if err != nil {
		t.Fatal(err)
	}
	inputStarted := make(chan struct{})
	var inputOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inputOnce.Do(func() { close(inputStarted) })
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
	start := func(dir string) Process {
		name, args, err := ffmpegCommand(Transcode, server.URL, "", -1, dir, 0, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		process, err := (OSExecutor{}).Start(name, args, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		return process
	}
	firstDir := t.TempDir()
	first := start(firstDir)
	select {
	case <-inputStarted:
	case <-time.After(5 * time.Second):
		_ = first.Kill()
		t.Fatal("ffmpeg did not request the fixture")
	}
	if err := first.Kill(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- first.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled ffmpeg exited successfully")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled ffmpeg did not exit")
	}
	secondDir := t.TempDir()
	second := start(secondDir)
	if err := second.Wait(); err != nil {
		t.Fatal(err)
	}
	assertPlaylistDecodes(t, filepath.Join(secondDir, "index.m3u8"))
}

func assertPlaylistDecodes(t *testing.T, manifest string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", manifest, "-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("decode playable HLS: %v\n%s", err, output)
	}
}

func assertPlaylistCodecs(t *testing.T, manifest string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-of", "json", manifest)
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
