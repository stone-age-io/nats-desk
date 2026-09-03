package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the embedded app.
//
// Vite emits content-hashed names under /assets, so those are immutable and
// can be cached hard. Everything else - index.html, the manifest, the service
// worker - keeps its name across builds and must not be cached, or a rebuilt
// binary would serve a stale shell pointing at asset names that no longer
// exist.
func spaHandler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}

		if _, err := fs.Stat(assets, p); err != nil {
			// Unknown path. A missing asset should 404 honestly rather than
			// returning HTML - a script tag that silently receives index.html
			// fails much later and much more confusingly.
			if strings.HasPrefix(p, "assets/") {
				http.NotFound(w, r)
				return
			}
			// Anything else is treated as an app route and gets the shell.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			p = "index.html"
		}

		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
