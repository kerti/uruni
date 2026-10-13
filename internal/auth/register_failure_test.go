package auth

import (
	"context"
	"errors"
	"testing"
)

// These tests cover Register's database-failure arms, which a happy-path test
// never reaches. Each must answer a plain wrapped error, never a sentinel of
// this package: mapAuthError routes ErrInvalidArgument to 400 and
// ErrAlreadyRegistered to 409, so a database fault wearing either would tell
// the treasurer her input was wrong when the instance is broken.

// TestRegisterSurfacesAFailureToOpenATransaction: withTx's BeginTx arm.
func TestRegisterSurfacesAFailureToOpenATransaction(t *testing.T) {
	t.Parallel()
	a, sqlDB := newTestAuth(t)
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("Close() = %v, want no error", err)
	}

	_, err := a.Register(context.Background(), "treasurer@example.org", "correct-horse-battery")
	if err == nil {
		t.Fatal("Register() on a closed database = nil, want an error")
	}
	assertNotASentinel(t, err)
}

// TestRegisterSurfacesAFailedPreCheck: the cheap count before hashing (#495)
// failing must refuse the registration, not fall through to hashing.
func TestRegisterSurfacesAFailedPreCheck(t *testing.T) {
	t.Parallel()
	a, sqlDB := newTestAuth(t)
	if _, err := sqlDB.Exec("DROP TABLE user"); err != nil {
		t.Fatalf("dropping user: %v", err)
	}

	_, err := a.Register(context.Background(), "treasurer@example.org", "correct-horse-battery")
	if err == nil {
		t.Fatal("Register() with no user table = nil, want an error")
	}
	assertNotASentinel(t, err)
}

// TestRegisterSurfacesAFailedCountUsers: the count in the transaction is the
// one-shot gate, so a count that could not be taken must refuse the
// registration, not fall through to the insert. The pre-check would trip first
// on a table never there, so the table is dropped from inside the hash: after
// the pre-check passed, before the transaction opens.
func TestRegisterSurfacesAFailedCountUsers(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	a := New(sqlDB, WithPasswordFuncs(func(password string) (string, error) {
		if _, err := sqlDB.Exec("DROP TABLE user"); err != nil {
			t.Errorf("dropping user: %v", err)
		}
		return hashPassword(password)
	}, verifyPassword))

	_, err := a.Register(context.Background(), "treasurer@example.org", "correct-horse-battery")
	if err == nil {
		t.Fatal("Register() with no user table = nil, want an error")
	}
	assertNotASentinel(t, err)
}

// TestRegisterSurfacesAFailedInsert: the count succeeds, the insert does not. A
// trigger is the only way to fail that one statement while the count reads
// zero, the split this arm needs.
func TestRegisterSurfacesAFailedInsert(t *testing.T) {
	t.Parallel()
	a, sqlDB := newTestAuth(t)
	if _, err := sqlDB.Exec(`CREATE TRIGGER refuse_insert BEFORE INSERT ON user
		BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatalf("creating the trigger: %v", err)
	}

	_, err := a.Register(context.Background(), "treasurer@example.org", "correct-horse-battery")
	if err == nil {
		t.Fatal("Register() with a refused insert = nil, want an error")
	}
	assertNotASentinel(t, err)

	var count int
	if err := sqlDB.QueryRow("SELECT count(*) FROM user").Scan(&count); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if count != 0 {
		t.Errorf("user rows = %d, want 0 - the failed insert must have rolled back", count)
	}
}

func assertNotASentinel(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("error = %v, want a plain failure, not ErrAlreadyRegistered (409)", err)
	}
	if errors.Is(err, ErrInvalidArgument) {
		t.Errorf("error = %v, want a plain failure, not ErrInvalidArgument (400)", err)
	}
}
