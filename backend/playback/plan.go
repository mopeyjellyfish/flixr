// Package playback owns capability planning and bounded media generations.
package playback

import (
	"errors"
	"strings"
)

var (
	ErrUnsupported       = errors.New("media is unsupported by this client")
	ErrFFmpegUnavailable = errors.New("ffmpeg is unavailable")
	ErrCapacity          = errors.New("playback capacity is unavailable")
	ErrPreparing         = errors.New("matching playback generation is preparing")
	ErrSessionInvalid    = errors.New("playback session is invalid")
	ErrInvalidSettings   = errors.New("playback settings are invalid")
	ErrRestartRequired   = errors.New("playback setting requires restart")
)

// MediaProperties is the path-free media description consumed by the planner.
type MediaProperties struct {
	Container    string   `json:"container"`
	VideoCodec   string   `json:"video_codec"`
	VideoProfile string   `json:"video_profile,omitempty"`
	AudioCodec   string   `json:"audio_codec"`
	Subtitles    []string `json:"subtitles,omitempty"`
}

// ClientCapabilities declares exact original-media and fMP4 HLS support.
type ClientCapabilities struct {
	Containers      []string `json:"containers"`
	VideoCodecs     []string `json:"video_codecs"`
	VideoProfiles   []string `json:"video_profiles,omitempty"`
	AudioCodecs     []string `json:"audio_codecs"`
	SupportsFMP4HLS bool     `json:"supports_fmp4_hls"`
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
	if codecCompatible(media, client) {
		return Plan{Kind: Remux, Container: "fmp4-hls", VideoCodec: media.VideoCodec, AudioCodec: media.AudioCodec, Description: "Stream-copy fMP4 HLS"}, nil
	}
	return Plan{Kind: Transcode, Container: "fmp4-hls", VideoCodec: "h264", AudioCodec: "aac", Description: "H.264/AAC compatibility stream"}, nil
}

func directCompatible(media MediaProperties, client ClientCapabilities) bool {
	return containerCompatible(media.Container, client.Containers) && codecCompatible(media, client)
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
