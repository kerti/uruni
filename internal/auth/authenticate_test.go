package auth

import (
	"context"
	"errors"
	"testing"
)

// TestAuthenticateAcceptsTheCorrectPassword is the happy path: the account
// Register created verifies against the same password and returns the real
// row.
func TestAuthenticateAcceptsTheCorrectPassword(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	created, err := a.Register(ctx, "treasurer@example.org", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}

	user, err := a.Authenticate(ctx, "treasurer@example.org", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Authenticate(correct password) = %v, want no error", err)
	}
	if user.ID != created.ID {
		t.Errorf("Authenticate() user.ID = %d, want %d", user.ID, created.ID)
	}
	if user.Email != created.Email {
		t.Errorf("Authenticate() user.Email = %q, want %q", user.Email, created.Email)
	}
}

// TestAuthenticateWrongPasswordAndUnknownEmailReturnTheSameSentinel is
// issue #115's "one generic failure": errors.Is must report true for
// ErrInvalidCredentials on both a wrong password and an email that names no
// account at all - neither case may surface a different error a caller
// could branch on.
func TestAuthenticateWrongPasswordAndUnknownEmailReturnTheSameSentinel(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}

	_, wrongPasswordErr := a.Authenticate(ctx, "treasurer@example.org", "not-the-password")
	if !errors.Is(wrongPasswordErr, ErrInvalidCredentials) {
		t.Errorf("Authenticate(wrong password) = %v, want ErrInvalidCredentials", wrongPasswordErr)
	}

	_, unknownEmailErr := a.Authenticate(ctx, "nobody@example.org", "whatever-password")
	if !errors.Is(unknownEmailErr, ErrInvalidCredentials) {
		t.Errorf("Authenticate(unknown email) = %v, want ErrInvalidCredentials", unknownEmailErr)
	}
}

// TestAuthenticateRejectsAnEmptyPasswordAgainstARealAccount: an empty guess
// is still just a wrong password, not a special case.
func TestAuthenticateRejectsAnEmptyPasswordAgainstARealAccount(t *testing.T) {
	a, _ := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}

	_, err := a.Authenticate(ctx, "treasurer@example.org", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(empty password) = %v, want ErrInvalidCredentials", err)
	}
}

// TestAuthenticateOnAFreshInstanceWithNoAccountAtAllReturnsTheSameSentinel:
// the "no such email" arm must not require any row to exist anywhere - a
// login attempt before Register has ever run once is just another unknown
// email.
func TestAuthenticateOnAFreshInstanceWithNoAccountAtAllReturnsTheSameSentinel(t *testing.T) {
	a, _ := newTestAuth(t)

	_, err := a.Authenticate(context.Background(), "nobody@example.org", "whatever-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate() on a fresh instance = %v, want ErrInvalidCredentials", err)
	}
}

// TestAuthenticateSurfacesADatabaseFailureAsAPlainError: a lookup that
// fails for an infrastructure reason - here the database is closed
// underneath it - must not be mistaken for ErrInvalidCredentials.
// mapAuthError would otherwise answer a broken instance with "wrong
// password" instead of a 500.
func TestAuthenticateSurfacesADatabaseFailureAsAPlainError(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("Close() = %v, want no error", err)
	}

	_, err := a.Authenticate(context.Background(), "treasurer@example.org", "whatever-password")
	if err == nil {
		t.Fatal("Authenticate() on a closed database = nil, want an error")
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate() on a closed database = %v, want a plain failure, not ErrInvalidCredentials", err)
	}
}

// TestAuthenticateSurfacesAMalformedStoredHashAsAPlainError: a hand-edited
// password_hash column (ADR-030's recovery story puts an operator at this
// exact column) is a corrupted row, not a guessed-wrong password - it must
// not come back as ErrInvalidCredentials either.
func TestAuthenticateSurfacesAMalformedStoredHashAsAPlainError(t *testing.T) {
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()

	if _, err := a.Register(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}
	if _, err := sqlDB.Exec(`UPDATE "user" SET password_hash = 'not-a-real-hash'`); err != nil {
		t.Fatalf("corrupting password_hash: %v", err)
	}

	_, err := a.Authenticate(ctx, "treasurer@example.org", "correct-horse-battery")
	if err == nil {
		t.Fatal("Authenticate() against a corrupted hash = nil, want an error")
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate() against a corrupted hash = %v, want a plain failure, not ErrInvalidCredentials", err)
	}
	if !errors.Is(err, ErrMalformedHash) {
		t.Errorf("Authenticate() against a corrupted hash = %v, want it to wrap ErrMalformedHash", err)
	}
}
