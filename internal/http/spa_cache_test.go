package http

import (
	"net/http"
	"strings"
	"testing"
)

// #506: Vite's hashed output caches for a year; a new build gets new names.
func TestHashedAssetsCacheAsImmutable(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/assets/app.js", "/assets/app.css"} {
		rec := get(t, p)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", p, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("GET %s Cache-Control = %q, want public, max-age=31536000, immutable", p, got)
		}
	}
}

// Only assets/ is hashed: the shell, sw.js and the stable-named files never
// get the year-long header.
func TestOnlyHashedAssetsCacheAsImmutable(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/", "/sw.js", "/favicon.svg", "/manifest.webmanifest", "/history/transactions"} {
		if got := get(t, p).Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
			t.Errorf("GET %s Cache-Control = %q, want no immutable", p, got)
		}
	}
}

// A directory is never listed: it reads as a client-side route and gets the
// shell, uncacheable like any shell response.
func TestADirectoryPathServesTheShellNotAListing(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/assets", "/assets/"} {
		rec := get(t, p)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || body != "<!doctype html><title>Uruni</title>" {
			t.Errorf("GET %s = %d %q, want 200 and the shell", p, rec.Code, body)
		}
		if strings.Contains(body, "app.js") {
			t.Errorf("GET %s listed the directory: %q", p, body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache (it is the shell)", p, got)
		}
	}
}

// A chunk an older shell still names after a deploy answers 404, not the
// shell served as JavaScript - and never with the year-long header.
func TestAMissingHashedAssetIs404(t *testing.T) {
	t.Parallel()
	rec := get(t, "/assets/index-0ld0ld.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET a missing hashed asset = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("404 Cache-Control = %q, want no immutable", got)
	}
}
