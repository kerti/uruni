package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// #278: the scenario from the issue, against a real open connection. The
// file is replaced under it by rename - what `cp` to a temp name and `mv`,
// a restored volume, or `make db-reset` all amount to - and the check has
// to notice even though the connection itself keeps working.
func TestFileIdentityNoticesTheDatabaseFileReplacedUnderAnOpenConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "uruni.db")

	sqlDB, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() = %v, want no error", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	identity, err := IdentifyFile(path)
	if err != nil {
		t.Fatalf("IdentifyFile() = %v, want no error", err)
	}
	if err := identity.Check(); err != nil {
		t.Fatalf("Check() before any swap = %v, want nil", err)
	}

	replacement := filepath.Join(dir, "replacement.db")
	if err := os.WriteFile(replacement, nil, 0o600); err != nil {
		t.Fatalf("writing the replacement file: %v", err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("renaming the replacement over the database: %v", err)
	}

	// The silent part of the bug: the orphaned file still answers.
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() after the swap = %v, want the orphan to still answer", err)
	}
	if err := identity.Check(); !errors.Is(err, ErrDatabaseFileReplaced) {
		t.Errorf("Check() after the swap = %v, want ErrDatabaseFileReplaced", err)
	}
}

func TestFileIdentityNoticesTheDatabaseFileRemoved(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "uruni.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("writing the database file: %v", err)
	}

	identity, err := IdentifyFile(path)
	if err != nil {
		t.Fatalf("IdentifyFile() = %v, want no error", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the database file: %v", err)
	}
	if err := identity.Check(); !errors.Is(err, ErrDatabaseFileReplaced) {
		t.Errorf("Check() after removal = %v, want ErrDatabaseFileReplaced", err)
	}
}

// Writes go to the same inode, so an ordinary busy database never trips it.
func TestFileIdentityIgnoresWritesToTheSameFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "uruni.db")
	sqlDB, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() = %v, want no error", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	identity, err := IdentifyFile(path)
	if err != nil {
		t.Fatalf("IdentifyFile() = %v, want no error", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "CREATE TABLE t (x INTEGER) STRICT"); err != nil {
		t.Fatalf("writing to the database: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpointing: %v", err)
	}
	if err := identity.Check(); err != nil {
		t.Errorf("Check() after writes = %v, want nil", err)
	}
}

func TestIdentifyFileRefusesAMissingPath(t *testing.T) {
	t.Parallel()
	if _, err := IdentifyFile(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("IdentifyFile(missing path) = nil error, want one")
	}
}
