package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFrontendHandlerNegotiatesPrecompressedHashedAssets(t *testing.T) {
	raw := []byte("console.log('playback engine');")
	compressed := gzipData(t, raw)
	assets := fstest.MapFS{
		"index.html":                   &fstest.MapFile{Data: []byte("<main>Flixr</main>")},
		"assets/player-Ab-2_cd3.js":    &fstest.MapFile{Data: raw},
		"assets/player-Ab-2_cd3.js.gz": &fstest.MapFile{Data: compressed},
		"assets/missing-12345678.js":   &fstest.MapFile{Data: raw},
		"assets/unhashed.js":           &fstest.MapFile{Data: raw},
		"assets/unhashed.js.gz":        &fstest.MapFile{Data: compressed},
	}
	handler := frontendHandlerFor(assets)

	tests := []struct {
		name           string
		acceptEncoding string
		rangeHeader    string
		path           string
		wantStatus     int
		wantEncoding   string
		wantBody       []byte
		wantVary       bool
	}{
		{name: "gzip", acceptEncoding: "br, gzip", path: "/assets/player-Ab-2_cd3.js", wantStatus: http.StatusOK, wantEncoding: "gzip", wantBody: compressed, wantVary: true},
		{name: "wildcard", acceptEncoding: "br, *;q=0.5", path: "/assets/player-Ab-2_cd3.js", wantStatus: http.StatusOK, wantEncoding: "gzip", wantBody: compressed, wantVary: true},
		{name: "explicit gzip rejection overrides wildcard", acceptEncoding: "gzip;q=0, *;q=1", path: "/assets/player-Ab-2_cd3.js", wantStatus: http.StatusOK, wantBody: raw, wantVary: true},
		{name: "raw fallback", path: "/assets/player-Ab-2_cd3.js", wantStatus: http.StatusOK, wantBody: raw, wantVary: true},
		{name: "raw fallback without gzip sibling", acceptEncoding: "gzip", path: "/assets/missing-12345678.js", wantStatus: http.StatusOK, wantBody: raw},
		{name: "range applies to gzip representation", acceptEncoding: "gzip", rangeHeader: "bytes=1-3", path: "/assets/player-Ab-2_cd3.js", wantStatus: http.StatusPartialContent, wantEncoding: "gzip", wantBody: compressed[1:4], wantVary: true},
		{name: "unhashed asset stays raw", acceptEncoding: "gzip", path: "/assets/unhashed.js", wantStatus: http.StatusOK, wantBody: raw},
		{name: "SPA fallback stays raw", acceptEncoding: "gzip", path: "/watch/film-1", wantStatus: http.StatusOK, wantBody: []byte("<main>Flixr</main>")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header.Set("Accept-Encoding", tt.acceptEncoding)
			request.Header.Set("Range", tt.rangeHeader)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if got := response.Header().Get("Content-Encoding"); got != tt.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, tt.wantEncoding)
			}
			if got := response.Header().Values("Vary"); containsHeaderToken(got, "Accept-Encoding") != tt.wantVary {
				t.Fatalf("Vary = %q, want Accept-Encoding=%v", got, tt.wantVary)
			}
			if got := response.Header().Get("Cache-Control"); got != "" {
				t.Fatalf("Cache-Control = %q, want existing empty policy", got)
			}
			if got := response.Body.Bytes(); !bytes.Equal(got, tt.wantBody) {
				t.Fatalf("body = %q, want %q", got, tt.wantBody)
			}
			if strings.HasSuffix(tt.path, ".js") && !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") {
				t.Fatalf("Content-Type = %q, want JavaScript", response.Header().Get("Content-Type"))
			}
		})
	}
}

func TestFrontendHandlerServesCompressedHEAD(t *testing.T) {
	raw := []byte("body { color: white; }")
	compressed := gzipData(t, raw)
	handler := frontendHandlerFor(fstest.MapFS{
		"index.html":                   &fstest.MapFile{Data: []byte("index")},
		"assets/index-1234abcd.css":    &fstest.MapFile{Data: raw},
		"assets/index-1234abcd.css.gz": &fstest.MapFile{Data: compressed},
	})
	request := httptest.NewRequest(http.MethodHead, "/assets/index-1234abcd.css", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d with %d body bytes", response.Code, response.Body.Len())
	}
	if got := response.Header().Get("Content-Length"); got != strconv.Itoa(len(compressed)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(compressed))
	}
	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q, want CSS", got)
	}
}

func gzipData(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, data) {
		t.Fatal("test gzip fixture does not round-trip")
	}
	return compressed.Bytes()
}

func containsHeaderToken(values []string, token string) bool {
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
