package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenAppliesEveryPragmaToEveryConnection(t *testing.T) {
	sqlDB := openTestDB(t)

	// Queried back rather than trusted from the DSN: SQLite silently ignores a
	// pragma it doesn't recognise, so a typo in the list would otherwise leave
	// foreign keys off (or the journal in rollback mode) with nothing to show it.
	want := map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"foreign_keys": "1",
		"synchronous":  "1", // NORMAL
	}

	assert := func(t *testing.T, when string) {
		t.Helper()
		for pragma, expected := range want {
			var got string
			if err := sqlDB.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
				t.Fatalf("%s: PRAGMA %s = %v, want no error", when, pragma, err)
			}
			if got != expected {
				t.Errorf("%s: PRAGMA %s = %q, want %q", when, pragma, got, expected)
			}
		}
	}

	assert(t, "on the first connection")

	// "Every connection", not "the first one": retire it and make the pool open a
	// fresh one, which is what happens in a long-lived server whenever a
	// connection is replaced.
	sqlDB.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(time.Millisecond)
	assert(t, "on a reopened connection")
}

func TestOpenSerializesOnOneConnection(t *testing.T) {
	sqlDB := openTestDB(t)

	// ADR-004: one connection is what makes SQLITE_BUSY structurally impossible,
	// so the ledger never needs retry logic. Raising this is a decision, not a
	// tuning knob — it needs a superseding ADR.
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestOpenFailsOnAnUnwritablePath(t *testing.T) {
	// sql.Open is lazy, so this only fails at boot because Open pings. A database
	// path the operator mistyped should be a startup error, not a 500 on the
	// treasurer's first tap.
	missingDir := filepath.Join(t.TempDir(), "no-such-dir", "uruni.db")

	sqlDB, err := Open(context.Background(), missingDir)
	if err == nil {
		_ = sqlDB.Close()
		t.Fatal("Open() on a path inside a missing directory = nil, want an error")
	}
	if !strings.Contains(err.Error(), missingDir) {
		t.Errorf("Open() error = %q, want it to name the path %q", err, missingDir)
	}
}

// openTestDB opens a throwaway database on a real file — not :memory: — because
// the pragmas under test (WAL especially) behave differently for an in-memory
// database, which would make the test prove less than it appears to.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB, err := Open(context.Background(), filepath.Join(t.TempDir(), "uruni.db"))
	if err != nil {
		t.Fatalf("Open() = %v, want no error", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("Close() = %v, want no error", err)
		}
	})
	return sqlDB
}
