package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Uruni</title>")},
		"assets/app.js":        {Data: []byte("console.log('uruni')")},
		"favicon.svg":          {Data: []byte("<svg/>")},
		".gitkeep":             {Data: []byte("")},
		"assets/app.css":       {Data: []byte("body{}")},
		"manifest.webmanifest": {Data: []byte("{}")},
		// A real file, not a fallback to the shell: the Cache-Control test below
		// exercises the service worker's branch (ADR-008).
		"sw.js": {Data: []byte("// service worker")},
	}
}

// testBuild stands in for the linker stamp; /healthz must return whatever was
// stamped.
var testBuild = Build{Version: "v9.9.9-test", Commit: "abc1234"}

// testRouter builds a router over a real, migrated in-memory database. The
// ledger and store are threaded through New for later slices, so building it
// for real (not nil) proves the constructor's signature wires together. It
// carries a valid session throughout; see authedRouterFor.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	return authedRouterFor(t, testStoreDB(t))
}

// authedRouterFor builds the router New builds, then makes one real POST
// /api/register against it, so every later request already carries a valid
// session. #116 moved all but four routes behind sessionRequired, and this
// package's hundred-plus business-logic tests prove what a route does once a
// session is present, so the fixture absorbs the gate once here.
//
// A test of the gate itself, or of the two routes reachable without a
// session, uses testRouterAndDB, postRegister, postLogin or postLogout, never
// this.
func authedRouterFor(t *testing.T, sqlDB *sql.DB) http.Handler {
	t.Helper()
	return authedRouterAt(t, sqlDB, time.Now)
}

// authedRouterAt is authedRouterFor with the router's clock pinned (#379), so
// a test can stand on either side of a Jakarta month boundary.
func authedRouterAt(t *testing.T, sqlDB *sql.DB, now func() time.Time) http.Handler {
	t.Helper()
	r := New(testAssets(), testBuild, ledger.New(sqlDB, ledger.WithClock(now)), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("fixture POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	token := sessionCookie(reg)
	if token == "" {
		t.Fatal("fixture register set no session cookie")
	}
	return withSessionCookie{Handler: r, token: token}
}

// withSessionCookie attaches one fixed, established session cookie to any
// request without its own, then delegates, so the hundred-plus tests building
// their own httptest.NewRequest need not learn to carry a cookie: they ask
// testRouter for a handler and it answers as the one registered treasurer.
type withSessionCookie struct {
	http.Handler
	token string
}

func (w withSessionCookie) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie("session"); err != nil {
		r.AddCookie(&http.Cookie{Name: "session", Value: w.token}) //nolint:gosec // test fixture cookie, not a credential
	}
	w.Handler.ServeHTTP(rw, r)
}

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsUnauthenticatedAndOK(t *testing.T) {
	t.Parallel()
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

// The stamp must be the one the binary was built with, not a router constant:
// that is the point of reading it off a deployment. An untagged build needs
// the commit: version is only `dev`, so the SHA is the sole thing naming what
// runs.
func TestHealthzReportsTheBuildItWasStampedWith(t *testing.T) {
	t.Parallel()
	untagged := Build{Version: "dev", Commit: "deadbee"}
	sqlDB := testStoreDB(t)
	rec := httptest.NewRecorder()
	New(testAssets(), untagged, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil).
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

// #278: a database path no longer naming the file this process opened turns
// /healthz red, so the container HEALTHCHECK raises the alarm.
func TestHealthzFailsWhenTheDatabaseFileCheckFails(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	replaced := func() error { return errors.New("the database file was replaced") }
	rec := httptest.NewRecorder()
	New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, replaced, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /healthz = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var got health
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding /healthz body: %v", err)
	}
	// The detail stays in the log (#505): the body is generic.
	if got.Status != "error" || got.Error != "database unavailable" {
		t.Errorf("GET /healthz = %+v, want status error with the generic message", got)
	}
	if got.Version != testBuild.Version {
		t.Errorf("GET /healthz version = %q, want %q - the build stays readable when unhealthy", got.Version, testBuild.Version)
	}
}

func TestRootServesTheEmbeddedSPA(t *testing.T) {
	t.Parallel()
	rec := get(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "<!doctype html><title>Uruni</title>" {
		t.Errorf("GET / served %q, want index.html", got)
	}
}

func TestAssetsAreServedAsThemselves(t *testing.T) {
	t.Parallel()
	rec := get(t, "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "console.log('uruni')" {
		t.Errorf("served %q, want the asset itself", got)
	}
}

func TestClientRoutesFallBackToIndex(t *testing.T) {
	t.Parallel()
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

// The stale-app failure ADR-008's prompt-to-update would still lose to:
// index.html and sw.js keep their names across deploys, so a browser caching
// either serves an old app from an updated server. Content-hashed assets are
// the opposite, cached for a year (#506).
func TestShellAndServiceWorkerAreNotBrowserCached(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/", "/index.html", "/sw.js", "/catat"} {
		rec := get(t, path)
		if got, want := rec.Header().Get("Cache-Control"), "no-cache"; got != want {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, got, want)
		}
	}

	// Hashed assets get the opposite, a year-long immutable header (#506,
	// TestHashedAssetsCacheAsImmutable).
	if got := get(t, "/assets/app.js").Header().Get("Cache-Control"); got == "no-cache" {
		t.Errorf("GET /assets/app.js Cache-Control = %q, want hashed assets cacheable", got)
	}
}

// A manifest served as text/plain may be declined by browsers, and Go's mime
// table has no .webmanifest entry.
func TestManifestIsServedAsAWebManifest(t *testing.T) {
	t.Parallel()
	rec := get(t, "/manifest.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/manifest+json"; !strings.HasPrefix(got, want) {
		t.Errorf("Content-Type = %q, want it to start with %q", got, want)
	}
}

func TestUnknownAPIPathsAnswerWithTheErrorEnvelope(t *testing.T) {
	t.Parallel()
	// A mistyped path is when a client copes worst with a second error shape, so
	// /api answers in the usual envelope, not chi's plain text.
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
	t.Parallel()
	for _, path := range []string{"/api", "/api/transactions", "/report", "/report/abc123"} {
		rec := get(t, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d (must not serve the SPA shell)", path, rec.Code, http.StatusNotFound)
		}
	}
}
