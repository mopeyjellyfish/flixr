// Package preview produces bounded, optional chapter and seek-preview assets.
// Its work is independent of playback admission and never gates the first frame.
package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("preview worker busy")
var ErrInvalid = errors.New("invalid preview")

const maxOutput = 512 << 10
const maxEntries = 64 // at most 32 MiB of frame data, plus bounded chapter JSON

type Chapter struct {
	Title   string `json:"title"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}
type entry struct {
	data []byte
	used uint64
}
type Service struct {
	mu    sync.Mutex
	cache map[string]entry
	tick  uint64
	slot  chan struct{}
	run   func(context.Context, *os.File, bool, int64) ([]byte, error)
}

func New() *Service {
	return &Service{cache: map[string]entry{}, slot: make(chan struct{}, 1), run: command}
}
func (s *Service) data(ctx context.Context, file *os.File, key string, frame bool, position int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.tick++
	cached, ok := s.cache[key]
	if ok {
		cached.used = s.tick
		s.cache[key] = cached
	}
	s.mu.Unlock()
	if ok {
		return bytes.Clone(cached.data), nil
	}
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		return nil, ErrBusy
	}
	data, err := s.run(ctx, file, frame, position)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > maxOutput {
		return nil, ErrInvalid
	}
	if frame && http.DetectContentType(data) != "image/jpeg" {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= maxEntries {
		var oldest string
		age := ^uint64(0)
		for k, v := range s.cache {
			if v.used < age {
				oldest, age = k, v.used
			}
		}
		delete(s.cache, oldest)
	}
	s.tick++
	s.cache[key] = entry{bytes.Clone(data), s.tick}
	return data, nil
}
func (s *Service) Frame(ctx context.Context, file *os.File, source string, position int64) ([]byte, error) {
	if position < 0 || position > 7*24*60*60*1000 {
		return nil, ErrInvalid
	}
	// A single real frame represents each ten-second timeline interval.
	position = position / 10000 * 10000
	return s.data(ctx, file, fmt.Sprintf("%s:frame:%d", source, position), true, position)
}
func (s *Service) Chapters(ctx context.Context, file *os.File, source string) ([]Chapter, error) {
	data, err := s.data(ctx, file, source+":chapters", false, 0)
	if err != nil {
		return nil, err
	}
	var response struct {
		Chapters []struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
			Tags  struct {
				Title string `json:"title"`
			} `json:"tags"`
		} `json:"chapters"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Chapters) > 1000 {
		return nil, ErrInvalid
	}
	result := make([]Chapter, 0, len(response.Chapters))
	for _, item := range response.Chapters {
		start, e1 := strconv.ParseFloat(item.Start, 64)
		end, e2 := strconv.ParseFloat(item.End, 64)
		if e1 != nil || e2 != nil || math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > 7*24*60*60 {
			continue
		}
		title := strings.TrimSpace(item.Tags.Title)
		if len(title) > 512 {
			title = string([]rune(title)[:min(128, len([]rune(title)))])
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", len(result)+1)
		}
		result = append(result, Chapter{title, int64(start * 1000), int64(end * 1000)})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].StartMS < result[j].StartMS })
	return result, nil
}

type boundedWriter struct{ bytes.Buffer }

func (w *boundedWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > maxOutput {
		return 0, ErrInvalid
	}
	return w.Buffer.Write(p)
}
func command(ctx context.Context, file *os.File, frame bool, position int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	fd := "/proc/self/fd/3"
	if runtime.GOOS != "linux" {
		fd = "/dev/fd/3"
	}
	formats := "mov,matroska,webm,avi,mpegts,mpeg,mpegvideo,ogg,flv,asf"
	name := "ffprobe"
	args := []string{"-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", formats, "-show_chapters", "-of", "json", fd}
	if frame {
		name = "ffmpeg"
		args = []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe", "-format_whitelist", formats, "-ss", strconv.FormatFloat(float64(position)/1000, 'f', 3, 64), "-i", fd, "-map", "0:v:0", "-an", "-sn", "-frames:v", "1", "-vf", "scale=320:180:force_original_aspect_ratio=decrease,pad=320:180:(ow-iw)/2:(oh-ih)/2", "-threads", "1", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1"}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.ExtraFiles = []*os.File{file}
	cmd.WaitDelay = time.Second
	cmd.Stderr = io.Discard
	var output boundedWriter
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
