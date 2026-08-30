package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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
		} `json:"format"`
		Streams []struct {
			CodecType string            `json:"codec_type"`
			CodecName string            `json:"codec_name"`
			Channels  int               `json:"channels"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &value); err != nil {
		return MediaProperties{}, fmt.Errorf("decode ffprobe output: %w", err)
	}
	media := MediaProperties{Container: value.Format.FormatName}
	for _, stream := range value.Streams {
		switch stream.CodecType {
		case "video":
			if media.VideoCodec == "" {
				media.VideoCodec = stream.CodecName
			}
		case "audio":
			media.Audio = append(media.Audio, AudioTrack{Codec: stream.CodecName, Channels: stream.Channels, Language: stream.Tags["language"]})
		case "subtitle":
			media.Subtitles = append(media.Subtitles, SubtitleTrack{Codec: stream.CodecName, Language: stream.Tags["language"]})
		}
	}
	return media, nil
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
