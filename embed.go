// Package uruni holds the built React bundle the server binary ships with.
// It lives at the repo root because `go:embed` reaches only files under the
// package directory, and the bundle is built into web/dist (ADR-001).
package uruni

import (
	"embed"
	"io/fs"
)

// web/dist is gitignored except .gitkeep: the directory must exist for this
// directive to compile, hence the committed placeholder. The `all:` prefix
// makes .gitkeep embeddable, so a fresh clone builds before Vite has run.
//
//go:embed all:web/dist
var dist embed.FS

// WebAssets returns the built SPA rooted at web/dist, so paths are served as
// the browser asks ("/assets/index.js", not "/web/dist/assets/...").
func WebAssets() (fs.FS, error) {
	return fs.Sub(dist, "web/dist")
}
