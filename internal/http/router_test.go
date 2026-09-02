package http

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

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
		// A real file, not a fallback to the shell: the Cache-Control test
		// below means to exercise the service worker's own branch (ADR-008).
		"sw.js": {Data: []byte("// service worker")},
	}
}

// testBuild stands in for the linker stamp; the point of the /healthz body is
// that whatever was stamped comes back out.
var testBuild = Build{Version: "v9.9.9-test", Commit: "abc1234"}

// testRouter builds a router over a real, migrated in-memory database — the
// ledger and store arguments are threaded through New for later M4 slices to
// use, so building it for real here (rather than passing nil) is what proves
// the constructor's new signature actually wires together. It carries a
// valid session throughout - see authedRouterFor's own comment for why.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	return authedRouterFor(t, testStoreDB(t))
}

// authedRouterFor builds the same router New builds, then makes one real
// POST /api/register call against it before returning, so every request the
// caller sends afterward already carries a valid session. #116 moved the
// entire surface but for four routes behind sessionRequired; this package's
// hundred-plus pre-existing business-logic tests exist to prove what a route
// does once a session is present, not to each separately reprove they can
// get past the gate, so the fixture absorbs that once here rather than at
// every call site.
//
// A test that means to exercise the gate itself - or the two routes that
// stay reachable without one - talks to testRouterAndDB, postRegister,
// postLogin or postLogout directly, never this.
func authedRouterFor(t *testing.T, sqlDB *sql.DB) http.Handler {
	t.Helper()
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), testLogger(), auth.New(sqlDB), "")

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

// withSessionCookie attaches one fixed, already-established session cookie
// to any request that doesn't already carry one of its own, then delegates.
// It exists so the hundred-plus tests already constructing their own
// httptest.NewRequest calls throughout this package did not each need to
// learn how to carry a cookie forward - they ask testRouter for a handler
// exactly as before, and the handler now happens to answer every request as
// the one treasurer authedRouterFor registered.
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
	New(testAssets(), untagged, ledger.New(sqlDB), store.New(sqlDB), testLogger(), auth.New(sqlDB), "").
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

// The stale-app failure ADR-008's prompt-to-update strategy would otherwise
// still lose to: index.html and sw.js keep their names across deploys, so a
// browser that caches either serves an old app from a freshly updated server.
// Every other asset is content-hashed and deliberately left alone here.
func TestShellAndServiceWorkerAreNotBrowserCached(t *testing.T) {
	for _, path := range []string{"/", "/index.html", "/sw.js", "/catat"} {
		rec := get(t, path)
		if got, want := rec.Header().Get("Cache-Control"), "no-cache"; got != want {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, got, want)
		}
	}

	if got := get(t, "/assets/app.js").Header().Get("Cache-Control"); got != "" {
		t.Errorf("GET /assets/app.js Cache-Control = %q, want it unset - hashed assets are cacheable", got)
	}
}

// A manifest served as text/plain is one browsers may decline to install
// from, and Go's mime table has no .webmanifest entry of its own.
func TestManifestIsServedAsAWebManifest(t *testing.T) {
	rec := get(t, "/manifest.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/manifest+json"; !strings.HasPrefix(got, want) {
		t.Errorf("Content-Type = %q, want it to start with %q", got, want)
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
