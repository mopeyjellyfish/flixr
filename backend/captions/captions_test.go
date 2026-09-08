package captions_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/captions"
)

func TestConvertRebasesSRTOnceAndEscapesCueMarkup(t *testing.T) {
	input := []byte("1\n00:00:05,000 --> 00:00:06,500\n<img src=x onerror=alert(1)> & hello\n\n2\n00:00:01,000 --> 00:00:03,000\nEarlier\n")
	got, err := captions.Convert(input, "subrip", 2_000)
	if err != nil {
		t.Fatal(err)
	}
	want := "WEBVTT\n\n00:00:03.000 --> 00:00:04.500\n&lt;img src=x onerror=alert(1)&gt; &amp; hello\n\n00:00:00.000 --> 00:00:01.000\nEarlier\n"
	if string(got) != want {
		t.Fatalf("converted cues:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(string(got), "00:00:01.000 --> 00:00:03.000") {
		t.Fatal("source offset was not applied")
	}
}

func TestExtractRejectsInvalidIndexAndCancellation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := captions.Extract(context.Background(), file, -1); !errors.Is(err, captions.ErrUnsupported) {
		t.Fatalf("negative index error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := captions.Extract(ctx, file, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled extraction error = %v", err)
	}
}

func TestConvertSanitizesWebVTTAndDropsNonCueBlocks(t *testing.T) {
	input := []byte("WEBVTT\n\nSTYLE\n::cue { color: red }\n\nNOTE private\nignored\n\n00:00.000 --> 00:01.000 position:50%\n<script>alert(1)</script>\n")
	got, err := captions.Convert(input, "webvtt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "STYLE") || strings.Contains(string(got), "NOTE") || strings.Contains(string(got), "<script>") {
		t.Fatalf("unsafe block survived:\n%s", got)
	}
	if !strings.Contains(string(got), "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("cue was not escaped:\n%s", got)
	}
}

func TestConvertAcceptsUTF8BOMBeforeWebVTTHeader(t *testing.T) {
	input := []byte("\xef\xbb\xbfWEBVTT\n\n00:00.000 --> 00:01.000\nCaption\n")
	got, err := captions.Convert(input, "webvtt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "00:00:00.000 --> 00:00:01.000\nCaption") {
		t.Fatalf("BOM-prefixed WebVTT cue missing:\n%s", got)
	}
}

func TestConvertRejectsUnsupportedAndOversizedInput(t *testing.T) {
	if _, err := captions.Convert([]byte("text"), "ass", 0); !errors.Is(err, captions.ErrUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, err := captions.Convert(make([]byte, captions.MaxInputBytes+1), "webvtt", 0); !errors.Is(err, captions.ErrTooLarge) {
		t.Fatalf("large input error = %v", err)
	}
}
