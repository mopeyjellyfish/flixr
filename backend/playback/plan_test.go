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
		{"source default selection stays original", MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", AudioCodec: "aac", AudioStreamIndex: 2}, browser, ServerReadiness{}, Direct, nil},
		{"non-default selection requires mapping", MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", AudioCodec: "aac", AudioStreamIndex: 3, RequiresAudioMapping: true}, browser, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"external selection requires mapping", MediaProperties{Container: "mp4", VideoCodec: "h264", VideoProfile: "High", AudioCodec: "aac", AudioStreamIndex: 0, AudioExternal: true, RequiresAudioMapping: true}, browser, ServerReadiness{FFmpeg: true}, Remux, nil},
		{"compatible WebM original", MediaProperties{Container: "webm", VideoCodec: "vp9", AudioCodec: "opus"}, ClientCapabilities{Containers: []string{"webm"}, VideoCodecs: []string{"vp9"}, AudioCodecs: []string{"opus"}}, ServerReadiness{}, Direct, nil},
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
			if err == nil && (got.AudioStreamIndex != tt.media.AudioStreamIndex || got.AudioExternal != tt.media.AudioExternal) {
				t.Fatalf("audio selection = %d external=%v, want %d external=%v", got.AudioStreamIndex, got.AudioExternal, tt.media.AudioStreamIndex, tt.media.AudioExternal)
			}
		})
	}
}
