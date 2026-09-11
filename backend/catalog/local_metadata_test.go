package catalog

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLocalNFOAllowsBuiltInEntitiesAndReportsUnsupportedFields(t *testing.T) {
	got, err := parseLocalNFO(context.Background(), "film", strings.NewReader(`<movie><title>Fish &amp; Chips</title><plot>Offline</plot><year>2024</year><mpaa>PG</mpaa><tag>Family</tag><genre>Comedy</genre><studio>Ignored</studio><uniqueid type="tmdb">42</uniqueid></movie>`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields["title"] != "Fish & Chips" || got.Fields["synopsis"] != "Offline" || got.Fields["year"] != "2024" || got.Fields["content_rating"] != "PG" || got.Fields["tags"] != "Family" || got.ProviderID != "42" {
		t.Fatalf("parsed = %#v", got)
	}
	if len(got.Ignored) != 2 || got.Ignored[0] != "genre" || got.Ignored[1] != "studio" {
		t.Fatalf("ignored = %#v", got.Ignored)
	}
}

func TestReadLocalArtworkRejectsUnsafeDimensionsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, maxArtworkWidth*4+1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "wide.png"), encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLocalArtwork(context.Background(), root, "wide.png"); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("wide artwork error=%v", err)
	}
	outside := filepath.Join(t.TempDir(), "poster.png")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.png")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := readLocalArtwork(context.Background(), root, "link.png"); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink artwork error=%v", err)
	}
}

func TestParseLocalNFORejectsDocumentTypesAndCustomEntities(t *testing.T) {
	for _, document := range []string{
		`<!DOCTYPE movie SYSTEM "https://example.invalid/remote.dtd"><movie><title>x</title></movie>`,
		`<!DOCTYPE movie [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><movie><title>&xxe;</title></movie>`,
	} {
		if _, err := parseLocalNFO(context.Background(), "film", strings.NewReader(document)); err == nil || !strings.Contains(err.Error(), "document type") {
			t.Fatalf("parse error = %v", err)
		}
	}
}

func TestParseLocalNFORejectsWrongRootAndConflictingProviderIDs(t *testing.T) {
	if _, err := parseLocalNFO(context.Background(), "episode", strings.NewReader(`<movie><title>x</title></movie>`)); err == nil {
		t.Fatal("accepted movie document for episode")
	}
	if _, err := parseLocalNFO(context.Background(), "film", strings.NewReader(`<movie><uniqueid type="tmdb">1</uniqueid><uniqueid type="tmdb">2</uniqueid></movie>`)); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting IDs error = %v", err)
	}
	if got, err := parseLocalNFO(context.Background(), "episode", strings.NewReader(`<episodedetails><title>Episode</title><plot>Plot</plot><aired>2025-03-01</aired><tag>One</tag></episodedetails>`)); err != nil || got.Fields["year"] != "2025" {
		t.Fatalf("episode = %#v, %v", got, err)
	}
}

func TestLocalSidecarCandidatesGuardFlatMovieFolders(t *testing.T) {
	one := localSidecarCandidates("film", "Film/Film.mp4", map[string]int{"Film": 1})
	if !hasCandidate(one.nfo, "Film/Film.nfo") || !hasCandidate(one.nfo, "Film/movie.nfo") {
		t.Fatalf("single movie candidates = %#v", one)
	}
	flat := localSidecarCandidates("film", "Film.mp4", map[string]int{".": 2})
	if hasCandidate(flat.nfo, "movie.nfo") || hasCandidate(flat.poster, "poster.jpg") {
		t.Fatalf("flat folder used directory fallback: %#v", flat)
	}
	series := localSidecarCandidates("series", "Show", nil)
	if !hasCandidate(series.nfo, "Show/tvshow.nfo") || !hasCandidate(series.backdrop, "Show/fanart.jpg") {
		t.Fatalf("series candidates = %#v", series)
	}
}

func hasCandidate(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
