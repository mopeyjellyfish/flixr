package playback

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	hlsSegmentDuration = 2 * time.Second
	// FFmpeg may read four media seconds at up to 8x while preparing a
	// generation, then returns to the sustained 1x input rate.
	hlsInitialReadBurst = 4 * time.Second
	hlsCatchupReadRate  = 8
)

func ffmpegCommand(plan Plan, inputURL, audioInputURL string, audioStreamIndex int, outputDir string, start, segmentWindow time.Duration) (string, []string, error) {
	if _, err := validatedLoopbackURL(inputURL); err != nil {
		return "", nil, err
	}
	if plan.Kind != Remux && plan.Kind != Transcode {
		return "", nil, fmt.Errorf("plan %q does not use FFmpeg", plan.Kind)
	}
	if outputDir == "" || !filepath.IsAbs(outputDir) {
		return "", nil, fmt.Errorf("output directory must be absolute")
	}
	if segmentWindow <= 0 {
		return "", nil, fmt.Errorf("segment window must be positive")
	}
	listSize := int(math.Ceil(float64(segmentWindow) / float64(hlsSegmentDuration)))
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostdin", "-y"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	args = append(args, ffmpegPacedInput(inputURL)...)
	audioInput := 0
	if audioInputURL != "" {
		if _, err := validatedLoopbackURL(audioInputURL); err != nil {
			return "", nil, err
		}
		if start > 0 {
			args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
		}
		args = append(args, ffmpegPacedInput(audioInputURL)...)
		audioInput = 1
	}
	audioMap := fmt.Sprintf("%d:a:0?", audioInput)
	if audioStreamIndex >= 0 {
		audioMap = fmt.Sprintf("%d:%d", audioInput, audioStreamIndex)
	}
	args = append(args, "-map", "0:v:0", "-map", audioMap)
	if plan.Kind == Remux {
		if plan.VideoBitrate <= 0 || plan.AudioCodec != "" && plan.AudioBitrate <= 0 {
			return "", nil, fmt.Errorf("remux bitrate evidence is required")
		}
		args = append(args, "-c", "copy", "-b:v", strconv.FormatInt(plan.VideoBitrate, 10))
		if plan.AudioCodec != "" {
			args = append(args, "-b:a", strconv.FormatInt(plan.AudioBitrate, 10))
		}
	} else {
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-r", strconv.Itoa(compatibilityMaxFrameRate/1000),
			"-b:v", strconv.FormatInt(compatibilityVideoBitrate, 10), "-maxrate", strconv.FormatInt(compatibilityVideoBitrate, 10), "-bufsize", strconv.FormatInt(compatibilityVideoBitrate*2, 10),
			"-force_key_frames", "expr:gte(t,n_forced*"+strconv.FormatFloat(hlsSegmentDuration.Seconds(), 'f', -1, 64)+")", "-sc_threshold", "0",
			"-c:a", "aac", "-ac", strconv.Itoa(compatibilityAudioChannels), "-ar", strconv.Itoa(compatibilityAudioSampleRate), "-b:a", strconv.FormatInt(compatibilityAudioBitrate, 10),
		)
	}
	args = append(args,
		"-f", "hls",
		"-hls_segment_type", "fmp4",
		"-hls_time", strconv.FormatFloat(hlsSegmentDuration.Seconds(), 'f', -1, 64),
		"-hls_list_size", strconv.Itoa(listSize),
		"-hls_delete_threshold", "2",
		"-hls_flags", "delete_segments+independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(outputDir, "segment-%06d.m4s"),
		"-master_pl_name", "master.m3u8",
		filepath.Join(outputDir, "index.m3u8"),
	)
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return "", nil, fmt.Errorf("command argument contains NUL")
		}
	}
	return "ffmpeg", args, nil
}

func ffmpegPacedInput(inputURL string) []string {
	return []string{
		"-readrate", "1",
		"-readrate_initial_burst", strconv.FormatFloat(hlsInitialReadBurst.Seconds(), 'f', -1, 64),
		"-readrate_catchup", strconv.Itoa(hlsCatchupReadRate),
		"-i", inputURL,
	}
}

func validatedLoopbackURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid loopback input URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("input URL is not loopback")
	}
	return parsed, nil
}
