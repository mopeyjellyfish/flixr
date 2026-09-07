//go:build metadata_acceptance

package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
	"github.com/mopeyjellyfish/flixr/backend/household"
	"github.com/mopeyjellyfish/flixr/backend/sqlite"
	"github.com/mopeyjellyfish/flixr/backend/web"
)

type metadataAcceptanceState struct {
	mu       sync.RWMutex
	data     string
	provider *httptest.Server
	inner    *httptest.Server
	db       *sqlite.DB
	catalog  *catalog.Catalog
	target   *url.URL
}

func TestMetadataAcceptanceServer(t *testing.T) {
	port := os.Getenv("FLIXR_METADATA_ACCEPTANCE_PORT")
	if port == "" {
		t.Skip("FLIXR_METADATA_ACCEPTANCE_PORT is required")
	}
	films, err := filepath.Abs(filepath.Join("..", "testdata", "media", "films"))
	if err != nil {
		t.Fatal(err)
	}
	tv, err := filepath.Abs(filepath.Join("..", "testdata", "media", "tv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, media := range []string{filepath.Join(films, "Blue Horizon 2026.mp4"), filepath.Join(tv, "Signal", "Season 01", "Signal S01E01.mkv")} {
		if _, err := os.Stat(media); err != nil {
			t.Fatalf("generated media fixture %q: %v", media, err)
		}
	}

	state := &metadataAcceptanceState{data: t.TempDir(), provider: newMetadataProvider()}
	setupToken := state.startApplication(t, false)
	defer state.close()

	proxy := &httputil.ReverseProxy{Director: func(request *http.Request) {
		state.mu.RLock()
		target := *state.target
		state.mu.RUnlock()
		request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__acceptance/config", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"setup_token": setupToken, "films": films, "tv": tv})
	})
	mux.HandleFunc("POST /__acceptance/restart-offline", func(w http.ResponseWriter, _ *http.Request) {
		state.restartOffline(t)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", proxy)
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("metadata acceptance listening on %s", listener.Addr())
	if err := http.Serve(listener, mux); err != nil {
		t.Fatal(err)
	}
}

func (s *metadataAcceptanceState) startApplication(t *testing.T, offline bool) string {
	t.Helper()
	db, err := sqlite.Open(s.data)
	if err != nil {
		t.Fatal(err)
	}
	home, err := household.Open(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	library, err := catalog.Open(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	providerURL := s.provider.URL
	if offline {
		providerURL = "http://127.0.0.1:1"
	}
	library.SetProvider(catalog.NewTMDBWithOrigins(http.DefaultClient, providerURL, providerURL))
	inner := httptest.NewServer(web.NewServer(home, library).Handler())
	target, _ := url.Parse(inner.URL)
	s.mu.Lock()
	s.db, s.catalog, s.inner, s.target = db, library, inner, target
	s.mu.Unlock()
	return home.SetupToken()
}

func (s *metadataAcceptanceState) restartOffline(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	inner, library, db, provider := s.inner, s.catalog, s.db, s.provider
	s.mu.Unlock()
	inner.Close()
	if err := library.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	provider.Close()
	s.startApplication(t, true)
}

func (s *metadataAcceptanceState) close() {
	s.mu.Lock()
	inner, library, db, provider := s.inner, s.catalog, s.db, s.provider
	s.inner, s.catalog, s.db, s.provider = nil, nil, nil, nil
	s.mu.Unlock()
	if inner != nil {
		inner.Close()
	}
	if library != nil {
		_ = library.Shutdown(context.Background())
	}
	if db != nil {
		_ = db.Close()
	}
	if provider != nil {
		provider.Close()
	}
}

func newMetadataProvider() *httptest.Server {
	var imageData bytes.Buffer
	pixels := image.NewRGBA(image.Rect(0, 0, 3, 2))
	pixels.Set(0, 0, color.RGBA{R: 230, G: 70, B: 90, A: 255})
	_ = png.Encode(&imageData, pixels)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/t/p/w500/") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageData.Bytes())
			return
		}
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/3/authentication":
			_, _ = w.Write([]byte(`{"success":true}`))
		case r.URL.Path == "/3/search/movie":
			query := r.URL.Query().Get("query")
			id := 101
			if strings.Contains(query, "Compatibility") {
				id = 102
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": id, "title": "Curated " + query, "original_title": query, "overview": "Verified film synopsis", "release_date": "2026-01-02", "poster_path": "/film-poster.png", "backdrop_path": "/film-backdrop.png"}}})
		case r.URL.Path == "/3/search/tv":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 300, "name": "Signal Archive", "original_name": r.URL.Query().Get("query"), "overview": "Verified series synopsis", "first_air_date": "2025-03-04", "poster_path": "/series-poster.png", "backdrop_path": "/series-backdrop.png"}}})
		case strings.HasPrefix(r.URL.Path, "/3/tv/300/season/1/episode/"):
			number, _ := strconv.Atoi(filepath.Base(r.URL.Path))
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 400 + number, "name": "Verified Episode " + strconv.Itoa(number), "overview": "Verified episode synopsis", "air_date": "2025-03-05", "still_path": "/episode-still.png"})
		default:
			http.NotFound(w, r)
		}
	}))
}
