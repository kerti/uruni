package auth

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/kerti/uruni/internal/db"
)

// newTestAuth returns an Auth over a private in-memory database carrying the
// real schema - the same recipe internal/ledger's fixture_test.go uses, for
// the same reason: a genuine *sql.Tx is what Register actually needs, so a
// fake Querier would not exercise the thing this package is here to prove.
//
// The raw *sql.DB comes back too, so a test can look underneath Register's
// own return value and assert what actually landed in the table - the "no
// row written" half of the one-shot refusal can't be proven from Register's
// error alone.
func newTestAuth(t *testing.T) (*Auth, *sql.DB) {
	t.Helper()
	sqlDB := newTestDB(t)
	return New(sqlDB), sqlDB
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(\":memory:\") = %v, want no error", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("Close() = %v, want no error", err)
		}
	})

	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return sqlDB
}
