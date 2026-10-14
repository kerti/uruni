package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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

	if err := shutdown(&http.Server{ReadHeaderTimeout: time.Second}, done, time.Second, 5*time.Second, logger); err != nil {
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
	err := shutdown(&http.Server{ReadHeaderTimeout: time.Second}, make(chan struct{}), time.Second, 100*time.Millisecond, logger)
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

// TestShutdownStillJoinsTheSchedulerWhenTheHTTPDrainTimesOut: a request that
// will not finish must not skip the scheduler wait, and its error is the one
// returned.
func TestShutdownStillJoinsTheSchedulerWhenTheHTTPDrainTimesOut(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	started := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}))
	t.Cleanup(func() { close(release); ts.Close() })
	go func() { _, _ = http.Get(ts.URL) }() //nolint:gosec,noctx,bodyclose // test client; the handler never answers
	<-started

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	done := make(chan struct{})
	var joined atomic.Bool
	go func() {
		time.Sleep(150 * time.Millisecond)
		joined.Store(true)
		close(done)
	}()

	err := shutdown(ts.Config, done, 50*time.Millisecond, 5*time.Second, logger)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("shutdown() = %v, want context.DeadlineExceeded", err)
	}
	if !joined.Load() {
		t.Error("shutdown returned before the scheduler finished")
	}
	if !strings.Contains(logs.String(), "http server did not drain in time") {
		t.Errorf("log = %q, want the drain timeout logged", logs.String())
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
	if got := httpShutdownTimeout + schedulerShutdownTimeout; got > gracePeriod-5*time.Second {
		t.Errorf("shutdown budget %v leaves under 5s of docker's %v stop_grace_period", got, gracePeriod)
	}
}
