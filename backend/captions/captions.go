// Package captions converts supported text subtitles into bounded, inert WebVTT cues.
package captions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const extractionTimeout = 10 * time.Second

const (
	MaxInputBytes = 8 << 20
	maxCueCount   = 10_000
	maxCueLine    = 16 << 10
)

var (
	ErrUnsupported = errors.New("unsupported subtitle format")
	ErrTooLarge    = errors.New("subtitle cue stream is too large")
	ErrMalformed   = errors.New("malformed subtitle cue stream")
)

// Convert parses SRT or WebVTT, subtracts one source offset, and emits text-only WebVTT.
func Convert(input []byte, codec string, offsetMS int64) ([]byte, error) {
	if len(input) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "subrip", "srt", "webvtt":
	default:
		return nil, ErrUnsupported
	}
	if offsetMS < 0 {
		offsetMS = 0
	}
	normalized := strings.ReplaceAll(string(input), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	blocks := strings.Split(normalized, "\n\n")
	var output strings.Builder
	output.WriteString("WEBVTT\n")
	cues := 0
	for _, raw := range blocks {
		lines := strings.Split(strings.TrimSpace(raw), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
			continue
		}
		first := strings.TrimSpace(lines[0])
		if strings.EqualFold(first, "WEBVTT") || strings.HasPrefix(first, "NOTE") || first == "STYLE" || first == "REGION" {
			continue
		}
		timingIndex := 0
		if !strings.Contains(first, "-->") && len(lines) > 1 {
			timingIndex = 1
		}
		if timingIndex >= len(lines) || !strings.Contains(lines[timingIndex], "-->") {
			return nil, ErrMalformed
		}
		start, end, err := parseRange(lines[timingIndex])
		if err != nil {
			return nil, err
		}
		if end <= offsetMS {
			continue
		}
		start -= offsetMS
		end -= offsetMS
		if start < 0 {
			start = 0
		}
		if end <= start {
			continue
		}
		if cues >= maxCueCount {
			return nil, ErrTooLarge
		}
		output.WriteString("\n")
		output.WriteString(formatTimestamp(start))
		output.WriteString(" --> ")
		output.WriteString(formatTimestamp(end))
		output.WriteString("\n")
		for _, line := range lines[timingIndex+1:] {
			if len(line) > maxCueLine {
				return nil, ErrTooLarge
			}
			output.WriteString(html.EscapeString(line))
			output.WriteString("\n")
			if output.Len() > MaxInputBytes {
				return nil, ErrTooLarge
			}
		}
		cues++
	}
	return []byte(output.String()), nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (w *limitedBuffer) Write(value []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 || len(value) > remaining {
		if remaining > 0 {
			_, _ = w.buffer.Write(value[:remaining])
		}
		return len(value), ErrTooLarge
	}
	return w.buffer.Write(value)
}

// Extract runs one time-limited, single-threaded embedded text extraction.
func Extract(ctx context.Context, source *os.File, streamIndex int) ([]byte, error) {
	if streamIndex < 0 || source == nil {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()
	output := &limitedBuffer{limit: MaxInputBytes}
	stderr := &limitedBuffer{limit: 64 << 10}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-i", "pipe:0", "-map", fmt.Sprintf("0:%d", streamIndex), "-c:s", "webvtt", "-f", "webvtt", "pipe:1")
	cmd.Stdin = io.LimitReader(source, 16<<30)
	cmd.Stdout = output
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		if errors.Is(err, ErrTooLarge) || output.buffer.Len() >= MaxInputBytes {
			return nil, ErrTooLarge
		}
		return nil, fmt.Errorf("extract embedded subtitle: %w: %s", err, strings.TrimSpace(stderr.buffer.String()))
	}
	return output.buffer.Bytes(), nil
}

func parseRange(line string) (int64, int64, error) {
	parts := strings.SplitN(line, "-->", 2)
	if len(parts) != 2 {
		return 0, 0, ErrMalformed
	}
	start, err := parseTimestamp(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	endField := strings.Fields(strings.TrimSpace(parts[1]))
	if len(endField) == 0 {
		return 0, 0, ErrMalformed
	}
	end, err := parseTimestamp(endField[0])
	if err != nil || end <= start {
		return 0, 0, ErrMalformed
	}
	return start, end, nil
}

func parseTimestamp(value string) (int64, error) {
	value = strings.Replace(value, ",", ".", 1)
	parts := strings.Split(value, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, ErrMalformed
	}
	var hours int64
	if len(parts) == 3 {
		parsed, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || parsed < 0 {
			return 0, ErrMalformed
		}
		hours = parsed
		parts = parts[1:]
	}
	minutes, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || minutes < 0 || minutes > 59 {
		return 0, ErrMalformed
	}
	secondsParts := strings.SplitN(parts[1], ".", 2)
	if len(secondsParts) != 2 || len(secondsParts[1]) != 3 {
		return 0, ErrMalformed
	}
	seconds, err := strconv.ParseInt(secondsParts[0], 10, 64)
	if err != nil || seconds < 0 || seconds > 59 {
		return 0, ErrMalformed
	}
	milliseconds, err := strconv.ParseInt(secondsParts[1], 10, 64)
	if err != nil {
		return 0, ErrMalformed
	}
	return ((hours*60+minutes)*60+seconds)*1000 + milliseconds, nil
}

func formatTimestamp(milliseconds int64) string {
	hours := milliseconds / 3_600_000
	milliseconds %= 3_600_000
	minutes := milliseconds / 60_000
	milliseconds %= 60_000
	seconds := milliseconds / 1000
	milliseconds %= 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, milliseconds)
}
