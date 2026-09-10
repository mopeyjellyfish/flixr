package playback

import (
	"errors"
	"testing"
)

func TestPlanFor(t *testing.T) {
	browser := ClientCapabilities{
		Containers:        []string{"mp4"},
		VideoCodecs:       []string{"h264"},
		VideoProfiles:     []string{"High"},
		AudioCodecs:       []string{"aac"},
		SupportsFMP4HLS:   true,
		SupportsDirect:    true,
		SupportsRemux:     true,
		SupportsTranscode: true,
		MaxWidth:          320,
		MaxHeight:         180,
		MaxFrameRateMilli: 24000,
		MaxBitDepth:       8,
		MaxAudioChannels:  2,
	}
	tests := []struct {
		name   string
		media  MediaProperties
		client ClientCapabilities
		ready  ServerReadiness
		want   Kind
		err    error
	}{
		{"compatible original", MediaProperties{Container: "mov,mp4,m4a", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 12, Width: 320, Height: 180, VideoBitrate: 5968, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioProfile: "LC", AudioChannels: 2, AudioSampleRate: 48000, AudioBitrate: 2323}, browser, ServerReadiness{}, Direct, nil},
		{"source default selection stays original", MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 12, Width: 320, Height: 180, VideoBitrate: 5968, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioProfile: "LC", AudioChannels: 2, AudioSampleRate: 48000, AudioBitrate: 2323, AudioStreamIndex: 2}, browser, ServerReadiness{}, Direct, nil},
		{"non-default selection requires mapping", MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 12, Width: 320, Height: 180, VideoBitrate: 5968, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioProfile: "LC", AudioChannels: 2, AudioSampleRate: 48000, AudioBitrate: 2323, AudioStreamIndex: 3, RequiresAudioMapping: true}, browser, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"external selection requires mapping", MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 12, Width: 320, Height: 180, VideoBitrate: 5968, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioProfile: "LC", AudioChannels: 2, AudioSampleRate: 48000, AudioBitrate: 2323, AudioStreamIndex: 0, AudioExternal: true, RequiresAudioMapping: true}, browser, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"constrained compatibility output is unsupported", MediaProperties{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 3840, Height: 2160, FrameRateMilli: 60000, BitDepth: 10, AudioChannels: 6, HDR: "smpte2084"}, ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRateMilli: 30000, MaxBitDepth: 8, MaxAudioChannels: 2}, ServerReadiness{FFmpeg: true}, Unsupported, ErrUnsupported},
		{"unknown direct capability transcodes only with exact fallback evidence", MediaProperties{Container: "mp4", VideoCodec: "h264", Width: 320, Height: 180, FrameRateMilli: 24000, AudioCodec: "aac"}, ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsTranscode: true}, ServerReadiness{FFmpeg: true}, Transcode, nil},
		{"compatible WebM original", MediaProperties{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"}, ClientCapabilities{Containers: []string{"webm"}, VideoCodecs: []string{"vp9"}, AudioCodecs: []string{"opus"}, SupportsDirect: true}, ServerReadiness{}, Direct, nil},
		{"container mismatch", MediaProperties{Container: "matroska,webm", VideoCodec: "h264", VideoProfile: "High", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, browser, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"incompatible codecs", MediaProperties{Container: "matroska,webm", VideoCodec: "vp9", Width: 320, Height: 180, FrameRateMilli: 24000, AudioCodec: "opus"}, browser, ServerReadiness{FFmpeg: true}, Transcode, nil},
		{"no HLS fallback", MediaProperties{Container: "matroska", VideoCodec: "hevc", AudioCodec: "opus"}, ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}, ServerReadiness{FFmpeg: true}, Unsupported, ErrUnsupported},
		{"remux requires ffmpeg", MediaProperties{Container: "matroska", VideoCodec: "h264", VideoBitrate: 1_000_000, AudioCodec: "aac", AudioBitrate: 128_000}, browser, ServerReadiness{}, "", ErrFFmpegUnavailable},
		{"transcode requires ffmpeg", MediaProperties{Container: "webm", VideoCodec: "vp9", Width: 320, Height: 180, FrameRateMilli: 24000, AudioCodec: "opus"}, browser, ServerReadiness{}, "", ErrFFmpegUnavailable},
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
			if err == nil && (got.AudioStreamIndex != tt.media.AudioStreamIndex || got.AudioExternal != tt.media.AudioExternal) {
				t.Fatalf("audio selection = %d external=%v, want %d external=%v", got.AudioStreamIndex, got.AudioExternal, tt.media.AudioStreamIndex, tt.media.AudioExternal)
			}
		})
	}
}

func TestPlanForUsesEvidenceForTheMatchingOutput(t *testing.T) {
	media := MediaProperties{Container: "matroska,webm", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 12, Width: 320, Height: 180, VideoBitrate: 157945, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioProfile: "LC", AudioChannels: 1, AudioSampleRate: 48000, AudioBitrate: 157945}
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, VideoProfiles: []string{"High"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsRemux: true, SupportsTranscode: true, MaxWidth: 320, MaxHeight: 180, MaxFrameRateMilli: 24000, MaxBitDepth: 8, MaxAudioChannels: 1}

	plan, err := PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if err != nil || plan.Kind != Remux || plan.VideoLevel != 12 || plan.AudioSampleRate != 48000 || plan.AudioBitrate != 157945 {
		t.Fatalf("remux plan = %#v, err = %v", plan, err)
	}

	media.AudioCodec, media.AudioProfile, media.AudioChannels, media.AudioSampleRate, media.AudioBitrate = "mp3", "", 1, 48000, 64000
	plan, err = PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if err != nil || plan.Kind != Transcode || plan.Container != "fmp4-hls" || plan.VideoCodec != "h264" || plan.VideoProfile != "High" || plan.VideoLevel != 40 || plan.VideoBitrate != 5_000_000 || plan.FrameRateMilli != 30000 || plan.BitDepth != 8 || plan.AudioCodec != "aac" || plan.AudioChannels != 2 || plan.AudioSampleRate != 48000 || plan.AudioBitrate != 128_000 || plan.HDR != "" {
		t.Fatalf("transcode plan = %#v, err = %v", plan, err)
	}
}

func TestPlanForRejectsUnassessedMatchingPath(t *testing.T) {
	media := MediaProperties{Container: "matroska", VideoCodec: "h264", VideoProfile: "High", Width: 320, Height: 180, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioChannels: 2}
	base := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, VideoProfiles: []string{"High"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, MaxWidth: 320, MaxHeight: 180, MaxFrameRateMilli: 24000, MaxBitDepth: 8, MaxAudioChannels: 2}
	for name, client := range map[string]ClientCapabilities{
		"remux":     base,
		"transcode": {Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := PlanFor(media, client, ServerReadiness{FFmpeg: true})
			if plan.Kind != Unsupported || !errors.Is(err, ErrUnsupported) {
				t.Fatalf("plan = %#v, err = %v", plan, err)
			}
		})
	}
}

func TestPlanForDoesNotRemuxWithoutMasterPlaylistBitrateEvidence(t *testing.T) {
	media := MediaProperties{Container: "matroska", VideoCodec: "h264", VideoProfile: "High", VideoLevel: 40, Width: 320, Height: 180, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioProfile: "LC", AudioChannels: 2, AudioSampleRate: 48000}
	client := ClientCapabilities{VideoCodecs: []string{"h264"}, VideoProfiles: []string{"High"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsRemux: true, SupportsTranscode: true, MaxWidth: 320, MaxHeight: 180, MaxFrameRateMilli: 24000, MaxBitDepth: 8, MaxAudioChannels: 2}
	plan, err := PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if err != nil || plan.Kind != Transcode {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
}

func TestPlanForRejectsInputsOutsideTheBoundedTranscodeContract(t *testing.T) {
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsTranscode: true}
	base := MediaProperties{Container: "avi", VideoCodec: "mpeg4", Width: 320, Height: 180, FrameRateMilli: 24000, AudioCodec: "mp3"}
	for name, change := range map[string]func(*MediaProperties){
		"odd width":                func(media *MediaProperties) { media.Width = 321 },
		"odd height":               func(media *MediaProperties) { media.Height = 181 },
		"oversize":                 func(media *MediaProperties) { media.Width = 1922 },
		"high frame rate":          func(media *MediaProperties) { media.FrameRateMilli = 30001 },
		"HDR without tone mapping": func(media *MediaProperties) { media.HDR = "smpte2084" },
		"unknown video decoder":    func(media *MediaProperties) { media.VideoCodec = "unknown" },
		"unknown audio decoder":    func(media *MediaProperties) { media.AudioCodec = "unknown" },
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
}

func TestPlanForPreservesBoundedPortraitDisplayGeometry(t *testing.T) {
	media := MediaProperties{Container: "mp4", VideoCodec: "mpeg4", Width: 1080, Height: 1920, FrameRateMilli: 24000, AudioCodec: "mp3"}
	client := ClientCapabilities{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsTranscode: true}
	plan, err := PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if err != nil || plan.Kind != Transcode || plan.Width != 1080 || plan.Height != 1920 {
		t.Fatalf("portrait plan = %#v, err = %v", plan, err)
	}

	media.Width = 1082
	plan, err = PlanFor(media, client, ServerReadiness{FFmpeg: true})
	if plan.Kind != Unsupported || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("oversize portrait plan = %#v, err = %v", plan, err)
	}
}

func TestPlanForRejectsEachKnownLimitBeforeStreamCopy(t *testing.T) {
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, SupportsRemux: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRateMilli: 30000, MaxBitDepth: 8, MaxAudioChannels: 2, HDR: []string{"smpte2084"}}
	base := MediaProperties{Container: "matroska", VideoCodec: "h264", VideoBitrate: 5_000_000, AudioCodec: "aac", AudioBitrate: 128_000, Width: 1920, Height: 1080, FrameRateMilli: 30000, BitDepth: 8, AudioChannels: 2, HDR: "smpte2084"}
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

func TestPlanTreatsConstrainedBaselineAsBaseline(t *testing.T) {
	media := MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "Constrained Baseline", Width: 1920, Height: 1080, VideoBitrate: 4_000_000, AudioCodec: "aac", AudioChannels: 2}
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, VideoProfiles: []string{"Baseline"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, MaxWidth: 1920, MaxHeight: 1080, MaxAudioChannels: 2}
	plan, err := PlanFor(media, client, ServerReadiness{})
	if err != nil || plan.Kind != Direct {
		t.Fatalf("constrained baseline plan = %#v, %v", plan, err)
	}
}

func TestPlanForQualityCapsOutputAndNeverUpscales(t *testing.T) {
	client := ClientCapabilities{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsTranscode: true}
	quality := QualityRequest{Mode: QualityAuto, MaxVideoBitrate: 2_500_000, MaxWidth: 1280, MaxHeight: 720}
	plan, err := PlanForQuality(MediaProperties{Container: "matroska", VideoCodec: "h264", Width: 1920, Height: 1080, VideoBitrate: 8_000_000, FrameRateMilli: 24000, AudioCodec: "aac", AudioBitrate: 256_000}, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != Transcode || plan.Width != 1280 || plan.Height != 720 || plan.VideoBitrate != 2_500_000 || plan.AudioBitrate != 128_000 || plan.Bandwidth != 2_900_000 || plan.QualityMode != QualityAuto {
		t.Fatalf("balanced plan = %#v", plan)
	}
	plan, err = PlanForQuality(MediaProperties{Container: "webm", VideoCodec: "vp9", Width: 640, Height: 360, VideoBitrate: 3_000_000, FrameRateMilli: 24000}, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil || plan.Width != 640 || plan.Height != 360 {
		t.Fatalf("small source was upscaled: %#v, err=%v", plan, err)
	}
	plan, err = PlanForQuality(MediaProperties{Container: "webm", VideoCodec: "vp9", Width: 1080, Height: 1920, VideoBitrate: 8_000_000, FrameRateMilli: 24000}, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil || plan.Width != 720 || plan.Height != 1280 {
		t.Fatalf("portrait cap lost display orientation: %#v, err=%v", plan, err)
	}
}

func TestPlanForQualityUsesCopyOnlyWithCapEvidence(t *testing.T) {
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, VideoProfiles: []string{"High"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, SupportsRemux: true, SupportsTranscode: true, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRateMilli: 30000, MaxBitDepth: 8, MaxAudioChannels: 2}
	quality := QualityRequest{Mode: QualityDataSaver, MaxVideoBitrate: 1_000_000, MaxWidth: 854, MaxHeight: 480}
	media := MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", Width: 854, Height: 480, FrameRateMilli: 24000, BitDepth: 8, AudioCodec: "aac", AudioChannels: 2}
	plan, err := PlanForQuality(media, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil || plan.Kind != Transcode {
		t.Fatalf("unknown source bitrate bypassed cap: %#v, err=%v", plan, err)
	}
	media.VideoBitrate, media.AudioBitrate = 800_000, 96_000
	plan, err = PlanForQuality(media, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil || plan.Kind != Direct || plan.VideoBitrate != 800_000 {
		t.Fatalf("proven source within cap not copied: %#v, err=%v", plan, err)
	}
	media.AudioBitrate = 256_000
	plan, err = PlanForQuality(media, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil || plan.Kind != Transcode || plan.AudioBitrate != 96_000 {
		t.Fatalf("source audio bypassed saver cap: %#v, err=%v", plan, err)
	}
}

func TestPlanForQualityAutoLowLeavesTransportHeadroom(t *testing.T) {
	client := ClientCapabilities{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SupportsFMP4HLS: true, SupportsTranscode: true}
	quality := QualityRequest{Mode: QualityAuto, MaxVideoBitrate: 500_000, MaxWidth: 640, MaxHeight: 360}
	media := MediaProperties{Container: "matroska", VideoCodec: "mpeg4", Width: 1280, Height: 720, VideoBitrate: 8_000_000, FrameRateMilli: 24000, AudioCodec: "aac", AudioBitrate: 128_000}
	plan, err := PlanForQuality(media, client, ServerReadiness{FFmpeg: true}, quality)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != Transcode || plan.Width != 640 || plan.Height != 360 || plan.VideoBitrate != 500_000 || plan.AudioBitrate != 64_000 || plan.Bandwidth != 630_000 {
		t.Fatalf("unexpected low Auto plan: %#v", plan)
	}
}

func TestQualityRequestRejectsUnboundedOrMismatchedLimits(t *testing.T) {
	for _, quality := range []QualityRequest{
		{Mode: "fast", MaxVideoBitrate: 1_000_000, MaxWidth: 854, MaxHeight: 480},
		{Mode: QualityDataSaver, MaxVideoBitrate: 9_000_000, MaxWidth: 854, MaxHeight: 480},
		{Mode: QualityOriginal, MaxVideoBitrate: 1, MaxWidth: 1, MaxHeight: 1},
	} {
		if _, err := quality.Normalized(); !errors.Is(err, ErrInvalidQuality) {
			t.Fatalf("quality %#v error=%v, want invalid quality", quality, err)
		}
	}
}

func TestOriginalKeepsCompatibleHighResolutionHDRDirectPlayback(t *testing.T) {
	media := MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", Width: 3840, Height: 2160, VideoBitrate: 20_000_000, FrameRateMilli: 60000, BitDepth: 10, HDR: "smpte2084", AudioCodec: "aac", AudioChannels: 2, AudioBitrate: 128_000}
	client := ClientCapabilities{Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, VideoProfiles: []string{"High"}, AudioCodecs: []string{"aac"}, SupportsDirect: true, SupportsFMP4HLS: true, SupportsTranscode: true, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRateMilli: 60000, MaxBitDepth: 10, MaxAudioChannels: 2, HDR: []string{"smpte2084"}}
	plan, err := PlanForQuality(media, client, ServerReadiness{FFmpeg: true}, QualityRequest{Mode: QualityOriginal})
	if err != nil || plan.Kind != Direct {
		t.Fatalf("compatible original plan = %#v, err=%v", plan, err)
	}
	auto := QualityRequest{Mode: QualityAuto, MaxVideoBitrate: 2_500_000, MaxWidth: 1280, MaxHeight: 720}
	if _, err := PlanForQuality(media, client, ServerReadiness{FFmpeg: true}, auto); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("capped HDR error=%v, want unsupported so the client can offer explicit Original", err)
	}
}
