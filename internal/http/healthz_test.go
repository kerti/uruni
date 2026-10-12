package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func getHealthz(t *testing.T, h http.HandlerFunc) (int, health, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	raw := rec.Body.String()
	var got health
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&got); err != nil {
		t.Fatalf("decoding /healthz body %q: %v", raw, err)
	}
	return rec.Code, got, raw
}

// #505: a store that can't answer a query is not up, and the body never
// carries the error's detail (which can name the database path).
func TestHealthzAnswers503WhenTheDatabaseCannotAnswer(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ping := func(context.Context) error { return errors.New("unable to open /srv/uruni/uruni.db: disk I/O error") }

	code, got, raw := getHealthz(t, healthz(testBuild, nil, ping, nil, logger))

	if code != http.StatusServiceUnavailable || got.Status != "error" || got.Error != "database unavailable" {
		t.Fatalf("GET /healthz = %d %+v, want 503, status error, generic message", code, got)
	}
	if strings.Contains(raw, "/srv/uruni") || strings.Contains(raw, "disk I/O") {
		t.Errorf("GET /healthz body %q leaks the error detail", raw)
	}
	if !strings.Contains(logs.String(), "disk I/O error") {
		t.Errorf("log = %q, want the detail logged", logs.String())
	}
}

// The ping runs against the real store and a real deadline: a closed
// database answers 503, an open one ok.
func TestHealthzPingsTheRealStore(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	h := healthz(testBuild, nil, pingStore(store.New(sqlDB)), nil, testLogger())

	if code, got, _ := getHealthz(t, h); code != http.StatusOK || got.Status != "ok" {
		t.Fatalf("GET /healthz on an open store = %d %+v, want 200 ok", code, got)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if code, got, _ := getHealthz(t, h); code != http.StatusServiceUnavailable || got.Status != "error" {
		t.Fatalf("GET /healthz on a closed store = %d %+v, want 503 error", code, got)
	}
}

// A ping that never returns is cut off by the deadline, so the probe gets a
// body before Docker's own 3s timeout gives up on it.
func TestHealthzPingHasADeadline(t *testing.T) {
	t.Parallel()
	if healthzPingTimeout >= 3*time.Second {
		t.Fatalf("healthzPingTimeout = %v, want under the Dockerfile HEALTHCHECK's 3s", healthzPingTimeout)
	}
	var sawDeadline bool
	ping := func(ctx context.Context) error {
		_, sawDeadline = ctx.Deadline()
		return nil
	}
	if code, _, _ := getHealthz(t, healthz(testBuild, nil, ping, nil, testLogger())); code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", code)
	}
	if !sawDeadline {
		t.Error("ping ran without a deadline")
	}
}

// last_dump_at is informational: present when a dump exists, absent when
// none does, and never the reason status changes (ADR-019's liveness).
func TestHealthzReportsTheLastDumpWithoutChangingStatus(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	backupDir := t.TempDir()
	ping := pingStore(store.New(sqlDB))

	code, got, raw := getHealthz(t, healthz(testBuild, nil, ping, lastDumpIn(backupDir), testLogger()))
	if code != http.StatusOK || got.Status != "ok" || got.LastDumpAt != nil || strings.Contains(raw, "last_dump_at") {
		t.Fatalf("GET /healthz with no dumps = %d %s, want 200 ok and no last_dump_at", code, raw)
	}

	name, err := backup.WriteDump(context.Background(), sqlDB, t.TempDir(), backupDir, backup.KindDaily, time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("WriteDump: %v", err)
	}
	code, got, _ = getHealthz(t, healthz(testBuild, nil, ping, lastDumpIn(backupDir), testLogger()))
	if code != http.StatusOK || got.Status != "ok" || got.LastDumpAt == nil {
		t.Fatalf("GET /healthz after dump %s = %d %+v, want 200 ok with last_dump_at", name, code, got)
	}
	if !got.LastDumpAt.Equal(time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("last_dump_at = %v, want the dump's time", got.LastDumpAt)
	}

	// An old dump stays an old dump: status is still ok.
	failing := func(context.Context) error { return errors.New("down") }
	code, got, _ = getHealthz(t, healthz(testBuild, nil, failing, lastDumpIn(backupDir), testLogger()))
	if code != http.StatusServiceUnavailable || got.LastDumpAt == nil {
		t.Errorf("GET /healthz with a failing store = %d %+v, want 503 and last_dump_at still reported", code, got)
	}
}

// The full router wires the ping: New's /healthz answers 503 once its store
// is gone.
func TestRouterHealthzPingsItsStore(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	h := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil)
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /healthz with a closed store = %d, want 503", rec.Code)
	}
}
