package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Prober is owned by Catalog because media inspection is part of indexing.
type Prober interface {
	Probe(context.Context, *os.File) (MediaProperties, error)
}

type ProberFunc func(context.Context, *os.File) (MediaProperties, error)

func (f ProberFunc) Probe(ctx context.Context, file *os.File) (MediaProperties, error) {
	return f(ctx, file)
}

type commandRunner interface {
	Output(context.Context, string, []string, []*os.File) ([]byte, error)
}

type execRunner struct{}

type limitedBuffer struct {
	bytes.Buffer
	remaining int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.Buffer.Write(p)
	b.remaining -= int64(n)
	return len(p), err
}

func (execRunner) Output(ctx context.Context, name string, args []string, files []*os.File) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.ExtraFiles = files
	cmd.WaitDelay = time.Second
	out := limitedBuffer{remaining: 1 << 20}
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.Bytes(), err
}

type ffprobe struct{ runner commandRunner }

func newFFprobe() Prober { return ffprobe{runner: execRunner{}} }

// Probe gives ffprobe a descriptor rather than a caller-controlled filesystem path.
func (p ffprobe) Probe(ctx context.Context, file *os.File) (MediaProperties, error) {
	fdPath := "/proc/self/fd/3"
	if runtime.GOOS != "linux" {
		fdPath = "/dev/fd/3"
	}
	out, err := p.runner.Output(ctx, "ffprobe", []string{"-v", "error", "-show_format", "-show_streams", "-of", "json", fdPath}, []*os.File{file})
	if err != nil {
		return MediaProperties{}, fmt.Errorf("ffprobe: %w", err)
	}
	var value struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			Index         *int              `json:"index"`
			CodecType     string            `json:"codec_type"`
			CodecName     string            `json:"codec_name"`
			Profile       string            `json:"profile"`
			Level         int               `json:"level"`
			Channels      int               `json:"channels"`
			SampleRate    string            `json:"sample_rate"`
			Width         int               `json:"width"`
			Height        int               `json:"height"`
			BitRate       string            `json:"bit_rate"`
			AvgFrameRate  string            `json:"avg_frame_rate"`
			RFrameRate    string            `json:"r_frame_rate"`
			BitsPerSample json.Number       `json:"bits_per_sample"`
			BitsPerRaw    json.Number       `json:"bits_per_raw_sample"`
			ColorTransfer string            `json:"color_transfer"`
			Tags          map[string]string `json:"tags"`
			SideDataList  []struct {
				Rotation int `json:"rotation"`
			} `json:"side_data_list"`
			Disposition struct {
				Default int `json:"default"`
				Forced  int `json:"forced"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &value); err != nil {
		return MediaProperties{}, fmt.Errorf("decode ffprobe output: %w", err)
	}
	media := MediaProperties{Container: value.Format.FormatName, PrimaryVideoStreamIndex: -1}
	media.DurationMS = durationMilliseconds(value.Format.Duration)
	formatBitrate := positiveInt64(value.Format.BitRate)
	for _, stream := range value.Streams {
		switch stream.CodecType {
		case "video":
			if media.VideoCodec == "" {
				width, height := nonNegative(stream.Width), nonNegative(stream.Height)
				for _, sideData := range stream.SideDataList {
					rotation := (sideData.Rotation%360 + 360) % 360
					switch rotation {
					case 90, 270:
						width, height = height, width
					case 0, 180:
						continue
					default:
						width, height = 0, 0
					}
					break
				}
				media.VideoCodec = stream.CodecName
				media.VideoProfile, media.VideoLevel = stream.Profile, nonNegative(stream.Level)
				media.PrimaryVideoStreamIndex, media.Width, media.Height, media.HDR = normalizedIndex(stream.Index), width, height, hdrTransfer(stream.ColorTransfer)
				media.FrameRateMilli = max(frameRateMilli(stream.AvgFrameRate), frameRateMilli(stream.RFrameRate))
				media.BitDepth = bitDepth(stream.BitsPerSample, stream.BitsPerRaw)
				media.Bitrate = positiveInt64(stream.BitRate)
				if media.Bitrate == 0 {
					media.Bitrate = formatBitrate
				}
			}
		case "audio":
			bitrate := positiveInt64(stream.BitRate)
			if bitrate == 0 {
				bitrate = formatBitrate
			}
			media.Audio = append(media.Audio, AudioTrack{Index: normalizedIndex(stream.Index), Codec: stream.CodecName, Profile: stream.Profile, Channels: nonNegative(stream.Channels), SampleRate: positiveInt(stream.SampleRate), Bitrate: bitrate, Language: stream.Tags["language"], Title: stream.Tags["title"], Default: stream.Disposition.Default != 0, Forced: stream.Disposition.Forced != 0})
		case "subtitle":
			media.Subtitles = append(media.Subtitles, SubtitleTrack{Index: normalizedIndex(stream.Index), Codec: stream.CodecName, Language: stream.Tags["language"], Title: stream.Tags["title"], Default: stream.Disposition.Default != 0, Forced: stream.Disposition.Forced != 0})
		}
	}
	return media, nil
}

func positiveInt(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func positiveInt64(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func bitDepth(values ...json.Number) int {
	max := 0
	for _, value := range values {
		if parsed, err := strconv.Atoi(value.String()); err == nil && parsed > 0 {
			if parsed > max {
				max = parsed
			}
		}
	}
	return max
}

func hdrTransfer(value string) string {
	switch strings.ToLower(value) {
	case "smpte2084", "arib-std-b67":
		return strings.ToLower(value)
	default:
		return ""
	}
}

func frameRateMilli(value string) int {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return 0
	}
	numerator, numeratorErr := strconv.ParseInt(parts[0], 10, 64)
	denominator, denominatorErr := strconv.ParseInt(parts[1], 10, 64)
	if numeratorErr != nil || denominatorErr != nil || numerator < 0 || denominator <= 0 || numerator > int64(math.MaxInt)/1000 {
		return 0
	}
	return int(numerator * 1000 / denominator)
}

func durationMilliseconds(value string) int64 {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64(math.MaxInt64)/1000 {
		return 0
	}
	return int64(seconds * 1000)
}

// contentFingerprint is a stable identity from sampled bytes and file size, never path.
func contentFingerprint(file *os.File) (string, error) {
	const sample = 64 << 10
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := fmt.Fprintf(hash, "%d\x00", info.Size()); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := io.CopyN(hash, file, sample); err != nil && err != io.EOF {
		return "", err
	}
	if info.Size() > sample {
		if _, err := file.Seek(max(0, info.Size()-sample), io.SeekStart); err != nil {
			return "", err
		}
		if _, err := io.CopyN(hash, file, sample); err != nil && err != io.EOF {
			return "", err
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
