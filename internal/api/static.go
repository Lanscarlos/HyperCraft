package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/webui"
)

// staticHandler serves the embedded single-page app.
func (s *Server) staticHandler() http.Handler {
	dist, err := webui.Dist()
	if err != nil {
		s.log.Warn("serving without a frontend", "reason", err)
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, webui.ErrNotBuilt.Error())
		})
	}

	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if info, err := fs.Stat(dist, name); err != nil || info.IsDir() {
			// Unknown path: hand it to the SPA router rather than 404ing, so a
			// deep link like /instances/<id> survives a page reload.
			serveIndex(w, r, dist)
			return
		}

		// Vite fingerprints everything under /assets/, so those are safe to
		// cache hard. index.html must never be cached or an upgrade would keep
		// serving the old asset references.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
