// Package webui embeds the built frontend into the panel binary, so deploying
// HyperCraft is copying one file.
package webui

import (
	"embed"
	"errors"
	"io/fs"
)

// The frontend lives in ../../web and Vite is configured to build into this
// directory. `all:` is required so Vite's hashed assets — and any dotfile it
// emits — are included rather than skipped.
//
//go:embed all:dist
var embedded embed.FS

// ErrNotBuilt means the binary was compiled without a frontend build. The API
// still works; only the UI is missing.
var ErrNotBuilt = errors.New("frontend not built: run `make web` (or `npm --prefix web run build`) before building the binary")

// Dist returns the built frontend as a filesystem rooted at index.html.
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, ErrNotBuilt
	}
	return sub, nil
}
