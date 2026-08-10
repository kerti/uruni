package uruni

import (
	"io/fs"
	"testing"
)

// The bundle's contents depend on whether Vite has run, but the sub-filesystem
// must resolve either way — that is what lets a fresh clone build before the
// first `make web-build`.
func TestWebAssetsRootsAtWebDist(t *testing.T) {
	assets, err := WebAssets()
	if err != nil {
		t.Fatalf("WebAssets() error = %v", err)
	}
	if _, err := fs.ReadDir(assets, "."); err != nil {
		t.Fatalf("reading the embedded bundle: %v", err)
	}
}
