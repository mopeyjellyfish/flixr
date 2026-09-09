//go:build media_integration

package playback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

func fixtureBitrateEvidence(t *testing.T, fixture string) (int64, string, int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=bit_rate:stream=codec_type,codec_name,bit_rate", "-of", "json", fixture).Output()
	if err != nil {
		t.Fatalf("ffprobe bitrates %s: %v", fixture, err)
	}
	var result struct {
		Format struct {
			Bitrate string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Bitrate   string `json:"bit_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	formatBitrate, _ := strconv.ParseInt(result.Format.Bitrate, 10, 64)
	var videoBitrate, audioBitrate int64
	var audioCodec string
	for _, stream := range result.Streams {
		bitrate, _ := strconv.ParseInt(stream.Bitrate, 10, 64)
		if bitrate <= 0 {
			bitrate = formatBitrate
		}
		switch stream.CodecType {
		case "video":
			if videoBitrate == 0 {
				videoBitrate = bitrate
			}
		case "audio":
			if audioCodec == "" {
				audioCodec, audioBitrate = stream.CodecName, bitrate
			}
		}
	}
	return videoBitrate, audioCodec, audioBitrate
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
			commandPlan := Plan{Kind: test.kind}
			if test.kind == Remux {
				commandPlan.VideoBitrate, commandPlan.AudioCodec, commandPlan.AudioBitrate = fixtureBitrateEvidence(t, fixture)
			}
			name, args, err := ffmpegCommand(commandPlan, server.URL, "", -1, dir, test.start, time.Minute)
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
			master, err := os.ReadFile(filepath.Join(dir, "master.m3u8"))
			if err != nil {
				t.Fatal(err)
			}
			codecDeclaration := `CODECS="avc1.640028,mp4a.40.2"`
			if test.kind == Remux {
				codecDeclaration = `CODECS="avc1.64000c,mp4a.40.2"`
			}
			if !bytes.Contains(master, []byte(codecDeclaration)) || !bytes.Contains(master, []byte("index.m3u8")) {
				t.Fatalf("master manifest does not declare the generated rendition:\n%s", master)
			}
			assertPlaylistCodecs(t, filepath.Join(dir, "index.m3u8"))
			assertPlaylistRendition(t, test.kind, fixture, filepath.Join(dir, "index.m3u8"))
			assertFragmentedMP4Segments(t, dir, manifest)
			assertPlaylistDecodes(t, filepath.Join(dir, "index.m3u8"))
		})
	}
}

func TestRealFFmpegNonzeroMain10TranscodeSeekPresentsFramesWithinTwoSeconds(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("FLIXR_REQUIRE_MEDIA_INTEGRATION") == "1" {
				t.Fatalf("%s is required for media integration", tool)
			}
			t.Skipf("%s is unavailable", tool)
		}
	}

	work := t.TempDir()
	fixture := filepath.Join(work, "seek-main10.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	generate := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=320x180:r=30:d=28",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=28",
		"-vf", "drawbox=color=red:t=fill:enable='between(t,0,2)',drawbox=color=green:t=fill:enable='between(t,2,4)',drawbox=color=blue:t=fill:enable='between(t,4,6)',drawbox=color=yellow:t=fill:enable='between(t,6,7)',drawbox=color=cyan:t=fill:enable='between(t,7,10)',drawbox=color=magenta:t=fill:enable='gte(t,10)'",
		"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le",
		"-x265-params", "log-level=error:keyint=60:min-keyint=60:scenecut=0",
		"-c:a", "aac", "-b:a", "96k", "-shortest", fixture,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate Main 10 seek fixture: %v\n%s", err, output)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, fixture)
	}))
	defer server.Close()
	outputDir := filepath.Join(work, "output")
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, args, err := ffmpegCommand(Plan{Kind: Transcode}, server.URL, "", -1, outputDir, 6*time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ffmpeg args: %s", strings.Join(args, " "))
	var stderr bytes.Buffer
	started := time.Now()
	process, err := (OSExecutor{}).Start(name, args, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill() })

	manifest := filepath.Join(outputDir, "index.m3u8")
	segments := waitForMediaSegments(t, manifest, 1, process, 10*time.Second)
	firstSegmentReady := time.Since(started)
	firstFrame := decodeFragmentRGB(t, outputDir, segments[0])
	firstFrameReady := time.Since(started)
	t.Logf("first segment %s; first decoded frame %s", firstSegmentReady.Round(time.Millisecond), firstFrameReady.Round(time.Millisecond))
	assertRGBNear(t, firstFrame, [3]byte{253, 253, 0}, 20)
	if firstFrameReady >= 2*time.Second {
		t.Fatalf("first decoded target frame ready in %s, want under 2s; ffmpeg:\n%s", firstFrameReady, stderr.String())
	}

	segments = waitForMediaSegments(t, manifest, 2, process, 10*time.Second)
	continuingFrame := decodeFragmentRGB(t, outputDir, segments[1])
	continuingReady := time.Since(started)
	assertRGBNear(t, continuingFrame, [3]byte{0, 253, 253}, 20)

	waitForMediaSegments(t, manifest, 7, process, 15*time.Second)
	seventhSegmentReady := time.Since(started)
	waitForMediaSegments(t, manifest, 9, process, 15*time.Second)
	ninthSegmentReady := time.Since(started)
	if pacedInterval := ninthSegmentReady - seventhSegmentReady; pacedInterval < 3500*time.Millisecond {
		t.Fatalf("two later segments advanced in %s; input did not return to sustained real-time pacing", pacedInterval)
	}
	t.Logf("Main 10 -> H.264 seek: first segment %s; first target frame %s; continuing frame %s; seventh segment %s; ninth segment %s", firstSegmentReady.Round(time.Millisecond), firstFrameReady.Round(time.Millisecond), continuingReady.Round(time.Millisecond), seventhSegmentReady.Round(time.Millisecond), ninthSegmentReady.Round(time.Millisecond))

	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = process.Kill()
		t.Fatalf("ffmpeg did not stop after seek qualification; stderr:\n%s", stderr.String())
	}
}

func TestRealFFmpegNonzeroRemuxSeekPresentsTargetAndContinuingFrames(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("FLIXR_REQUIRE_MEDIA_INTEGRATION") == "1" {
				t.Fatalf("%s is required for media integration", tool)
			}
			t.Skipf("%s is unavailable", tool)
		}
	}

	work := t.TempDir()
	fixture := filepath.Join(work, "seek-remux.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	generate := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=320x180:r=24:d=20",
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=20",
		"-vf", "drawbox=color=red:t=fill:enable='between(t,0,4)',drawbox=color=yellow:t=fill:enable='between(t,4,8)',drawbox=color=cyan:t=fill:enable='between(t,8,12)',drawbox=color=magenta:t=fill:enable='between(t,12,16)',drawbox=color=green:t=fill:enable='gte(t,16)'",
		"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-pix_fmt", "yuv420p",
		"-g", "96", "-keyint_min", "96", "-sc_threshold", "0", "-bf", "0",
		"-c:a", "aac", "-b:a", "96k", "-movflags", "+faststart", "-shortest", fixture,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate remux seek fixture: %v\n%s", err, output)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, fixture)
	}))
	defer server.Close()
	outputDir := filepath.Join(work, "output")
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Kind: Remux, VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 96_000}
	name, args, err := ffmpegCommand(plan, server.URL, "", -1, outputDir, 8*time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(args, " "); !strings.Contains(joined, "-hls_time 4") || !strings.Contains(joined, "-hls_list_size 15") {
		t.Fatalf("remux did not preserve its bounded four-second segment window: %s", joined)
	}
	var stderr bytes.Buffer
	started := time.Now()
	process, err := (OSExecutor{}).Start(name, args, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill() })

	manifest := filepath.Join(outputDir, "index.m3u8")
	segments := waitForMediaSegments(t, manifest, 1, process, 10*time.Second)
	firstFrame := decodeFragmentRGB(t, outputDir, segments[0])
	firstFrameReady := time.Since(started)
	assertRGBNear(t, firstFrame, [3]byte{0, 253, 253}, 20)
	if firstFrameReady >= 2*time.Second {
		t.Fatalf("first decoded remux target frame ready in %s, want under 2s; ffmpeg:\n%s", firstFrameReady, stderr.String())
	}
	segments = waitForMediaSegments(t, manifest, 2, process, 10*time.Second)
	continuingFrame := decodeFragmentRGB(t, outputDir, segments[1])
	continuingReady := time.Since(started)
	assertRGBNear(t, continuingFrame, [3]byte{253, 0, 252}, 20)
	t.Logf("H.264 remux seek: first target frame %s; continuing frame %s", firstFrameReady.Round(time.Millisecond), continuingReady.Round(time.Millisecond))

	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = process.Kill()
		t.Fatalf("ffmpeg did not stop after remux seek qualification; stderr:\n%s", stderr.String())
	}
}

func waitForMediaSegments(t *testing.T, manifest string, count int, process Process, timeout time.Duration) []string {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		content, err := os.ReadFile(manifest)
		if err == nil {
			var segments []string
			for _, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, "segment-") && strings.HasSuffix(line, ".m4s") {
					segments = append(segments, line)
				}
			}
			if len(segments) >= count {
				return segments
			}
		}
		select {
		case <-deadline.C:
			_ = process.Kill()
			t.Fatalf("playlist did not publish %d complete segments within %s", count, timeout)
		case <-ticker.C:
		}
	}
}

func decodeFragmentRGB(t *testing.T, outputDir, segment string) [3]byte {
	t.Helper()
	fragment, err := os.CreateTemp(t.TempDir(), "segment-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"init.mp4", segment} {
		input, err := os.Open(filepath.Join(outputDir, name))
		if err != nil {
			fragment.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(fragment, input)
		closeErr := input.Close()
		if copyErr != nil {
			fragment.Close()
			t.Fatal(copyErr)
		}
		if closeErr != nil {
			fragment.Close()
			t.Fatal(closeErr)
		}
	}
	if err := fragment.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-i", fragment.Name(),
		"-map", "0:v:0", "-frames:v", "1", "-vf", "scale=1:1,format=rgb24", "-f", "rawvideo", "-",
	).Output()
	if err != nil {
		t.Fatalf("decode segment %s: %v", segment, err)
	}
	if len(output) != 3 {
		t.Fatalf("decoded RGB frame has %d bytes, want 3", len(output))
	}
	return [3]byte(output)
}

func assertRGBNear(t *testing.T, got, want [3]byte, tolerance int) {
	t.Helper()
	for channel := range got {
		difference := int(got[channel]) - int(want[channel])
		if difference < 0 {
			difference = -difference
		}
		if difference > tolerance {
			t.Fatalf("decoded RGB = %v, want %v within %d", got, want, tolerance)
		}
	}
}

func TestRealFFmpegTranscodePreservesRotatedDisplayGeometry(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("FLIXR_REQUIRE_MEDIA_INTEGRATION") == "1" {
				t.Fatalf("%s is required for media integration", tool)
			}
			t.Skipf("%s is unavailable", tool)
		}
	}
	work := t.TempDir()
	base := filepath.Join(work, "base.mp4")
	rotated := filepath.Join(work, "rotated.mp4")
	for _, command := range [][]string{
		{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24:duration=0.2", "-c:v", "mpeg4", "-q:v", "5", base},
		{"-hide_banner", "-loglevel", "error", "-y", "-display_rotation:v:0", "90", "-i", base, "-c", "copy", rotated},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		output, err := exec.CommandContext(ctx, "ffmpeg", command...).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("generate rotated fixture: %v\n%s", err, output)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, rotated)
	}))
	defer server.Close()
	outputDir := filepath.Join(work, "output")
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, args, err := ffmpegCommand(Plan{Kind: Transcode}, server.URL, "", -1, outputDir, 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
		t.Fatalf("transcode rotated fixture: %v\n%s", err, output)
	}
	rendered := probeAV(t, filepath.Join(outputDir, "index.m3u8"))
	if rendered.Video.Width != 1080 || rendered.Video.Height != 1920 {
		t.Fatalf("rotated output geometry = %dx%d, want 1080x1920", rendered.Video.Width, rendered.Video.Height)
	}
}

type probedAV struct {
	Video struct {
		Codec, Profile, PixelFormat, FrameRate string
		Level, Width, Height                   int
	}
	Audio struct {
		Codec, Profile       string
		Channels, SampleRate int
	}
}

func probeAV(t *testing.T, path string) probedAV {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name,profile,level,width,height,pix_fmt,avg_frame_rate,channels,sample_rate", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("probe rendition %s: %v", path, err)
	}
	var value struct {
		Streams []struct {
			CodecType   string `json:"codec_type"`
			CodecName   string `json:"codec_name"`
			Profile     string `json:"profile"`
			PixelFormat string `json:"pix_fmt"`
			FrameRate   string `json:"avg_frame_rate"`
			SampleRate  string `json:"sample_rate"`
			Level       int    `json:"level"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Channels    int    `json:"channels"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatal(err)
	}
	var result probedAV
	for _, stream := range value.Streams {
		switch stream.CodecType {
		case "video":
			result.Video.Codec, result.Video.Profile, result.Video.PixelFormat, result.Video.FrameRate = stream.CodecName, stream.Profile, stream.PixelFormat, stream.FrameRate
			result.Video.Level, result.Video.Width, result.Video.Height = stream.Level, stream.Width, stream.Height
		case "audio":
			if result.Audio.Codec == "" {
				result.Audio.Codec, result.Audio.Profile, result.Audio.Channels = stream.CodecName, stream.Profile, stream.Channels
				result.Audio.SampleRate, _ = strconv.Atoi(stream.SampleRate)
			}
		}
	}
	return result
}

func assertPlaylistRendition(t *testing.T, kind Kind, source, manifest string) {
	t.Helper()
	input, output := probeAV(t, source), probeAV(t, manifest)
	if kind == Remux {
		if output != input {
			t.Fatalf("remux changed primary streams: input=%#v output=%#v", input, output)
		}
		return
	}
	if output.Video.Codec != "h264" || output.Video.Profile != "High" || output.Video.Level != 40 || output.Video.PixelFormat != "yuv420p" || output.Video.FrameRate != "30/1" || output.Video.Width != input.Video.Width || output.Video.Height != input.Video.Height ||
		output.Audio.Codec != "aac" || output.Audio.Profile != "LC" || output.Audio.Channels != 2 || output.Audio.SampleRate != 48000 {
		t.Fatalf("transcode rendition = %#v from %#v", output, input)
	}
}

func assertFragmentedMP4Segments(t *testing.T, dir string, manifest []byte) {
	t.Helper()
	segment := regexp.MustCompile(`segment-[0-9]+\.m4s`).Find(manifest)
	if segment == nil {
		t.Fatalf("manifest has no media segment:\n%s", manifest)
	}
	combined := filepath.Join(t.TempDir(), "fragment.mp4")
	out, err := os.Create(combined)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"init.mp4", string(segment)} {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			out.Close()
			t.Fatal(err)
		}
		if _, err := out.Write(contents); err != nil {
			out.Close()
			t.Fatal(err)
		}
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	format, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=format_name", "-of", "default=nw=1:nk=1", combined).Output()
	if err != nil || !strings.Contains(string(format), "mp4") {
		t.Fatalf("fragment container = %q, err = %v", format, err)
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
	videoBitrate, audioCodec, audioBitrate := fixtureBitrateEvidence(t, fixture)
	name, args, err := ffmpegCommand(Plan{Kind: Remux, VideoBitrate: videoBitrate, AudioCodec: audioCodec, AudioBitrate: audioBitrate}, server.URL, "", 2, dir, 0, time.Minute)
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
	_, audioCodec, audioBitrate = fixtureBitrateEvidence(t, sidecar)
	name, args, err = ffmpegCommand(Plan{Kind: Remux, VideoBitrate: videoBitrate, AudioCodec: audioCodec, AudioBitrate: audioBitrate}, server.URL, audioServer.URL, 0, externalDir, 0, time.Minute)
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
		name, args, err := ffmpegCommand(Plan{Kind: Transcode}, server.URL, "", -1, dir, 0, time.Minute)
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
