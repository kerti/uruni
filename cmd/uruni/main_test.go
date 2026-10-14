package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/config"
)

func TestRunRejectsAnEmptyCommand(t *testing.T) {
	if err := run(nil); !errors.Is(err, ErrNoCommand) {
		t.Fatalf("run(nil) = %v, want ErrNoCommand", err)
	}
}

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	err := run([]string{"migrate-everything"})
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("run([migrate-everything]) = %v, want ErrUnknownCommand", err)
	}
	// The subcommands are a contract the Makefile and the Dockerfile are
	// written against (ADR-019); a typo should say what the alternatives are.
	for _, cmd := range []string{"serve", "migrate", "create-user", "version", "healthcheck", "seed-e2e"} {
		if !strings.Contains(err.Error(), cmd) {
			t.Errorf("run([migrate-everything]) = %q, want it to mention %q", err, cmd)
		}
	}
}

// TestRunDispatchesSeedE2E: the subcommand table is a contract the Makefile is
// written against (ADR-019), so the wiring (the name `make e2e-reset` invokes
// reaching seedE2E) is worth a test beyond seedE2E's behaviour.
func TestRunDispatchesSeedE2E(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni-e2e.db"))

	if err := run([]string{"seed-e2e"}); err != nil {
		t.Fatalf("run([seed-e2e]) = %v, want nil", err)
	}
}

func TestPrintVersion(t *testing.T) {
	var out bytes.Buffer
	if err := printVersion(&out); err != nil {
		t.Fatalf("printVersion() = %v, want nil", err)
	}

	got := out.String()
	// An unstamped build is a dev build and says so - the point of the line is
	// that a *tagged* image says something else (ADR-018).
	if !strings.HasPrefix(got, "uruni "+version+" ") {
		t.Errorf("printVersion() = %q, want it to lead with the version %q", got, version)
	}
	if !strings.Contains(got, "commit ") {
		t.Errorf("printVersion() = %q, want it to report a commit", got)
	}
}

func TestBuildCommitPrefersTheLinkerStamp(t *testing.T) {
	original := commit
	t.Cleanup(func() { commit = original })

	commit = "0123456789abcdef0123456789abcdef01234567"
	if got := buildCommit(); got != "0123456" {
		t.Errorf("buildCommit() = %q, want the abbreviated stamp %q", got, "0123456")
	}

	// No stamp: `go test` binaries carry no VCS record either, so this is the
	// honest fallback rather than an empty string.
	commit = ""
	if got := buildCommit(); got == "" {
		t.Error("buildCommit() = \"\", want a non-empty commit")
	}
}

func TestProbeHealthAcceptsAHealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := probeHealth(srv.URL+"/healthz", time.Second); err != nil {
		t.Fatalf("probeHealth() against a healthy server = %v, want nil", err)
	}
}

func TestProbeHealthRejectsAnUnhealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := probeHealth(srv.URL+"/healthz", time.Second); err == nil {
		t.Fatal("probeHealth() against a 503 = nil, want an error")
	}
}

func TestProbeHealthRejectsAServerThatIsNotThere(t *testing.T) {
	// Bound then immediately closed, so the port is real and refusing - the
	// container's "the binary died but the container is up" case.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL + "/healthz"
	srv.Close()

	if err := probeHealth(url, time.Second); err == nil {
		t.Fatal("probeHealth() against a closed port = nil, want an error")
	}
}

func TestNewLoggerHonoursFormatAndLevel(t *testing.T) {
	var out bytes.Buffer

	logger := newLogger(config.Config{LogLevel: slog.LevelInfo, LogFormat: config.LogFormatJSON}, &out)
	logger.Info("listening", "port", 8080)
	if got := out.String(); !strings.HasPrefix(got, "{") {
		t.Errorf("json format logged %q, want JSON", got)
	}

	out.Reset()
	logger = newLogger(config.Config{LogLevel: slog.LevelInfo, LogFormat: config.LogFormatText}, &out)
	logger.Debug("noisy")
	if got := out.String(); got != "" {
		t.Errorf("debug at info level logged %q, want nothing", got)
	}
	logger.Info("listening", "port", 8080)
	if got := out.String(); !strings.Contains(got, "port=8080") {
		t.Errorf("text format logged %q, want a key=value line", got)
	}
}

// TestShutdownWaitsForTheSchedulerBeforeReturning: serve closes the database
// right after shutdown returns, so a tick still writing a dump when it returns
// would write against a closed database. The "scheduler" here finishes some
// time after shutdown starts and records whether the database was already
// closed by then.
func TestShutdownWaitsForTheSchedulerBeforeReturning(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	var dbClosed atomic.Bool
	finishedBeforeClose := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		finishedBeforeClose <- !dbClosed.Load()
		close(done)
	}()

	if err := shutdown(&http.Server{ReadHeaderTimeout: time.Second}, newRequestTracker(), newSchedulerJoin(done), testBounds(time.Second, time.Second, 5*time.Second), logger); err != nil {
		t.Fatalf("shutdown() = %v, want nil", err)
	}
	dbClosed.Store(true) // what the deferred sqlDB.Close stands for

	if !<-finishedBeforeClose {
		t.Error("the database was closed while the scheduler was still running")
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("clean shutdown logged an error: %s", logs.String())
	}
}

// TestShutdownGivesUpOnAStuckSchedulerAndSaysSo: the wait is bounded so a hung
// tick cannot hold the process past docker's stop_grace_period and get it
// SIGKILLed mid-close; the cost is one error line.
func TestShutdownGivesUpOnAStuckSchedulerAndSaysSo(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	start := time.Now()
	err := shutdown(&http.Server{ReadHeaderTimeout: time.Second}, newRequestTracker(), newSchedulerJoin(make(chan struct{})), testBounds(time.Second, time.Second, 100*time.Millisecond), logger)
	if err != nil {
		t.Errorf("shutdown() = %v, want nil (the HTTP side drained)", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("shutdown took %v, want about the 100ms scheduler bound", elapsed)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "backup scheduler still running") {
		t.Errorf("log = %q, want an error naming the scheduler", logs.String())
	}
}

func testBounds(drain, handlers, scheduler time.Duration) shutdownBounds {
	return shutdownBounds{drain: drain, handlers: handlers, scheduler: scheduler}
}

// trackedServer starts a test server wired the way serve wires its own: the
// request tracker attached to the *http.Server before it listens.
func trackedServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *requestTracker) {
	t.Helper()
	ts := httptest.NewUnstartedServer(h)
	requests := newRequestTracker()
	requests.attach(ts.Config)
	ts.Start()
	t.Cleanup(ts.Close)
	return ts, requests
}

// getAsync fires a request in the background and returns once its handler has
// started; the channel carries the client's error, nil if it got an answer.
func getAsync(t *testing.T, url string, started <-chan struct{}) <-chan error {
	t.Helper()
	errc := make(chan error, 1)
	go func() {
		resp, err := http.Get(url) //nolint:gosec,noctx // test client
		if err == nil {
			_ = resp.Body.Close()
		}
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}
	return errc
}

// TestShutdownEndsARequestStillRunningAtTheDrainDeadline: the heart of #586. A
// handler that outlasts the drain (a restore, in life) must be ended and must
// have returned before shutdown does, because serve closes the database right
// after. The handler stands for the restore's transaction: it stops on its
// request context and records whether the "database" was already closed.
func TestShutdownEndsARequestStillRunningAtTheDrainDeadline(t *testing.T) {
	t.Parallel()
	var dbClosed atomic.Bool
	started := make(chan struct{})
	returnedBeforeClose := make(chan bool, 1)
	ts, requests := trackedServer(t, func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second): // only if the forced end is broken; keeps that failure fast
		}
		time.Sleep(50 * time.Millisecond) // the rollback and the error response
		returnedBeforeClose <- !dbClosed.Load()
	})
	client := getAsync(t, ts.URL, started)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	done := make(chan struct{})
	close(done)

	start := time.Now()
	err := shutdown(ts.Config, requests, newSchedulerJoin(done), testBounds(50*time.Millisecond, time.Second, time.Second), logger)
	dbClosed.Store(true) // what the deferred sqlDB.Close stands for

	if err != nil {
		t.Errorf("shutdown() = %v, want nil (the forced end was clean)", err)
	}
	select {
	case ok := <-returnedBeforeClose:
		if !ok {
			t.Error("the handler was still running when the database closed")
		}
	default:
		t.Error("the handler was still running when the database closed")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("shutdown took %v, want the drain bound plus the handler's own wind-down", elapsed)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "http server did not drain in time") {
		t.Errorf("log = %q, want the drain timeout logged as a warning", logs.String())
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a clean forced end logged an error: %s", logs.String())
	}
	select {
	case err := <-client:
		if err == nil {
			t.Error("the client got a response, want its connection closed")
		}
	case <-time.After(5 * time.Second):
		t.Error("the client's connection was never closed")
	}
}

// TestShutdownGivesUpOnAHandlerThatIgnoresItsContext: the wait for handlers is
// bounded for the same reason the scheduler's is, and the exit is non-zero
// because the database is about to close under a request.
func TestShutdownGivesUpOnAHandlerThatIgnoresItsContext(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	started := make(chan struct{})
	ts, requests := trackedServer(t, func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})
	t.Cleanup(func() { close(release) })
	getAsync(t, ts.URL, started)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	done := make(chan struct{})
	var joined atomic.Bool
	go func() {
		time.Sleep(150 * time.Millisecond)
		joined.Store(true)
		close(done)
	}()

	start := time.Now()
	err := shutdown(ts.Config, requests, newSchedulerJoin(done), testBounds(50*time.Millisecond, 100*time.Millisecond, 5*time.Second), logger)
	if !errors.Is(err, errHandlersOutlivedShutdown) {
		t.Errorf("shutdown() = %v, want errHandlersOutlivedShutdown", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("shutdown took %v, want about the 50ms drain plus the 100ms handler bound", elapsed)
	}
	if !joined.Load() {
		t.Error("shutdown returned before the scheduler finished: a stuck handler must not skip the join")
	}
	if !strings.Contains(logs.String(), "requests still running at shutdown") {
		t.Errorf("log = %q, want an error naming the requests", logs.String())
	}
}

// TestShutdownWaitsForARequestThatFinishesInsideTheDrain: the ordinary case. No
// forced end, no warning, and shutdown does not return until the handler has.
func TestShutdownWaitsForARequestThatFinishesInsideTheDrain(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	var finished atomic.Bool
	ts, requests := trackedServer(t, func(http.ResponseWriter, *http.Request) {
		close(started)
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
	})
	client := getAsync(t, ts.URL, started)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	done := make(chan struct{})
	close(done)

	if err := shutdown(ts.Config, requests, newSchedulerJoin(done), testBounds(5*time.Second, time.Second, time.Second), logger); err != nil {
		t.Errorf("shutdown() = %v, want nil", err)
	}
	if !finished.Load() {
		t.Error("shutdown returned before the request finished")
	}
	if err := <-client; err != nil {
		t.Errorf("the request failed: %v", err)
	}
	if strings.Contains(logs.String(), "level=WARN") || strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a clean drain logged: %s", logs.String())
	}
}

// TestRequestTrackerRefusesWorkOnceWaitHasBegun: the gate that keeps wait exact.
func TestRequestTrackerRefusesWorkOnceWaitHasBegun(t *testing.T) {
	t.Parallel()
	ran := false
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })}
	requests := newRequestTracker()
	requests.attach(srv)

	<-requests.wait()
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if ran || rec.Code != http.StatusServiceUnavailable {
		t.Errorf("after wait: handler ran = %v, status = %d, want refused with 503", ran, rec.Code)
	}
}

// TestNewServerTracksItsRequests: serve builds its server through newServer, so
// this is the wiring test. A server it returns must hold wait open while a
// handler runs, and must cancel that handler's context on end() with the
// connection still open. The handler is blocked on an unread streamed request
// body, the real shape of a restore upload: nothing reads the connection, so
// nothing but the BaseContext can cancel the context. (srv.Close would also
// cancel it, by dropping the connection; this test must not depend on that.)
func TestNewServerTracksItsRequests(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	cancelled := make(chan bool, 1)
	srv, requests := newServer("", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			cancelled <- true
		case <-time.After(3 * time.Second):
			cancelled <- false
		}
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	body, bodyWriter := io.Pipe()
	t.Cleanup(func() { _ = bodyWriter.Close() })
	req, err := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String(), body) //nolint:noctx // test client
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil { //nolint:gosec,bodyclose // test client
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never started")
	}

	idle := requests.wait()
	select {
	case <-idle:
		t.Fatal("wait() reported idle while a handler was running")
	case <-time.After(50 * time.Millisecond):
	}

	requests.end()
	if !<-cancelled {
		t.Error("end() did not cancel the request context")
	}
	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Error("wait() never reported idle after the handler returned")
	}
}

// TestSchedulerJoinWaitsOnlyOnce: shutdown and serve's deferred cleanup both
// join the scheduler. The second call must not wait again, or a scheduler that
// outlasted the first bound would be waited on for another full bound.
func TestSchedulerJoinWaitsOnlyOnce(t *testing.T) {
	t.Parallel()
	j := newSchedulerJoin(make(chan struct{})) // never closes
	start := time.Now()
	if j.wait(80 * time.Millisecond) {
		t.Fatal("wait() = true for a scheduler that never finished")
	}
	first := time.Since(start)
	start = time.Now()
	if j.wait(5 * time.Second) {
		t.Error("second wait() = true, want the first call's outcome")
	}
	if second := time.Since(start); second > first/2 {
		t.Errorf("second wait took %v after a first of %v, want it to return at once", second, first)
	}
}

// The shutdown budget must fit docker-compose's stop_grace_period (30s) with
// room for the database close and lock release.
func TestShutdownBudgetFitsTheComposeGracePeriod(t *testing.T) {
	t.Parallel()
	compose, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*stop_grace_period:\s*(\S+)\s*$`).FindSubmatch(compose)
	if m == nil {
		t.Fatal("docker-compose.yml sets no stop_grace_period for the app")
	}
	gracePeriod, err := time.ParseDuration(string(m[1]))
	if err != nil {
		t.Fatalf("stop_grace_period %q: %v", m[1], err)
	}
	if got := httpShutdownTimeout + handlerShutdownTimeout + schedulerShutdownTimeout; got > gracePeriod-5*time.Second {
		t.Errorf("shutdown budget %v leaves under 5s of docker's %v stop_grace_period", got, gracePeriod)
	}
}
