package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/txguard"
)

// TestRegisterCreatesTheAccount is the happy path: the returned row is real (a
// nonzero id, the email as given, a created_at) and password_hash is not the
// plaintext password.
func TestRegisterCreatesTheAccount(t *testing.T) {
	t.Parallel()
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
// point: the gate is the row count, not a duplicate email colliding with the
// UNIQUE index. A second registration with a different address must still be
// refused, or resolveFund would hand that stranger funds[0].
func TestRegisterRefusesASecondAccountRegardlessOfEmail(t *testing.T) {
	t.Parallel()
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

// TestRegisterRefusalWritesNoRow proves the refusal is a pure read: one row
// exists after the second, refused call, as the count check and insert share
// one transaction.
func TestRegisterRefusalWritesNoRow(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()

	for _, email := range []string{"", "   ", "not-an-email"} {
		if _, err := a.Register(ctx, email, "correct-horse-battery"); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Register(%q, ...) = %v, want ErrInvalidArgument", email, err)
		}
	}
}

func TestRegisterRefusesAPasswordUnderTheMinimumLength(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()

	short := strings.Repeat("a", MinPasswordLength-1)
	if _, err := a.Register(ctx, "treasurer@example.org", short); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Register(..., %q) = %v, want ErrInvalidArgument", short, err)
	}
}

// A rejected call for either validation reason also writes nothing: the
// zero-value boundary beside the one-shot refusal above.
func TestRegisterValidationFailureWritesNoRow(t *testing.T) {
	t.Parallel()
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

// The pool holds one connection and withTx's transaction owns it, so a nested
// withTx or a pool read inside fn would wait on a connection its own caller
// holds. Each case runs under a
// short deadline: a regression fails here instead of hanging the suite.
func TestWithTxRefusesWhatWouldDeadlockOnTheHeldConnection(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)

	cases := []struct {
		name string
		do   func(ctx context.Context) error
	}{
		{"a nested withTx", func(ctx context.Context) error {
			return a.withTx(ctx, func(context.Context, store.Querier) error { return nil })
		}},
		{"a read through a.q", func(ctx context.Context) error {
			_, err := a.q.CountUsers(ctx)
			return err
		}},
		{"a many-row read through a.q", func(ctx context.Context) error {
			_, err := a.q.ListUsers(ctx)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := a.withTx(ctx, func(ctx context.Context, _ store.Querier) error {
				return tc.do(ctx)
			})
			if !errors.Is(err, txguard.ErrNested) {
				t.Fatalf("withTx(%s) = %v, want txguard.ErrNested", tc.name, err)
			}
			if ctx.Err() != nil {
				t.Errorf("withTx(%s) ran into its deadline: it blocked instead of refusing", tc.name)
			}
		})
	}
}

// A refused nested call leaves the connection free: the next withTx works.
func TestWithTxStillWorksAfterANestedRefusal(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = a.withTx(ctx, func(ctx context.Context, _ store.Querier) error {
		return a.withTx(ctx, func(context.Context, store.Querier) error { return nil })
	})

	if err := a.withTx(ctx, func(context.Context, store.Querier) error { return nil }); err != nil {
		t.Fatalf("withTx() after a refused nested call = %v, want no error", err)
	}
}
