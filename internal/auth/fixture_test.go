package auth

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/kerti/uruni/internal/db"
)

// newTestAuth returns an Auth over a private in-memory database with the real
// schema (the recipe of internal/ledger's fixture_test.go): a genuine *sql.Tx
// is what Register needs, so a fake Querier would not exercise this package's
// point.
//
// The raw *sql.DB comes back too, so a test can look under Register's return
// and assert what landed in the table; "no row written" cannot be proven from
// Register's error alone.
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
