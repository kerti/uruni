package main

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	for _, cmd := range []string{"serve", "migrate", "version", "healthcheck", "seed-e2e"} {
		if !strings.Contains(err.Error(), cmd) {
			t.Errorf("run([migrate-everything]) = %q, want it to mention %q", err, cmd)
		}
	}
}

// TestRunDispatchesSeedE2E: the subcommand table is a contract the Makefile is
// written against (ADR-019), so the wiring itself — the name `make e2e-reset`
// invokes reaching seedE2E — is worth a test, not just seedE2E's own behaviour.
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
	// An unstamped build is a dev build and says so — the point of the line is
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
	// Bound then immediately closed, so the port is real and refusing — the
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
