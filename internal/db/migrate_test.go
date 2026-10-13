package db

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"testing"
	"testing/fstest"

	"github.com/kerti/uruni/internal/store"
)

func TestUpAppliesPendingMigrationsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)

	before, err := Status(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Status() on a fresh database = %v, want no error", err)
	}
	if len(before) == 0 {
		t.Fatal("Status() on a fresh database returned no migrations, want the embedded ones")
	}
	for _, m := range before {
		if m.Applied {
			t.Errorf("migration %d (%s) reported applied on a fresh database", m.Version, m.Name)
		}
	}

	applied, err := Up(ctx, sqlDB, discardLogger())
	if err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}
	if applied != len(before) {
		t.Errorf("Up() applied %d migrations, want %d", applied, len(before))
	}

	after, err := Status(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Status() after Up() = %v, want no error", err)
	}
	for _, m := range after {
		if !m.Applied {
			t.Errorf("migration %d (%s) still pending after Up()", m.Version, m.Name)
		}
		if m.AppliedAt.IsZero() {
			t.Errorf("migration %d (%s) applied with no timestamp", m.Version, m.Name)
		}
	}

	// `serve` runs Up on every boot (ADR-019), so a second run must be a no-op
	// rather than an error.
	reapplied, err := Up(ctx, sqlDB, discardLogger())
	if err != nil {
		t.Fatalf("second Up() = %v, want no error", err)
	}
	if reapplied != 0 {
		t.Errorf("second Up() applied %d migrations, want 0", reapplied)
	}
}

// threeVersions is a migration set with a baseline and two more files. The real
// set has one file through 0.x (ADR-025), which Down refuses to roll back, so a
// down that runs needs its own set.
func threeVersions() fstest.MapFS {
	file := func(name string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE " + name + " (id INTEGER PRIMARY KEY) STRICT;\n" +
			"-- +goose Down\nDROP TABLE " + name + ";\n")}
	}
	return fstest.MapFS{
		"00001_a.sql": file("a"),
		"00002_b.sql": file("b"),
		"00003_c.sql": file("c"),
	}
}

func upThrough(t *testing.T, sqlDB *sql.DB, fsys fs.FS) {
	t.Helper()
	p, err := providerFor(sqlDB, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func hasTable(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestDownRollsBackOneMigrationAtATimeAndStopsAtTheBaseline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)
	fsys := threeVersions()
	upThrough(t, sqlDB, fsys)

	for _, want := range []int64{3, 2} {
		version, err := Down(ctx, sqlDB, fsys, discardLogger(), nil)
		if err != nil {
			t.Fatalf("Down() = %v, want no error", err)
		}
		if version != want {
			t.Errorf("Down() rolled back version %d, want the most recent %d", version, want)
		}
	}

	// One step per invocation, and the baseline is what is left.
	if !hasTable(t, sqlDB, "a") || hasTable(t, sqlDB, "b") || hasTable(t, sqlDB, "c") {
		t.Errorf("tables after two downs: a=%v b=%v c=%v, want only a", hasTable(t, sqlDB, "a"), hasTable(t, sqlDB, "b"), hasTable(t, sqlDB, "c"))
	}

	if _, err := Down(ctx, sqlDB, fsys, discardLogger(), nil); !errors.Is(err, ErrBaseline) {
		t.Fatalf("Down() at the baseline = %v, want ErrBaseline", err)
	}
	if !hasTable(t, sqlDB, "a") {
		t.Errorf("a refused baseline Down() dropped the baseline's table")
	}
}

func TestDownRefusesTheEmbeddedBaseline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)
	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}

	called := false
	_, err := Down(ctx, sqlDB, nil, discardLogger(), func(int64) error { called = true; return nil })
	if !errors.Is(err, ErrBaseline) {
		t.Fatalf("Down() on the real single-file set = %v, want ErrBaseline", err)
	}
	if called {
		t.Errorf("the before hook ran for a refused rollback")
	}
	applied, err := Status(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if !applied[0].Applied {
		t.Errorf("the baseline is no longer applied after a refused Down()")
	}
}

func TestDownRunsTheBeforeHookFirstAndItsErrorRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)
	fsys := threeVersions()
	upThrough(t, sqlDB, fsys)

	var sawVersion int64
	_, err := Down(ctx, sqlDB, fsys, discardLogger(), func(v int64) error {
		sawVersion = v
		if !hasTable(t, sqlDB, "c") {
			t.Error("before ran after the rollback, want it first")
		}
		return nil
	})
	if err != nil || sawVersion != 3 {
		t.Fatalf("Down() = %v with before seeing version %d, want nil and 3", err, sawVersion)
	}

	boom := errors.New("no dump")
	if _, err := Down(ctx, sqlDB, fsys, discardLogger(), func(int64) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("Down() with a failing before = %v, want its error", err)
	}
	if !hasTable(t, sqlDB, "b") {
		t.Errorf("migration 2 was rolled back although before failed")
	}
}

func TestDownOnAnUnmigratedDatabaseIsNotAFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)

	// Nothing applied. Rolling back nothing is a non-event the CLI reports and
	// exits 0 on, so a script rolling back before restoring a backup keeps working.
	if _, err := Down(ctx, sqlDB, nil, discardLogger(), nil); !errors.Is(err, ErrNothingToRollBack) {
		t.Fatalf("Down() on version 0 = %v, want ErrNothingToRollBack", err)
	}
}

func TestOpenMigrateQueryThroughTheGeneratedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)

	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}

	// The point of M1.3: the database path works end to end (opened with its
	// pragmas, migrated, queried through sqlc-generated code, ADR-005), not
	// hand-written SQL in a test.
	ok, err := store.New(sqlDB).Ping(ctx)
	if err != nil {
		t.Fatalf("Ping() = %v, want no error", err)
	}
	if ok != 1 {
		t.Errorf("Ping() = %d, want 1", ok)
	}
}

// discardLogger is for tests that care about the migration, not the line it
// prints. main builds the real one from URUNI_LOG_* (ADR-022).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
