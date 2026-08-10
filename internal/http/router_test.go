package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>Uruni</title>")},
		"assets/app.js":   {Data: []byte("console.log('uruni')")},
		"favicon.svg":     {Data: []byte("<svg/>")},
		".gitkeep":        {Data: []byte("")},
		"assets/app.css":  {Data: []byte("body{}")},
		"manifest.webman": {Data: []byte("{}")},
	}
}

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	New(testAssets()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsUnauthenticatedAndOK(t *testing.T) {
	rec := get(t, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

func TestRootServesTheEmbeddedSPA(t *testing.T) {
	rec := get(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "<!doctype html><title>Uruni</title>" {
		t.Errorf("GET / served %q, want index.html", got)
	}
}

func TestAssetsAreServedAsThemselves(t *testing.T) {
	rec := get(t, "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "console.log('uruni')" {
		t.Errorf("served %q, want the asset itself", got)
	}
}

func TestClientRoutesFallBackToIndex(t *testing.T) {
	for _, path := range []string{"/catat", "/rekonsiliasi/2026-08"} {
		rec := get(t, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != "<!doctype html><title>Uruni</title>" {
			t.Errorf("GET %s served %q, want index.html", path, got)
		}
	}
}

func TestServerNamespacesDoNotFallBackToTheSPA(t *testing.T) {
	for _, path := range []string{"/api", "/api/transactions", "/report", "/report/abc123"} {
		rec := get(t, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d (must not serve the SPA shell)", path, rec.Code, http.StatusNotFound)
		}
	}
}
