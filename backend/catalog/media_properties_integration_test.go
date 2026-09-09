//go:build media_integration

package catalog_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

func TestScheduledPollingFindsRealMediaWithoutFilesystemEvents(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal("ffprobe is required for media integration")
	}
	source := filepath.Join("..", "testdata", "media", "films", "Blue Horizon 2026.mp4")
	media, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "first.mp4"), media, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil || c.SetRoots(root, "") != nil {
		t.Fatalf("catalog: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.StartScanScheduler(ctx, 1); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown(context.Background())
	first, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	first = waitRealScanJob(t, c, first.ID)
	if first.Status != "succeeded" || first.Scanned != 1 {
		t.Fatalf("first job = %#v", first)
	}
	second, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	second = waitRealScanJob(t, c, second.ID)
	if second.Scanned != 0 || second.Skipped != 1 {
		t.Fatalf("unchanged job = %#v", second)
	}
	secondMedia, err := os.ReadFile(filepath.Join("..", "testdata", "media", "films", "Compatibility Check 2026.avi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "second.avi"), secondMedia, 0600); err != nil {
		t.Fatal(err)
	}
	third, err := c.QueueLibraryScan("films", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	third = waitRealScanJob(t, c, third.ID)
	items, err := c.List("", 0, 10)
	if err != nil || third.Scanned != 1 || third.Skipped != 1 || len(items) != 2 {
		t.Fatalf("polling job=%#v items=%d err=%v", third, len(items), err)
	}
}

func waitRealScanJob(t *testing.T, c *catalog.Catalog, id string) catalog.ScanJob {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		job, err := c.ScanJob(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "queued" && job.Status != "running" {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("scan job did not finish")
	return catalog.ScanJob{}
}

func generateMedia(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("generate media: %v\n%s", err, output)
	}
}

func TestCatalogProbesActualVFRAndRotatedDisplayGeometry(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required for media integration", tool)
		}
	}
	root := t.TempDir()
	work := t.TempDir()
	vfr := filepath.Join(root, "VFR Burst Check 2026.mp4")
	base := filepath.Join(work, "rotation-base.mp4")
	rotated := filepath.Join(root, "Rotated Display Check 2026.mp4")
	generateMedia(t, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=60:duration=1", "-vf", "select='eq(mod(n,7),0)+eq(mod(n,7),1)+eq(mod(n,7),2)'", "-fps_mode", "vfr", "-c:v", "libx264", "-pix_fmt", "yuv420p", vfr)
	generateMedia(t, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24:duration=0.2", "-c:v", "mpeg4", "-q:v", "5", base)
	generateMedia(t, "-hide_banner", "-loglevel", "error", "-y", "-display_rotation:v:0", "90", "-i", base, "-c", "copy", rotated)

	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(root, ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Scan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %#v, err = %v", items, err)
	}
	byTitle := make(map[string]catalog.Item, len(items))
	for _, item := range items {
		byTitle[item.Title] = item
	}
	vfrItem := byTitle["VFR Burst Check 2026"]
	if got := vfrItem.FrameRateMilli; got != 60000 {
		t.Fatalf("VFR ceiling = %d, want 60000", got)
	}
	vfrPlan, err := playback.PlanFor(playback.MediaProperties{Container: vfrItem.Container, VideoCodec: vfrItem.VideoCodec, Width: vfrItem.Width, Height: vfrItem.Height, FrameRateMilli: vfrItem.FrameRateMilli, BitDepth: vfrItem.BitDepth}, playback.ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, SupportsRemux: true, SupportsTranscode: true, MaxWidth: 320, MaxHeight: 180, MaxFrameRateMilli: 30000, MaxBitDepth: 8}, playback.ServerReadiness{FFmpeg: true})
	if vfrPlan.Kind != playback.Unsupported || err == nil {
		t.Fatalf("VFR plan = %#v, err = %v", vfrPlan, err)
	}
	portrait := byTitle["Rotated Display Check 2026"]
	if portrait.Width != 1080 || portrait.Height != 1920 {
		t.Fatalf("rotated display geometry = %dx%d, want 1080x1920", portrait.Width, portrait.Height)
	}
	plan, err := playback.PlanFor(playback.MediaProperties{Container: portrait.Container, VideoCodec: portrait.VideoCodec, Width: portrait.Width, Height: portrait.Height, FrameRateMilli: portrait.FrameRateMilli}, playback.ClientCapabilities{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsTranscode: true}, playback.ServerReadiness{FFmpeg: true})
	if err != nil || plan.Kind != playback.Transcode || plan.Width != 1080 || plan.Height != 1920 {
		t.Fatalf("rotated plan = %#v, err = %v", plan, err)
	}
}

func TestCatalogPersistsRealMultitrackCorpusProperties(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "testdata", "media", "tv")
	ctx, cancel := context.WithTimeout(context.Background(), 15_000_000_000)
	defer cancel()
	if err := c.SetRoots("", root); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("List = %#v, %v", items, err)
	}
	var want catalog.Item
	for _, item := range items {
		if item.Episode == 1 {
			want = item
		}
	}
	if want.ID == "" {
		t.Fatalf("episode one missing from %#v", items)
	}
	if want.DurationMS != 2021 || want.PrimaryVideoStreamIndex != 0 || want.Width != 320 || want.Height != 180 || want.Bitrate <= 0 || want.HDR != "" || len(want.Audio) != 3 || len(want.Subtitles) != 2 {
		t.Fatalf("decoded properties = %#v", want.MediaProperties)
	}
	if want.VideoLevel != 12 || want.Bitrate <= 0 || want.Audio[0].Index != 1 || want.Audio[0].Profile != "LC" || want.Audio[0].Language != "eng" || want.Audio[0].Channels != 1 || want.Audio[0].SampleRate != 48000 || want.Audio[0].Bitrate <= 0 || want.Audio[1].Index != 2 || want.Audio[1].Language != "fra" || want.Subtitles[0].Index != 3 || want.Subtitles[0].Language != "eng" || !want.Subtitles[0].Default || !want.Subtitles[0].Forced || want.Subtitles[1].Index != 4 || want.Subtitles[1].Language != "fra" || want.Subtitles[1].Default || want.Subtitles[1].Forced {
		t.Fatalf("decoded tracks = %#v / %#v", want.Audio, want.Subtitles)
	}
	if want.Audio[2].Index != 3 || want.Audio[2].SourceStreamIndex() != 0 || !want.Audio[2].External || want.Audio[2].Profile != "LC" || want.Audio[2].Channels != 1 || want.Audio[2].SampleRate != 48000 || want.Audio[2].Bitrate <= 0 || want.Audio[2].Language != "jpn" || want.Audio[2].Title != "Director Commentary" {
		t.Fatalf("decoded sidecar = %#v", want.Audio[2])
	}
	reopened, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := reopened.Item(want.ID)
	if !ok || restored.DurationMS != want.DurationMS || restored.VideoLevel != 12 || restored.PrimaryVideoStreamIndex != 0 || restored.Audio[0].SampleRate != 48000 || restored.Audio[0].Bitrate <= 0 || len(restored.Audio) != 3 || restored.Audio[2].Profile != "LC" || restored.Audio[2].SampleRate != 48000 || restored.Audio[2].Bitrate <= 0 || len(restored.Subtitles) != 2 || !restored.Subtitles[0].Forced {
		t.Fatalf("reopened properties = %#v", restored)
	}
}

func TestRealH264AACAndIncompatibleAudioFixturesChooseExactPlans(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(filepath.Join("..", "testdata", "media", "films"), filepath.Join("..", "testdata", "media", "tv")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15_000_000_000)
	defer cancel()
	if err := c.Scan(ctx, 2); err != nil {
		t.Fatal(err)
	}
	items, err := c.List("", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	client := playback.ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, VideoProfiles: []string{"High"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsDirect: true, SupportsRemux: true, SupportsTranscode: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRateMilli: 30000, MaxBitDepth: 8, MaxAudioChannels: 2}
	want := map[string]playback.Kind{"Blue Horizon 2026": playback.Direct, "Signal S01E01": playback.Remux, "Compatibility Check 2026": playback.Transcode}
	seen := map[string]bool{}
	for _, item := range items {
		fixtureName := item.Title
		if item.Title == "Signal" && item.Episode == 1 {
			fixtureName = "Signal S01E01"
		}
		kind, ok := want[fixtureName]
		if !ok {
			continue
		}
		if fixtureName == "Compatibility Check 2026" && (len(item.Audio) == 0 || item.Audio[0].Codec != "mp3") {
			t.Fatalf("incompatible-audio fixture = %#v", item.Audio)
		}
		audio := catalog.AudioTrack{}
		if len(item.Audio) > 0 {
			audio = item.Audio[0]
		}
		media := playback.MediaProperties{Container: item.Container, VideoCodec: item.VideoCodec, VideoProfile: item.VideoProfile, VideoLevel: item.VideoLevel, Width: item.Width, Height: item.Height, VideoBitrate: item.Bitrate, FrameRateMilli: item.FrameRateMilli, BitDepth: item.BitDepth, HDR: item.HDR, AudioCodec: audio.Codec, AudioProfile: audio.Profile, AudioChannels: audio.Channels, AudioSampleRate: audio.SampleRate, AudioBitrate: audio.Bitrate}
		plan, err := playback.PlanFor(media, client, playback.ServerReadiness{FFmpeg: true})
		if err != nil || plan.Kind != kind {
			t.Fatalf("%s plan = %#v, err = %v, want %s", fixtureName, plan, err, kind)
		}
		if kind == playback.Transcode && (plan.VideoCodec != "h264" || plan.VideoProfile != "High" || plan.VideoLevel != 40 || plan.VideoBitrate != 5_000_000 || plan.FrameRateMilli != 30000 || plan.BitDepth != 8 || plan.AudioCodec != "aac" || plan.AudioProfile != "LC" || plan.AudioChannels != 2 || plan.AudioSampleRate != 48000 || plan.AudioBitrate != 128_000) {
			t.Fatalf("compatibility rendition = %#v", plan)
		}
		seen[fixtureName] = true
	}
	for title := range want {
		if !seen[title] {
			t.Fatalf("fixture %q missing from %#v", title, items)
		}
	}
}
