package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/db"
)

// ADR-045: serve under URUNI_DEMO. None of these run in parallel: bootEnv sets
// process environment, and the uptime test swaps a package variable.

// getBody is one GET against the booted server.
func getBody(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx,gosec // local test request against a server this test started
	if err != nil {
		t.Fatalf("GET %s = %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", url, err)
	}
	return resp, string(b)
}

func TestServeDemoSeedsAnEmptyDatabaseAndShowsTheLogin(t *testing.T) {
	base := bootEnv(t, t.TempDir())
	t.Setenv("URUNI_DEMO", "true")
	done, cancel := startServe(t)
	waitHealthy(t, base, done)

	resp, body := getBody(t, base+"/api/session")
	for _, want := range []string{`"demo_email":"demo@uruni.test"`, `"demo_password":"coba-uruni"`, `"has_account":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /api/session = %s, want it to carry %s", body, want)
		}
	}
	if got := resp.Header.Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag = %q, want noindex, nofollow", got)
	}

	// The fixed slug the README links to.
	if resp, _ := getBody(t, base+"/report/CsShiftSoreDemoUruni2026"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /report/<the demo slug> = %d, want 200", resp.StatusCode)
	}
	cancel()
	<-done
}

func TestServeWithoutDemoShowsNothingOfIt(t *testing.T) {
	base := bootEnv(t, t.TempDir())
	t.Setenv("URUNI_DEMO", "")
	done, cancel := startServe(t)
	waitHealthy(t, base, done)

	resp, body := getBody(t, base+"/api/session")
	if strings.TrimSpace(body) != `{"authenticated":false,"has_account":false}` {
		t.Errorf("GET /api/session = %s, want only authenticated and has_account and no account seeded", body)
	}
	if got := resp.Header.Get("X-Robots-Tag"); got != "" {
		t.Errorf("X-Robots-Tag = %q, want none outside the demo", got)
	}
	cancel()
	<-done
}

// The setting cannot be turned on over a real fund by accident.
func TestServeDemoRefusesAForeignLogin(t *testing.T) {
	dir := t.TempDir()
	bootEnv(t, dir)
	t.Setenv("URUNI_DEMO", "true")

	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(dir, "uruni.db"))
	if err != nil {
		t.Fatalf("db.Open() = %v", err)
	}
	if _, err := db.Up(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v", err)
	}
	if _, err := auth.New(sqlDB).Register(ctx, "bendahara@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	_ = sqlDB.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = serveContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "URUNI_DEMO") {
		t.Fatalf("serve over a real login = %v, want an error naming URUNI_DEMO", err)
	}
}

// A second boot over the demo's own database is a normal boot, not a reseed and
// not a refusal.
func TestServeDemoBootsOverItsOwnDatabase(t *testing.T) {
	dir := t.TempDir()
	base := bootEnv(t, dir)
	t.Setenv("URUNI_DEMO", "true")

	done, cancel := startServe(t)
	waitHealthy(t, base, done)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("first serve = %v, want nil", err)
	}

	done, cancel = startServe(t)
	waitHealthy(t, base, done)
	if _, body := getBody(t, base+"/api/session"); !strings.Contains(body, `"demo_email"`) {
		t.Errorf("GET /api/session after the second boot = %s, want the demo login", body)
	}
	cancel()
	<-done
}

// ADR-045 decision 3: under URUNI_DEMO, serve ends itself after the uptime limit
// by the graceful path and returns nil, so the process exits 0.
func TestServeDemoExitsCleanlyAfterTheUptimeLimit(t *testing.T) {
	bootEnv(t, t.TempDir())
	t.Setenv("URUNI_DEMO", "true")
	old := demoMaxUptime
	demoMaxUptime = 300 * time.Millisecond
	t.Cleanup(func() { demoMaxUptime = old })

	// No cancel and no signal: only the limit can end this. The deadline is the
	// failure mode, a serve that never stops.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	if err := serveContext(ctx); err != nil {
		t.Fatalf("serve after the uptime limit = %v, want nil (exit 0)", err)
	}
	if ctx.Err() != nil {
		t.Fatal("serve only returned when the test's deadline cancelled it, not at the uptime limit")
	}
	if took := time.Since(start); took < demoMaxUptime {
		t.Errorf("serve returned after %v, before the %v limit", took, demoMaxUptime)
	}
}
