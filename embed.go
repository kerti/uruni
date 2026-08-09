// Package uruni holds the built React bundle that the server binary ships with.
//
// It lives at the repo root because `go:embed` can only reach files under the
// package's own directory, and the bundle is built into web/dist (ADR-001).
package uruni

import (
	"embed"
	"io/fs"
)

// web/dist is gitignored except for .gitkeep — the directory has to exist for
// this directive to compile, which is why that placeholder is committed. The
// `all:` prefix is what makes .gitkeep itself embeddable, so a fresh clone
// builds before Vite has ever run.
//
//go:embed all:web/dist
var dist embed.FS

// WebAssets returns the built SPA rooted at web/dist, so paths are served as
// the browser asks for them ("/assets/index.js", not "/web/dist/assets/...").
func WebAssets() (fs.FS, error) {
	return fs.Sub(dist, "web/dist")
}
