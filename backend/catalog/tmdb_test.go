package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTMDBRejectsUnsafeAndOversizedArtwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte(strings.Repeat("x", maxArtworkBody+1)))
	}))
	defer server.Close()
	tmdb := NewTMDBWithOrigins(server.Client(), server.URL, server.URL)
	if _, err := tmdb.FetchArtwork(context.Background(), "https://attacker.example/x.jpg"); err == nil {
		t.Fatal("unsafe provider path was accepted")
	}
	if requests != 0 {
		t.Fatalf("unsafe path made %d requests", requests)
	}
	if _, err := tmdb.FetchArtwork(context.Background(), "/poster.jpg"); err == nil {
		t.Fatal("oversized image was accepted")
	}
}

func TestTMDBRejectsNonImageArtwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("not an image"))
	}))
	defer server.Close()
	if _, err := NewTMDBWithOrigins(server.Client(), server.URL, server.URL).FetchArtwork(context.Background(), "/poster.jpg"); err == nil {
		t.Fatal("non-image content type was accepted")
	}
}
