package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRejectsAnEmptyVerb(t *testing.T) {
	if err := migrate(context.Background(), nil, io.Discard); !errors.Is(err, ErrNoCommand) {
		t.Fatalf("migrate(nil) = %v, want ErrNoCommand", err)
	}
}

func TestMigrateRejectsAnUnknownVerbBeforeTouchingTheDatabase(t *testing.T) {
	// No URUNI_BASE_URL is set here on purpose: a rejected verb must not get as
	// far as config.Load or as creating a database file.
	err := migrate(context.Background(), []string{"sideways"}, io.Discard)
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("migrate([sideways]) = %v, want ErrUnknownCommand", err)
	}
	for _, verb := range []string{"up", "down", "status"} {
		if !strings.Contains(err.Error(), verb) {
			t.Errorf("migrate([sideways]) = %q, want it to mention %q", err, verb)
		}
	}
}

func TestMigrateUpStatusDownAgainstAFreshFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "uruni.db")
	t.Setenv("URUNI_DB", dbPath)
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")

	run := func(t *testing.T, verb string) string {
		t.Helper()
		var out bytes.Buffer
		if err := migrate(context.Background(), []string{verb}, &out); err != nil {
			t.Fatalf("migrate([%s]) = %v, want no error", verb, err)
		}
		return out.String()
	}

	// A fresh, absent file: `migrate up` creates and migrates it, which is the
	// same path `serve` takes on an operator's first boot.
	if got := run(t, "up"); !strings.Contains(got, "applied 1 migration") {
		t.Errorf("migrate([up]) printed %q, want it to report what it applied", got)
	}
	if got := run(t, "up"); !strings.Contains(got, "already up to date") {
		t.Errorf("second migrate([up]) printed %q, want it to report nothing to do", got)
	}

	// M2.1 replaced the temporary baseline migration with the real first
	// migration (see 00001_schema.sql), so these assertions now name that one.
	status := run(t, "status")
	// The file name is on the line because migrating the wrong database is the
	// commonest surprise here (URUNI_DB unset, or .env not exported).
	if !strings.Contains(status, dbPath) {
		t.Errorf("migrate([status]) printed %q, want it to name %q", status, dbPath)
	}
	if !strings.Contains(status, "applied") || !strings.Contains(status, "00001_schema.sql") {
		t.Errorf("migrate([status]) printed %q, want the first migration reported applied", status)
	}

	if got := run(t, "down"); !strings.Contains(got, "rolled back migration 1") {
		t.Errorf("migrate([down]) printed %q, want it to name what it rolled back", got)
	}
	if got := run(t, "down"); !strings.Contains(got, "nothing to roll back") {
		t.Errorf("second migrate([down]) printed %q, want the non-event reported", got)
	}
}
