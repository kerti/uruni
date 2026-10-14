package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/db"
)

// staleDatabase writes a database goose believes is current but whose schema
// differs from what this binary creates by one column - ADR-025's failure.
func staleDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "uruni.db")

	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("db.Open() = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if _, err := db.Up(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v", err)
	}
	if _, err := sqlDB.Exec(`ALTER TABLE fund ADD COLUMN stray TEXT`); err != nil {
		t.Fatalf("ALTER TABLE = %v", err)
	}
	return path
}

func TestServeRefusesToStartOnAStaleSchema(t *testing.T) {
	path := staleDatabase(t)
	t.Setenv("URUNI_DB", path)
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_UPLOADS_DIR", t.TempDir())
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())

	err := serve()
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("serve() on a stale database = %v, want ErrSchemaMismatch", err)
	}
	for _, want := range []string{path, "table fund changed", "uruni rebuild --from", "SELF-HOSTING.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("serve() error = %q, want it to mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "CREATE TABLE") || strings.Contains(err.Error(), "\n") {
		t.Errorf("serve() error = %q, want one line and no DDL", err)
	}
}

func TestMigrateStatusSaysWhetherTheSchemaMatches(t *testing.T) {
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	status := func(t *testing.T, path string) string {
		t.Helper()
		t.Setenv("URUNI_DB", path)
		var out bytes.Buffer
		if err := migrate(context.Background(), []string{"status"}, &out); err != nil {
			t.Fatalf("migrate([status]) = %v, want no error", err)
		}
		return out.String()
	}

	fresh := filepath.Join(t.TempDir(), "uruni.db")
	t.Setenv("URUNI_DB", fresh)
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())
	if err := migrate(context.Background(), []string{"up"}, io.Discard); err != nil {
		t.Fatalf("migrate([up]) = %v", err)
	}
	if got := status(t, fresh); !strings.Contains(got, "schema: matches this version") {
		t.Errorf("status of an up-to-date database = %q, want it to say the schema matches", got)
	}

	if got := status(t, staleDatabase(t)); !strings.Contains(got, "schema: DIFFERS") || !strings.Contains(got, "table fund changed") {
		t.Errorf("status of a stale database = %q, want it to say the schema differs and name table fund", got)
	}
}

func TestCreateUserRefusesAStaleSchema(t *testing.T) {
	t.Setenv("URUNI_DB", staleDatabase(t))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")

	err := createUser(context.Background(), []string{"treasurer@example.org", "correct-horse-battery"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("createUser() on a stale database = %v, want ErrSchemaMismatch", err)
	}
}
