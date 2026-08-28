package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// TestRegisterCreatesTheAccount is the happy path: the returned row is real
// (a nonzero id, the email as given, a created_at) and the password never
// lands in the row verbatim - password_hash is not the plaintext password.
func TestRegisterCreatesTheAccount(t *testing.T) {
	a, _ := newTestAuth(t)

	user, err := a.Register(context.Background(), "treasurer@example.org", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}

	if user.ID == 0 {
		t.Error("user.ID is zero")
	}
	if user.Email != "treasurer@example.org" {
		t.Errorf("user.Email = %q, want %q", user.Email, "treasurer@example.org")
	}
	if user.CreatedAt == 0 {
		t.Error("user.CreatedAt is zero")
	}
	if user.PasswordHash == "correct-horse-battery" {
		t.Error("password_hash is the plaintext password")
	}
	if !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		t.Errorf("password_hash = %q, want an argon2id-encoded hash", user.PasswordHash)
	}
}

// TestRegisterRefusesASecondAccountRegardlessOfEmail is ADR-030 decision 2's
// whole point: the gate is the row count, not a duplicate email colliding
// with the UNIQUE index. A second registration with a *different* address
// still has to be refused, or resolveFund would hand that stranger
// funds[0].
func TestRegisterRefusesASecondAccountRegardlessOfEmail(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "first@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("first Register() = %v, want no error", err)
	}

	_, err := a.Register(ctx, "second@example.org", "another-long-enough-password")
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register() = %v, want ErrAlreadyRegistered", err)
	}
}

// TestRegisterRefusalWritesNoRow proves the refusal is a pure read: exactly
// one row exists after the second, refused call - the count check and the
// insert share one transaction, so a refused call can't leave a partial or
// extra row behind.
func TestRegisterRefusalWritesNoRow(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "first@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("first Register() = %v, want no error", err)
	}
	if _, err := a.Register(ctx, "second@example.org", "another-long-enough-password"); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register() = %v, want ErrAlreadyRegistered", err)
	}

	count, err := store.New(sqlDB).CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() = %d, want 1 (the refusal must not have written a row)", count)
	}
}

func TestRegisterRefusesAnEmptyOrMalformedEmail(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	for _, email := range []string{"", "   ", "not-an-email"} {
		if _, err := a.Register(ctx, email, "correct-horse-battery"); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Register(%q, ...) = %v, want ErrInvalidArgument", email, err)
		}
	}
}

func TestRegisterRefusesAPasswordUnderTheMinimumLength(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	short := strings.Repeat("a", MinPasswordLength-1)
	if _, err := a.Register(ctx, "treasurer@example.org", short); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Register(..., %q) = %v, want ErrInvalidArgument", short, err)
	}
}

// A rejected call for either validation reason must also write nothing -
// the zero-value boundary alongside the one-shot refusal above.
func TestRegisterValidationFailureWritesNoRow(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "", "correct-horse-battery"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Register(\"\", ...) = %v, want ErrInvalidArgument", err)
	}
	if _, err := a.Register(ctx, "treasurer@example.org", "short"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Register(..., \"short\") = %v, want ErrInvalidArgument", err)
	}

	count, err := store.New(sqlDB).CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 0 {
		t.Errorf("CountUsers() = %d, want 0", count)
	}
}
