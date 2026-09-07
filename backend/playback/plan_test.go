package playback

import (
	"errors"
	"testing"
)

func TestPlanFor(t *testing.T) {
	browser := ClientCapabilities{
		Containers:      []string{"mp4"},
		VideoCodecs:     []string{"h264"},
		VideoProfiles:   []string{"High"},
		AudioCodecs:     []string{"aac"},
		SupportsFMP4HLS: true,
		SupportsDirect:  true,
	}
	tests := []struct {
		name   string
		media  MediaProperties
		client ClientCapabilities
		ready  ServerReadiness
		want   Kind
		err    error
	}{
		{"compatible original", MediaProperties{Container: "mov,mp4,m4a", VideoCodec: "h264", VideoProfile: "High", AudioCodec: "aac"}, browser, ServerReadiness{}, Direct, nil},
		{"constrained compatibility output is unsupported", MediaProperties{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 3840, Height: 2160, FrameRateMilli: 60000, BitDepth: 10, AudioChannels: 6, HDR: "smpte2084"}, ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRateMilli: 30000, MaxBitDepth: 8, MaxAudioChannels: 2}, ServerReadiness{FFmpeg: true}, Unsupported, ErrUnsupported},
		{"unknown direct capability falls back", MediaProperties{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"}, ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true}, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"compatible WebM original", MediaProperties{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"}, ClientCapabilities{Containers: []string{"webm"}, VideoCodecs: []string{"vp9"}, AudioCodecs: []string{"opus"}, SupportsDirect: true}, ServerReadiness{}, Direct, nil},
		{"container mismatch", MediaProperties{Container: "matroska,webm", VideoCodec: "h264", VideoProfile: "High", AudioCodec: "aac"}, browser, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"incompatible codecs", MediaProperties{Container: "matroska,webm", VideoCodec: "vp9", AudioCodec: "opus"}, browser, ServerReadiness{FFmpeg: true}, Transcode, nil},
		{"no HLS fallback", MediaProperties{Container: "matroska", VideoCodec: "hevc", AudioCodec: "opus"}, ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}, ServerReadiness{FFmpeg: true}, Unsupported, ErrUnsupported},
		{"remux requires ffmpeg", MediaProperties{Container: "matroska", VideoCodec: "h264", AudioCodec: "aac"}, browser, ServerReadiness{}, "", ErrFFmpegUnavailable},
		{"transcode requires ffmpeg", MediaProperties{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"}, browser, ServerReadiness{}, "", ErrFFmpegUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PlanFor(tt.media, tt.client, tt.ready)
			if got.Kind != tt.want {
				t.Fatalf("kind = %q, want %q", got.Kind, tt.want)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestPlanForRejectsEachKnownLimitBeforeStreamCopy(t *testing.T) {
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRateMilli: 30000, MaxBitDepth: 8, MaxAudioChannels: 2, HDR: []string{"smpte2084"}}
	base := MediaProperties{Container: "matroska", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, FrameRateMilli: 30000, BitDepth: 8, AudioChannels: 2, HDR: "smpte2084"}
	for name, change := range map[string]func(*MediaProperties){
		"width": func(m *MediaProperties) { m.Width = 1921 }, "height": func(m *MediaProperties) { m.Height = 1081 },
		"frame rate": func(m *MediaProperties) { m.FrameRateMilli = 30001 }, "bit depth": func(m *MediaProperties) { m.BitDepth = 9 },
		"audio channels": func(m *MediaProperties) { m.AudioChannels = 3 }, "hdr": func(m *MediaProperties) { m.HDR = "arib-std-b67" },
	} {
		t.Run(name, func(t *testing.T) {
			media := base
			change(&media)
			plan, err := PlanFor(media, client, ServerReadiness{FFmpeg: true})
			if plan.Kind != Unsupported || !errors.Is(err, ErrUnsupported) {
				t.Fatalf("plan = %#v, err = %v", plan, err)
			}
		})
	}
	plan, err := PlanFor(base, client, ServerReadiness{FFmpeg: true})
	if err != nil || plan.Kind != Remux {
		t.Fatalf("boundary plan = %#v, err = %v", plan, err)
	}
}

func TestPlanForDoesNotRemuxWhenTitleEvidenceIsMissing(t *testing.T) {
	media := MediaProperties{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 3840, Height: 2160, FrameRateMilli: 60000, BitDepth: 10, AudioChannels: 6}
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsDirect: false}
	plan, err := PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if plan.Kind != Unsupported || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
	client.MaxWidth, client.MaxHeight, client.MaxFrameRateMilli, client.MaxBitDepth = 3840, 2160, 60000, 10
	plan, err = PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if plan.Kind != Unsupported || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unproven audio plan = %#v, err = %v", plan, err)
	}
}

func TestClientCapabilitiesNormalizesAliasesAndRejectsUnknownValues(t *testing.T) {
	client, err := (ClientCapabilities{Containers: []string{"mov"}, VideoCodecs: []string{"avc1"}, VideoProfiles: []string{"high"}, AudioCodecs: []string{"mp4a"}, HDR: []string{"smpte2084"}}).Normalized()
	if err != nil || client.Containers[0] != "mp4" || client.VideoCodecs[0] != "h264" || client.AudioCodecs[0] != "aac" {
		t.Fatalf("normalized = %#v, err = %v", client, err)
	}
	for _, client := range []ClientCapabilities{{Containers: []string{"unknown"}}, {VideoCodecs: []string{"unknown"}}, {VideoProfiles: []string{"unknown"}}, {AudioCodecs: []string{"unknown"}}, {HDR: []string{"unknown"}}} {
		if _, err := client.Normalized(); !errors.Is(err, ErrUnknownCapability) {
			t.Fatalf("error = %v", err)
		}
	}
}
