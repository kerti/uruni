package auth

import (
	"context"
	"errors"
	"testing"
)

// The three tests below cover Register's database-failure arms - the ones a
// happy-path test can never reach. Each has to answer with a plain wrapped
// error, never with one of this package's sentinels: mapAuthError routes
// ErrInvalidArgument to 400 and ErrAlreadyRegistered to 409, so a database
// fault that came back wearing either would tell the treasurer her input
// was wrong when in fact the instance is broken.

// TestRegisterSurfacesAFailureToOpenATransaction: withTx's BeginTx arm.
func TestRegisterSurfacesAFailureToOpenATransaction(t *testing.T) {
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

// TestRegisterSurfacesAFailedCountUsers: the count inside the transaction
// is the one-shot gate, so a count that could not be taken must refuse the
// registration rather than fall through to the insert.
func TestRegisterSurfacesAFailedCountUsers(t *testing.T) {
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

// TestRegisterSurfacesAFailedInsert: the count succeeds and the insert does
// not. A trigger is the only way to fail that one statement while leaving
// the count reading zero, which is exactly the split this arm needs.
func TestRegisterSurfacesAFailedInsert(t *testing.T) {
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
