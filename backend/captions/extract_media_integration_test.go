//go:build media_integration

package captions_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/captions"
)

func TestExtractEmbeddedTextFromRealMedia(t *testing.T) {
	file, err := os.Open("../testdata/media/tv/Signal/Season 01/Signal S01E01.mkv")
	if err != nil {
		t.Skipf("real-media corpus unavailable: %v", err)
	}
	defer file.Close()
	raw, err := captions.Extract(context.Background(), file, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, err := captions.Convert(raw, "webvtt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Signal caption") {
		t.Fatalf("embedded cue missing:\n%s", got)
	}
}
