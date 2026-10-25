package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/db"
)

// TestSeedE2ERefusesAnUnsetURUNI_DB is the guard's key case: no silent
// fallback to config.DefaultDBPath, a developer's real dev database.
func TestSeedE2ERefusesAnUnsetURUNI_DB(t *testing.T) {
	t.Setenv("URUNI_DB", "")

	err := seedE2E(context.Background())
	if err == nil {
		t.Fatal("seedE2E() = nil, want a refusal for an unset URUNI_DB")
	}
	if !strings.Contains(err.Error(), "URUNI_DB") {
		t.Errorf("seedE2E() = %q, want it to name URUNI_DB", err)
	}
}

// TestSeedE2ERefusesTheDevDatabase: an ordinary project-local database path
// must refuse rather than wipe, though a perfectly valid SQLite file.
func TestSeedE2ERefusesTheDevDatabase(t *testing.T) {
	t.Setenv("URUNI_DB", "./uruni.db")

	err := seedE2E(context.Background())
	if err == nil {
		t.Fatal("seedE2E() = nil, want a refusal for a non-throwaway path")
	}
	if !strings.Contains(err.Error(), "uruni.db") {
		t.Errorf("seedE2E() = %q, want it to name the rejected path", err)
	}
}

// TestSeedE2ERefusesATempPathWithoutE2EInTheName: living under a temp
// directory is not enough; a hand-made "/tmp/uruni.db" must not look
// throwaway because of where it sits.
func TestSeedE2ERefusesATempPathWithoutE2EInTheName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db")
	t.Setenv("URUNI_DB", path)

	if err := seedE2E(context.Background()); err == nil {
		t.Fatal("seedE2E() = nil, want a refusal for a temp path with no \"e2e\" in its name")
	}
}

// TestSeedE2ESeedsAThrowawayDatabase is the happy path: a path satisfying both
// halves of the guard (under a temp directory, "e2e" in the name) migrates and
// seeds cleanly against a database that does not exist yet, the only state
// `make e2e` calls it in, as `make e2e-reset` deletes the file first.
func TestSeedE2ESeedsAThrowawayDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni-e2e.db")
	t.Setenv("URUNI_DB", path)

	if err := seedE2E(context.Background()); err != nil {
		t.Fatalf("seedE2E() = %v, want no error against a fresh throwaway path", err)
	}

	// The seeded members are tiered through member_tier (ADR-040), from their join
	// month, so the e2e journeys that read dues see a tier.
	sqlDB, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	var members, rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM member`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM member_tier WHERE effective_from = '2024-01' AND correction = 0 AND tier_id IS NOT NULL`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if members != 2 || rows != 2 {
		t.Errorf("seeded %d members and %d first tier rows, want 2 and 2", members, rows)
	}
}

// TestSeedE2ERefusesAnAlreadySeededDatabase: the command is not idempotent.
// auth.Register is single-account (ErrAlreadyRegistered), so a second run on a
// seeded database fails loudly rather than seeding a second fund. `make
// e2e-reset` deleting the file makes a re-run work.
func TestSeedE2ERefusesAnAlreadySeededDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni-e2e.db")
	t.Setenv("URUNI_DB", path)

	if err := seedE2E(context.Background()); err != nil {
		t.Fatalf("first seedE2E() = %v, want no error", err)
	}

	err := seedE2E(context.Background())
	if err == nil {
		t.Fatal("second seedE2E() = nil, want an error against an already-seeded database")
	}
	if !errors.Is(err, auth.ErrAlreadyRegistered) {
		t.Errorf("second seedE2E() = %v, want auth.ErrAlreadyRegistered", err)
	}
}

// TestSeedE2EReportsADatabaseItCannotOpen: the failure an operator most likely
// hits past the guard, a throwaway-looking path seedE2E cannot open. The error
// must name the path, the only thing the operator controls (URUNI_DB).
func TestSeedE2EReportsADatabaseItCannotOpen(t *testing.T) {
	// A directory where a database file belongs: passes both halves of the guard,
	// then fails at db.Open's ping.
	path := filepath.Join(t.TempDir(), "uruni-e2e.db")
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatalf("creating the directory standing in for a database: %v", err)
	}
	t.Setenv("URUNI_DB", path)

	err := seedE2E(context.Background())
	if err == nil {
		t.Fatal("seedE2E() = nil, want an error for a database it cannot open")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("seedE2E() = %q, want it to name %q", err, path)
	}
}

func TestRequireThrowawayDBPath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "empty", path: "", wantErr: true},
		{name: "relative dev db", path: "./uruni.db", wantErr: true},
		{name: "absolute dev db", path: "/home/treasurer/uruni.db", wantErr: true},
		{name: "temp dir without e2e in the name", path: filepath.Join(t.TempDir(), "uruni.db"), wantErr: true},
		{name: "a tmp e2e db", path: "/tmp/uruni-e2e.db", wantErr: false},
		{name: "the makefile's own e2e db", path: "/work/uruni/.run/e2e.db", wantErr: false},
		{name: "a .run file without e2e in the name", path: "/work/uruni/.run/uruni.db", wantErr: true},
		{name: "a file merely named .run-e2e", path: "/work/uruni/.run-x/e2e.db", wantErr: true},
		{name: "another e2e-named file under a temp dir", path: filepath.Join(t.TempDir(), "uruni-e2e.db"), wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireThrowawayDBPath(tc.path)
			if tc.wantErr && err == nil {
				t.Errorf("requireThrowawayDBPath(%q) = nil, want a refusal", tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("requireThrowawayDBPath(%q) = %v, want no error", tc.path, err)
			}
		})
	}
}
