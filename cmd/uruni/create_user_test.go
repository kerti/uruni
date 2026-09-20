package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDispatchesCreateUser: `make dev-user` invokes this name (ADR-019),
// so the wiring itself is worth a test - #287 was exactly this wiring missing.
func TestRunDispatchesCreateUser(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")

	if err := run([]string{"create-user", "treasurer@example.org", "correct-horse-battery"}); err != nil {
		t.Fatalf("run([create-user ...]) = %v, want nil", err)
	}
}

// TestCreateUserCreatesThenResets is the issue's acceptance pair: the first
// run creates the login on a fresh database, the second resets it instead of
// failing - and the password appears in neither line of output.
func TestCreateUserCreatesThenResets(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	ctx := context.Background()

	var first, second bytes.Buffer
	if err := createUser(ctx, []string{"treasurer@example.org", "correct-horse-battery"}, &first); err != nil {
		t.Fatalf("first createUser() = %v, want nil", err)
	}
	if err := createUser(ctx, []string{"treasurer@example.org", "another-long-enough-password"}, &second); err != nil {
		t.Fatalf("second createUser() = %v, want nil", err)
	}

	if !strings.HasPrefix(first.String(), "created") {
		t.Errorf("first output = %q, want it to report a created login", first.String())
	}
	if !strings.HasPrefix(second.String(), "reset") {
		t.Errorf("second output = %q, want it to report a reset", second.String())
	}
	for _, out := range []string{first.String(), second.String()} {
		if strings.Contains(out, "correct-horse-battery") || strings.Contains(out, "another-long-enough-password") {
			t.Errorf("output %q contains the password", out)
		}
	}
}

func TestCreateUserRefusesASecondLogin(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	ctx := context.Background()

	var out bytes.Buffer
	if err := createUser(ctx, []string{"first@example.org", "correct-horse-battery"}, &out); err != nil {
		t.Fatalf("first createUser() = %v, want nil", err)
	}
	err := createUser(ctx, []string{"second@example.org", "another-long-enough-password"}, &out)
	if err == nil || strings.Contains(err.Error(), "another-long-enough-password") {
		t.Fatalf("createUser(second email) = %v, want a refusal that does not echo the password", err)
	}
}

func TestCreateUserWantsExactlyTwoArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"treasurer@example.org"}, {"a@b", "c", "d"}} {
		if err := createUser(context.Background(), args, &bytes.Buffer{}); !errors.Is(err, errCreateUserUsage) {
			t.Errorf("createUser(%q) = %v, want errCreateUserUsage", args, err)
		}
	}
}
