//go:build media_integration

package catalog_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/playback"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
)

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
	if want.DurationMS != 2021 || want.PrimaryVideoStreamIndex != 0 || want.Width != 320 || want.Height != 180 || want.Bitrate <= 0 || want.HDR != "" || len(want.Audio) != 2 || len(want.Subtitles) != 2 {
		t.Fatalf("decoded properties = %#v", want.MediaProperties)
	}
	if want.VideoLevel != 12 || want.Bitrate <= 0 || want.Audio[0].Index != 1 || want.Audio[0].Profile != "LC" || want.Audio[0].Language != "eng" || want.Audio[0].Channels != 1 || want.Audio[0].SampleRate != 48000 || want.Audio[0].Bitrate <= 0 || want.Audio[1].Index != 2 || want.Audio[1].Language != "fra" || want.Subtitles[0].Index != 3 || want.Subtitles[0].Language != "eng" || !want.Subtitles[0].Default || !want.Subtitles[0].Forced || want.Subtitles[1].Index != 4 || want.Subtitles[1].Language != "fra" || want.Subtitles[1].Default || want.Subtitles[1].Forced {
		t.Fatalf("decoded tracks = %#v / %#v", want.Audio, want.Subtitles)
	}
	reopened, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := reopened.Item(want.ID)
	if !ok || restored.DurationMS != want.DurationMS || restored.VideoLevel != 12 || restored.PrimaryVideoStreamIndex != 0 || restored.Audio[0].SampleRate != 48000 || restored.Audio[0].Bitrate <= 0 || len(restored.Subtitles) != 2 || !restored.Subtitles[0].Forced {
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
