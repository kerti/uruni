package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// TestSetLoginCreatesTheFirstLogin: on a fresh database SetLogin creates the
// row, and the login it created authenticates.
func TestSetLoginCreatesTheFirstLogin(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	created, err := a.SetLogin(ctx, "treasurer@example.org", "correct-horse-battery")
	if err != nil {
		t.Fatalf("SetLogin() = %v, want no error", err)
	}
	if !created {
		t.Error("SetLogin() created = false on an empty database, want true")
	}
	if _, err := a.Authenticate(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Errorf("Authenticate() after SetLogin = %v, want no error", err)
	}
}

// TestSetLoginResetsAnExistingPassword is the half #287 exists for: a dev
// database whose password nobody remembers. The old password stops working,
// the new one works, and no second row appears.
func TestSetLoginResetsAnExistingPassword(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "treasurer@example.org", "forgotten-password"); err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}

	created, err := a.SetLogin(ctx, "treasurer@example.org", "correct-horse-battery")
	if err != nil {
		t.Fatalf("SetLogin() = %v, want no error", err)
	}
	if created {
		t.Error("SetLogin() created = true for an existing email, want false (a reset)")
	}
	if _, err := a.Authenticate(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Errorf("Authenticate(new password) = %v, want no error", err)
	}
	if _, err := a.Authenticate(ctx, "treasurer@example.org", "forgotten-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(old password) = %v, want ErrInvalidCredentials", err)
	}

	count, err := store.New(sqlDB).CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() = %d, want 1", count)
	}
}

// TestSetLoginResetSignsOutEverySession: a cookie issued under the old
// password must not outlive the reset.
func TestSetLoginResetSignsOutEverySession(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if _, err := a.Register(ctx, "treasurer@example.org", "forgotten-password"); err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}
	now := time.Now().Unix()
	if _, err := q.CreateSession(ctx, store.CreateSessionParams{Token: "old-cookie", Data: []byte("x"), ExpiresAt: now + 3600}); err != nil {
		t.Fatalf("CreateSession() = %v, want no error", err)
	}

	if _, err := a.SetLogin(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("SetLogin() = %v, want no error", err)
	}

	if _, err := q.GetSession(ctx, store.GetSessionParams{Token: "old-cookie", ExpiresAt: now}); err == nil {
		t.Error("GetSession(old-cookie) found a row after the reset, want it deleted")
	}
}

// TestSetLoginRefusesASecondLogin keeps ADR-030 decision 2's one-login
// invariant: a different email is refused, not added beside the first, and
// the existing password is left alone.
func TestSetLoginRefusesASecondLogin(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "first@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}

	if _, err := a.SetLogin(ctx, "second@example.org", "another-long-enough-password"); !errors.Is(err, ErrOtherAccountExists) {
		t.Fatalf("SetLogin(other email) = %v, want ErrOtherAccountExists", err)
	}

	count, err := store.New(sqlDB).CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() = %d, want 1 (the refusal must not have written a row)", count)
	}
	if _, err := a.Authenticate(ctx, "first@example.org", "correct-horse-battery"); err != nil {
		t.Errorf("Authenticate(first) = %v, want the first login untouched", err)
	}
}

func TestSetLoginRefusesABadEmailOrShortPassword(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	for _, tc := range []struct{ email, password string }{
		{"", "correct-horse-battery"},
		{"not-an-email", "correct-horse-battery"},
		{"treasurer@example.org", "short"},
	} {
		if _, err := a.SetLogin(ctx, tc.email, tc.password); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("SetLogin(%q, %q) = %v, want ErrInvalidArgument", tc.email, tc.password, err)
		}
	}
}
