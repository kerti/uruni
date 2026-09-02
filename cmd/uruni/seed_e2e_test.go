package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
)

// TestSeedE2ERefusesAnUnsetURUNI_DB is the guard's most important case: no
// silent fallback to config.DefaultDBPath, which in a developer's working
// copy is their real dev database.
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

// TestSeedE2ERefusesTheDevDatabase covers the guard's whole point: pointing
// it at what looks like an ordinary project-local database must refuse
// rather than wipe it, even though the path is a perfectly valid SQLite
// file.
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
// directory alone isn't enough — a stray "/tmp/uruni.db" a developer created
// by hand while debugging something else must not look throwaway just
// because of where it happens to sit.
func TestSeedE2ERefusesATempPathWithoutE2EInTheName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni.db")
	t.Setenv("URUNI_DB", path)

	if err := seedE2E(context.Background()); err == nil {
		t.Fatal("seedE2E() = nil, want a refusal for a temp path with no \"e2e\" in its name")
	}
}

// TestSeedE2ESeedsAThrowawayDatabase is the happy path: a path that satisfies
// both halves of the guard (under a temp directory, "e2e" in the filename)
// migrates and seeds cleanly against a database that does not exist yet —
// which is the only state `make e2e` ever calls it in, since `make e2e-reset`
// deletes the file first.
func TestSeedE2ESeedsAThrowawayDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni-e2e.db")
	t.Setenv("URUNI_DB", path)

	if err := seedE2E(context.Background()); err != nil {
		t.Fatalf("seedE2E() = %v, want no error against a fresh throwaway path", err)
	}
}

// TestSeedE2ERefusesAnAlreadySeededDatabase pins the other half of that: the
// command is *not* idempotent and must not pretend to be. auth.Register is
// single-account by design (ErrAlreadyRegistered), so a second run against a
// database that still has the first fixture fails loudly rather than seeding a
// second fund on top of the first. `make e2e-reset` deleting the file is what
// makes a re-run work, and this is the test that says so.
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

// TestSeedE2EReportsADatabaseItCannotOpen covers the failure an operator is
// most likely to actually hit past the guard: the path looks throwaway but is
// not a file seedE2E can open. The error has to name the path, because the
// only thing the operator controls here is URUNI_DB.
func TestSeedE2EReportsADatabaseItCannotOpen(t *testing.T) {
	// A directory where a database file should be: passes both halves of the
	// guard, then fails at db.Open's ping.
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
		{name: "the makefile's own e2e db", path: "/tmp/uruni-e2e.db", wantErr: false},
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
