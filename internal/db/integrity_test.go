package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestQuickCheckPassesAHealthyDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB, err := Open(ctx, filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := Up(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}

	if err := QuickCheck(ctx, sqlDB); err != nil {
		t.Fatalf("QuickCheck on a freshly migrated database = %v, want nil", err)
	}
}

// A damaged b-tree page is what a torn write or a bad disk leaves behind:
// the file still opens, the header still reads, and only a structural check
// notices. Overwriting the start of a table's root page with zeroes stands in
// for it deterministically.
func TestQuickCheckRefusesADamagedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "u.db")
	sqlDB, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Up(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	var rootPage, pageSize int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT rootpage FROM sqlite_schema WHERE type = 'table' AND name = 'fund'").Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	// Fold the WAL back into the main file so the damage below is the only
	// copy of that page SQLite can read.
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path is the test's own t.TempDir() database
	if err != nil {
		t.Fatal(err)
	}
	// 0x0D is a table leaf page's type byte; 0xFF is no page type at all.
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, (rootPage-1)*pageSize); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	damaged, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = damaged.Close() })

	if err := QuickCheck(ctx, damaged); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("QuickCheck on a damaged database = %v, want ErrIntegrity", err)
	}
}

func TestQuickCheckEndsWithItsContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB, err := Open(ctx, filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := QuickCheck(cancelled, sqlDB); !errors.Is(err, context.Canceled) {
		t.Fatalf("QuickCheck with a cancelled context = %v, want context.Canceled", err)
	}
}
