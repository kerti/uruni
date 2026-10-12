package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/db"
)

// damagedDatabase writes an up-to-date database, then overwrites the head of
// the fund table's root page, so the file opens and its schema matches but
// PRAGMA quick_check fails - what a torn write or a bad disk leaves.
func damagedDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "uruni.db")

	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("db.Open() = %v", err)
	}
	if _, err := db.Up(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v", err)
	}
	var rootPage, pageSize int64
	if err := sqlDB.QueryRow(`SELECT rootpage FROM sqlite_schema WHERE type = 'table' AND name = 'fund'`).Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path is the test's own t.TempDir() database
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, (rootPage-1)*pageSize); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// #505: serve refuses a damaged database at boot, before the boot dump could
// copy the damage into the backup directory.
func TestServeRefusesToStartOnADamagedDatabase(t *testing.T) {
	path := damagedDatabase(t)
	backupDir := t.TempDir()
	t.Setenv("URUNI_DB", path)
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_UPLOADS_DIR", t.TempDir())
	t.Setenv("URUNI_BACKUP_DIR", backupDir)

	err := serve()
	if !errors.Is(err, db.ErrIntegrity) {
		t.Fatalf("serve() on a damaged database = %v, want db.ErrIntegrity", err)
	}
	for _, want := range []string{path, "restore a backup", "SELF-HOSTING.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("serve() error = %q, want it to mention %q", err, want)
		}
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("backup dir holds %d entries, want none - no dump of a damaged file", len(entries))
	}
}
