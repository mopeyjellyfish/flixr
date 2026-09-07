// Package playback owns capability planning and bounded media generations.
package playback

import (
	"errors"
	"strings"
)

var (
	ErrUnsupported         = errors.New("media is unsupported by this client")
	ErrFFmpegUnavailable   = errors.New("ffmpeg is unavailable")
	ErrCapacity            = errors.New("playback capacity is unavailable")
	ErrPreparing           = errors.New("matching playback generation is preparing")
	ErrSessionInvalid      = errors.New("playback session is invalid")
	ErrInvalidSettings     = errors.New("playback settings are invalid")
	ErrRestartRequired     = errors.New("playback setting requires restart")
	ErrInvalidCapabilities = errors.New("playback capabilities are invalid")
	ErrUnknownCapability   = errors.New("playback capability is unknown")
)

// MediaProperties is the path-free media description consumed by the planner.
type MediaProperties struct {
	Container      string   `json:"container"`
	VideoCodec     string   `json:"video_codec"`
	VideoProfile   string   `json:"video_profile,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
	FrameRateMilli int      `json:"frame_rate_milli,omitempty"`
	BitDepth       int      `json:"bit_depth,omitempty"`
	HDR            string   `json:"hdr,omitempty"`
	AudioCodec     string   `json:"audio_codec"`
	AudioChannels  int      `json:"audio_channels,omitempty"`
	Subtitles      []string `json:"subtitles,omitempty"`
}

// ClientCapabilities declares exact original-media and fMP4 HLS support.
type ClientCapabilities struct {
	Containers        []string `json:"containers"`
	VideoCodecs       []string `json:"video_codecs"`
	VideoProfiles     []string `json:"video_profiles,omitempty"`
	AudioCodecs       []string `json:"audio_codecs"`
	SupportsFMP4HLS   bool     `json:"supports_fmp4_hls"`
	SupportsDirect    bool     `json:"supports_direct"`
	MaxWidth          int      `json:"max_width,omitempty"`
	MaxHeight         int      `json:"max_height,omitempty"`
	MaxFrameRateMilli int      `json:"max_frame_rate_milli,omitempty"`
	MaxBitDepth       int      `json:"max_bit_depth,omitempty"`
	MaxAudioChannels  int      `json:"max_audio_channels,omitempty"`
	HDR               []string `json:"hdr,omitempty"`
}

type ServerReadiness struct {
	FFmpeg bool `json:"ffmpeg"`
}

type Kind string

const (
	Direct      Kind = "direct"
	Remux       Kind = "remux"
	Transcode   Kind = "transcode"
	Unsupported Kind = "unsupported"
)

// Plan describes the selected path and its server-controlled output rendition.
type Plan struct {
	Kind        Kind   `json:"kind"`
	Container   string `json:"container,omitempty"`
	VideoCodec  string `json:"video_codec,omitempty"`
	AudioCodec  string `json:"audio_codec,omitempty"`
	Description string `json:"description,omitempty"`
}

// PlanFor selects the least expensive compatible path from recorded plain values.
func PlanFor(media MediaProperties, client ClientCapabilities, ready ServerReadiness) (Plan, error) {
	var err error
	if client, err = client.Normalized(); err != nil {
		return Plan{}, err
	}
	if directCompatible(media, client) {
		return Plan{
			Kind:        Direct,
			Container:   media.Container,
			VideoCodec:  media.VideoCodec,
			AudioCodec:  media.AudioCodec,
			Description: "Original media",
		}, nil
	}
	if !fallbackCompatible(client) {
		return Plan{Kind: Unsupported}, ErrUnsupported
	}
	if !ready.FFmpeg {
		return Plan{}, ErrFFmpegUnavailable
	}
	if exceedsKnownLimits(media, client) {
		return Plan{Kind: Unsupported}, ErrUnsupported
	}
	if codecCompatible(media, client) {
		return Plan{Kind: Remux, Container: "fmp4-hls", VideoCodec: media.VideoCodec, AudioCodec: media.AudioCodec, Description: "Stream-copy fMP4 HLS"}, nil
	}
	return Plan{Kind: Transcode, Container: "fmp4-hls", VideoCodec: "h264", AudioCodec: "aac", Description: "H.264/AAC compatibility stream"}, nil
}

func exceedsKnownLimits(media MediaProperties, client ClientCapabilities) bool {
	return media.Width > 0 && client.MaxWidth > 0 && media.Width > client.MaxWidth ||
		media.Height > 0 && client.MaxHeight > 0 && media.Height > client.MaxHeight ||
		media.FrameRateMilli > 0 && client.MaxFrameRateMilli > 0 && media.FrameRateMilli > client.MaxFrameRateMilli ||
		media.BitDepth > 0 && client.MaxBitDepth > 0 && media.BitDepth > client.MaxBitDepth ||
		media.AudioChannels > 0 && client.MaxAudioChannels > 0 && media.AudioChannels > client.MaxAudioChannels ||
		media.HDR != "" && len(client.HDR) > 0 && !containsFold(client.HDR, media.HDR)
}

func (client ClientCapabilities) Normalized() (ClientCapabilities, error) {
	for _, values := range [][]string{client.Containers, client.VideoCodecs, client.VideoProfiles, client.AudioCodecs, client.HDR} {
		if len(values) > 16 {
			return ClientCapabilities{}, ErrInvalidCapabilities
		}
		for _, value := range values {
			if value == "" || len(value) > 64 {
				return ClientCapabilities{}, ErrInvalidCapabilities
			}
		}
	}
	for _, value := range []int{client.MaxWidth, client.MaxHeight, client.MaxFrameRateMilli, client.MaxBitDepth, client.MaxAudioChannels} {
		if value < 0 || value > 100000000 {
			return ClientCapabilities{}, ErrInvalidCapabilities
		}
	}
	var err error
	if client.Containers, err = normalizeValues(client.Containers, containerNames); err != nil {
		return ClientCapabilities{}, err
	}
	if client.VideoCodecs, err = normalizeValues(client.VideoCodecs, videoCodecNames); err != nil {
		return ClientCapabilities{}, err
	}
	if client.VideoProfiles, err = normalizeValues(client.VideoProfiles, videoProfileNames); err != nil {
		return ClientCapabilities{}, err
	}
	if client.AudioCodecs, err = normalizeValues(client.AudioCodecs, audioCodecNames); err != nil {
		return ClientCapabilities{}, err
	}
	if client.HDR, err = normalizeValues(client.HDR, hdrNames); err != nil {
		return ClientCapabilities{}, err
	}
	return client, nil
}

// Validate retains the shape-only public check for callers that do not plan media.
func (client ClientCapabilities) Validate() error { _, err := client.Normalized(); return err }

var (
	containerNames    = map[string]string{"mp4": "mp4", "mov": "mp4", "m4a": "mp4", "3gp": "mp4", "3g2": "mp4", "mj2": "mp4", "webm": "webm", "matroska": "matroska", "mkv": "matroska"}
	videoCodecNames   = map[string]string{"h264": "h264", "avc": "h264", "avc1": "h264", "vp9": "vp9", "hevc": "hevc", "h265": "hevc", "mpeg4": "mpeg4", "mpeg2video": "mpeg2video"}
	videoProfileNames = map[string]string{"baseline": "Baseline", "main": "Main", "high": "High"}
	audioCodecNames   = map[string]string{"aac": "aac", "mp4a": "aac", "opus": "opus", "mp3": "mp3", "mp2": "mp2"}
	hdrNames          = map[string]string{"smpte2084": "smpte2084", "arib-std-b67": "arib-std-b67"}
)

func normalizeValues(values []string, known map[string]string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		canonical, ok := known[strings.ToLower(strings.TrimSpace(value))]
		if !ok {
			return nil, ErrUnknownCapability
		}
		normalized = append(normalized, canonical)
	}
	return normalized, nil
}

func directCompatible(media MediaProperties, client ClientCapabilities) bool {
	return client.SupportsDirect && containerCompatible(media.Container, client.Containers) && codecCompatible(media, client) && limitsCompatible(media, client)
}

func limitsCompatible(media MediaProperties, client ClientCapabilities) bool {
	if media.Width > 0 && (client.MaxWidth <= 0 || media.Width > client.MaxWidth) || media.Height > 0 && (client.MaxHeight <= 0 || media.Height > client.MaxHeight) || media.FrameRateMilli > 0 && (client.MaxFrameRateMilli <= 0 || media.FrameRateMilli > client.MaxFrameRateMilli) || media.BitDepth > 0 && (client.MaxBitDepth <= 0 || media.BitDepth > client.MaxBitDepth) || media.AudioChannels > 0 && (client.MaxAudioChannels <= 0 || media.AudioChannels > client.MaxAudioChannels) {
		return false
	}
	return media.HDR == "" || containsFold(client.HDR, media.HDR)
}

func codecCompatible(media MediaProperties, client ClientCapabilities) bool {
	if !containsFold(client.VideoCodecs, media.VideoCodec) || !containsFold(client.AudioCodecs, media.AudioCodec) {
		return false
	}
	return media.VideoProfile == "" || len(client.VideoProfiles) == 0 || containsFold(client.VideoProfiles, media.VideoProfile)
}

func fallbackCompatible(client ClientCapabilities) bool {
	return client.SupportsFMP4HLS && containsFold(client.VideoCodecs, "h264") && containsFold(client.AudioCodecs, "aac")
}

func containerCompatible(container string, accepted []string) bool {
	for _, candidate := range strings.Split(container, ",") {
		if containsFold(accepted, strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func containsFold(values []string, value string) bool {
	if value == "" {
		return false
	}
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}
