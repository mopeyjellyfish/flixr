package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// frontendAssets always contains placeholder.txt so backend builds do not depend
// on a prior frontend build. Production packaging copies the Vite output here.
//
//go:embed assets
var frontendAssets embed.FS

func frontendHandler() http.Handler {
	assets, err := fs.Sub(frontendAssets, "assets")
	if err != nil {
		panic("web: embedded frontend assets: " + err.Error())
	}
	return frontendHandlerFor(assets)
}

func frontendHandlerFor(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetPath := strings.TrimPrefix(r.URL.Path, "/")
		if assetPath != "" {
			if _, err := fs.Stat(assets, assetPath); err == nil {
				if compressedPath := assetPath + ".gz"; isHashedCompressibleAsset(assetPath) {
					if compressed, err := fs.Stat(assets, compressedPath); err == nil {
						w.Header().Add("Vary", "Accept-Encoding")
						if acceptsGzip(r.Header.Get("Accept-Encoding")) {
							w.Header().Set("Content-Encoding", "gzip")
							w.Header().Set("Content-Length", strconv.FormatInt(compressed.Size(), 10))
							w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(assetPath)))
							request := r.Clone(r.Context())
							request.URL.Path += ".gz"
							files.ServeHTTP(w, request)
							return
						}
					}
				}
				files.ServeHTTP(w, r)
				return
			}
		}

		if _, err := fs.Stat(assets, "index.html"); err != nil {
			http.NotFound(w, r)
			return
		}

		request := r.Clone(r.Context())
		request.URL.Path = "/"
		files.ServeHTTP(w, request)
	})
}

func isHashedCompressibleAsset(assetPath string) bool {
	extension := path.Ext(assetPath)
	if extension != ".js" && extension != ".css" {
		return false
	}
	name := strings.TrimSuffix(path.Base(assetPath), extension)
	hashStart := len(name) - 8
	if hashStart < 2 || name[hashStart-1] != '-' {
		return false
	}
	for _, character := range name[hashStart:] {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func acceptsGzip(header string) bool {
	explicit, hasExplicit := 0.0, false
	wildcard, hasWildcard := 0.0, false
	for value := range strings.SplitSeq(header, ",") {
		parts := strings.Split(value, ";")
		encoding := strings.ToLower(strings.TrimSpace(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, ok := strings.Cut(parameter, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
		}
		switch encoding {
		case "gzip":
			explicit, hasExplicit = quality, true
		case "*":
			wildcard, hasWildcard = quality, true
		}
	}
	if hasExplicit {
		return explicit > 0
	}
	return hasWildcard && wildcard > 0
}
