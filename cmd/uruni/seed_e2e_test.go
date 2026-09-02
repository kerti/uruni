package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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
// resets, migrates and seeds cleanly, and running it twice is not itself an
// error the caller has to special-case — `make e2e-reset` deletes the file
// first, but this proves seedE2E doesn't depend on that deletion to succeed
// against a database it has never seen.
func TestSeedE2ESeedsAThrowawayDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uruni-e2e.db")
	t.Setenv("URUNI_DB", path)

	if err := seedE2E(context.Background()); err != nil {
		t.Fatalf("seedE2E() = %v, want no error against a fresh throwaway path", err)
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
