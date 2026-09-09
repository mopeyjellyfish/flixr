package web_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

type artworkProvider struct{}

func (artworkProvider) Lookup(context.Context, string, string, string) (catalog.Enrichment, error) {
	return catalog.Enrichment{ProviderID: "provider-id", Poster: "/poster.jpg"}, nil
}
func (artworkProvider) FetchArtwork(context.Context, string) (catalog.Artwork, error) {
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return catalog.Artwork{Bytes: data.Bytes(), ContentType: "image/png"}, nil
}

func TestProfileServesCachedArtwork(t *testing.T) {
	data, films := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("film"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	c.SetProvider(artworkProvider{})
	if err := c.SetTMDBToken("secret"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, _, err := c.Browse("", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	h, err := household.New()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := h.CreateProfile("viewer", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.Select(profile.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/artwork/"+items[0].ID+"/poster", nil)
	r.AddCookie(&http.Cookie{Name: "flixr_session", Value: session})
	w := httptest.NewRecorder()
	web.NewServer(h, c).Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("artwork response=%d %q %q %q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"), w.Body.String())
	}
	if decoded, format, err := image.Decode(bytes.NewReader(w.Body.Bytes())); err != nil || format != "png" || decoded.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("decoded artwork=%v %q %v", decoded, format, err)
	}
	w = httptest.NewRecorder()
	web.NewServer(h, c).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/artwork/../../flixr.db/poster", nil))
	if w.Code == http.StatusOK {
		t.Fatalf("path request served artwork: %d %q", w.Code, w.Body.String())
	}
}

type svgArtworkProvider struct{ artworkProvider }

func (svgArtworkProvider) FetchArtwork(context.Context, string) (catalog.Artwork, error) {
	return catalog.Artwork{Bytes: []byte("<svg></svg>"), ContentType: "image/svg+xml"}, nil
}

func TestProviderSVGArtworkIsNotCached(t *testing.T) {
	data, films := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(films, "Film.mp4"), []byte("film"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.OpenWithProber(db, catalog.ProberFunc(func(context.Context, *os.File) (catalog.MediaProperties, error) {
		return catalog.MediaProperties{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	c.SetProvider(svgArtworkProvider{})
	if err := c.SetTMDBToken("secret"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoots(films, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Scan(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	items, _, err := c.Browse("", 0, 1)
	if err != nil || len(items) != 1 || items[0].Poster != "" {
		t.Fatalf("SVG artwork entered catalog: %#v, %v", items, err)
	}
	if _, _, err := c.Artwork(items[0].ID, "poster"); err == nil {
		t.Fatal("SVG artwork was cached")
	}
}
