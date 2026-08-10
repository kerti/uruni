package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

func TestUpAppliesPendingMigrationsAndIsIdempotent(t *testing.T) {
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

func TestDownRollsBackOneMigrationAtATime(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)

	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}

	applied, err := Status(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Status() = %v, want no error", err)
	}
	latest := applied[len(applied)-1]

	version, err := Down(ctx, sqlDB, discardLogger())
	if err != nil {
		t.Fatalf("Down() = %v, want no error", err)
	}
	if version != latest.Version {
		t.Errorf("Down() rolled back version %d, want the most recent %d", version, latest.Version)
	}

	rolled, err := Status(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Status() after Down() = %v, want no error", err)
	}
	if rolled[len(rolled)-1].Applied {
		t.Errorf("migration %d still applied after Down()", latest.Version)
	}
	// One step per invocation: everything below the last migration is untouched.
	for _, m := range rolled[:len(rolled)-1] {
		if !m.Applied {
			t.Errorf("Down() also rolled back migration %d (%s), want one step only", m.Version, m.Name)
		}
	}
}

func TestDownOnAnUnmigratedDatabaseIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)

	// Nothing applied at all. Rolling back nothing is a non-event the CLI reports
	// and exits 0 on, so a script that rolls back before restoring a backup keeps
	// working.
	if _, err := Down(ctx, sqlDB, discardLogger()); !errors.Is(err, ErrNothingToRollBack) {
		t.Fatalf("Down() on version 0 = %v, want ErrNothingToRollBack", err)
	}
}

func TestOpenMigrateQueryThroughTheGeneratedStore(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)

	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}

	// The whole point of M1.3: the database path works end to end — opened with
	// its pragmas, migrated, and queried through the sqlc-generated code
	// (ADR-005) rather than through hand-written SQL in a test.
	ok, err := store.New(sqlDB).Ping(ctx)
	if err != nil {
		t.Fatalf("Ping() = %v, want no error", err)
	}
	if ok != 1 {
		t.Errorf("Ping() = %d, want 1", ok)
	}
}

// discardLogger is the logger for tests that care about the migration, not the
// line it prints. main builds the real one from URUNI_LOG_* (ADR-022).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
