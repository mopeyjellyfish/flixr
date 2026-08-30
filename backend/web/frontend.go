package web

import (
	"embed"
	"io/fs"
	"net/http"
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

	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(assets, path); err == nil {
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
