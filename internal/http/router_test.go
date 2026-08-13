package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
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

// testBuild stands in for the linker stamp; the point of the /healthz body is
// that whatever was stamped comes back out.
var testBuild = Build{Version: "v9.9.9-test", Commit: "abc1234"}

// testRouter builds a router over a real, migrated in-memory database — the
// ledger and store arguments are threaded through New for later M4 slices to
// use, so building it for real here (rather than passing nil) is what proves
// the constructor's new signature actually wires together.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	sqlDB := testStoreDB(t)
	return New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), testLogger())
}

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsUnauthenticatedAndOK(t *testing.T) {
	rec := get(t, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var got health
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding /healthz body: %v", err)
	}
	want := health{Status: "ok", Version: testBuild.Version, Commit: testBuild.Commit}
	if got != want {
		t.Errorf("GET /healthz = %+v, want %+v", got, want)
	}
}

// The stamp has to be the one the binary was built with, not a constant baked
// into the router — that is the whole point of reading it off a deployment. An
// untagged build is the case that needs the commit: version is only ever `dev`,
// so the SHA is the sole thing naming what runs.
func TestHealthzReportsTheBuildItWasStampedWith(t *testing.T) {
	untagged := Build{Version: "dev", Commit: "deadbee"}
	sqlDB := testStoreDB(t)
	rec := httptest.NewRecorder()
	New(testAssets(), untagged, ledger.New(sqlDB), store.New(sqlDB), testLogger()).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	var got health
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding /healthz body: %v", err)
	}
	if got.Version != untagged.Version || got.Commit != untagged.Commit {
		t.Errorf("GET /healthz = %+v, want version %q and commit %q",
			got, untagged.Version, untagged.Commit)
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

func TestUnknownAPIPathsAnswerWithTheErrorEnvelope(t *testing.T) {
	// A mistyped path is when a client is least able to cope with a second
	// error shape, so /api answers in the envelope every other API failure
	// uses rather than chi's plain-text default.
	rec := get(t, "/api/no-such-route")

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/no-such-route = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the body = %v, want the JSON error envelope (body: %q)", err, rec.Body.String())
	}
	if body.Error.Code != "not_found" {
		t.Errorf("error code = %q, want %q", body.Error.Code, "not_found")
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
