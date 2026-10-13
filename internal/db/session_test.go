package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// #113: the raw round trip on user and session, before #114-#116 build on it.
// No password hashing here (that is #114's).

func TestUserRoundTrip(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	//nolint:gosec // not a credential - a placeholder hash string; hashing itself is #114's
	created, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "treasurer@example.org", PasswordHash: "argon2id$fake", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateUser() = %v, want no error", err)
	}

	got, err := q.GetUserByEmail(ctx, store.GetUserByEmailParams{Normalized: "treasurer@example.org", Exact: "treasurer@example.org"})
	if err != nil {
		t.Fatalf("GetUserByEmail() = %v, want no error", err)
	}
	if got.ID != created.ID || got.PasswordHash != "argon2id$fake" {
		t.Errorf("GetUserByEmail() = %+v, want it to match the created row %+v", got, created)
	}
}

func TestUserEmailIsUnique(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if _, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "treasurer@example.org", PasswordHash: "hash-1", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first CreateUser() = %v, want no error", err)
	}

	if _, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "treasurer@example.org", PasswordHash: "hash-2", CreatedAt: 2,
	}); err == nil {
		t.Fatal("second CreateUser() with the same email = nil error, want the UNIQUE constraint to reject it")
	}
}

// CountUsers is register's (#114) one-shot bootstrap gate: zero before the
// first account, positive immediately after.
func TestCountUsersReflectsBootstrapState(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if got, err := q.CountUsers(ctx); err != nil || got != 0 {
		t.Fatalf("CountUsers() before any account = (%d, %v), want (0, nil)", got, err)
	}

	if _, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "treasurer@example.org", PasswordHash: "hash", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateUser() = %v, want no error", err)
	}

	if got, err := q.CountUsers(ctx); err != nil || got != 1 {
		t.Fatalf("CountUsers() after one account = (%d, %v), want (1, nil)", got, err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	created, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: "tok-1", Data: []byte("payload"), ExpiresAt: 1000,
	})
	if err != nil {
		t.Fatalf("CreateSession() = %v, want no error", err)
	}

	got, err := q.GetSession(ctx, store.GetSessionParams{Token: "tok-1", ExpiresAt: 500})
	if err != nil {
		t.Fatalf("GetSession() = %v, want no error", err)
	}
	if got.Token != created.Token || string(got.Data) != "payload" {
		t.Errorf("GetSession() = %+v, want it to match the created row %+v", got, created)
	}

	if err := q.DeleteSession(ctx, "tok-1"); err != nil {
		t.Fatalf("DeleteSession() = %v, want no error", err)
	}

	if _, err := q.GetSession(ctx, store.GetSessionParams{Token: "tok-1", ExpiresAt: 500}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession() after DeleteSession() = %v, want sql.ErrNoRows", err)
	}
}

// TestSessionExpiryBoundary is the sliding 30-day idle timeout's edge (#113):
// GetSession compares expires_at > ?, strictly, so a session exactly at its
// expiry is gone and one a second before is live.
func TestSessionExpiryBoundary(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	const expiresAt = int64(1_000_000)
	if _, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: "tok-boundary", Data: []byte("payload"), ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("CreateSession() = %v, want no error", err)
	}

	if _, err := q.GetSession(ctx, store.GetSessionParams{
		Token: "tok-boundary", ExpiresAt: expiresAt,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession() with now == expires_at = %v, want sql.ErrNoRows (exactly-at-expiry is expired)", err)
	}

	if _, err := q.GetSession(ctx, store.GetSessionParams{
		Token: "tok-boundary", ExpiresAt: expiresAt - 1,
	}); err != nil {
		t.Errorf("GetSession() with now one second before expires_at = %v, want no error (still live)", err)
	}
}

// TestTouchSessionSlidesExpiryForward is the "sliding" half: a live session's
// expires_at moves forward, and the guard protecting GetSession also stops
// TouchSession reviving a lapsed session.
func TestTouchSessionSlidesExpiryForward(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if _, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: "tok-1", Data: []byte("payload"), ExpiresAt: 1000,
	}); err != nil {
		t.Fatalf("CreateSession() = %v, want no error", err)
	}

	touched, err := q.TouchSession(ctx, store.TouchSessionParams{
		NewExpiresAt: 2000, Token: "tok-1", NotBefore: 500,
	})
	if err != nil {
		t.Fatalf("TouchSession() = %v, want no error", err)
	}
	if touched.ExpiresAt != 2000 {
		t.Errorf("TouchSession() expires_at = %d, want 2000", touched.ExpiresAt)
	}

	if _, err := q.TouchSession(ctx, store.TouchSessionParams{
		NewExpiresAt: 9999, Token: "tok-1", NotBefore: 2000,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("TouchSession() on an already-expired session = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteExpiredSessionsSweepsOnlyPastRows(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if _, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: "tok-expired", Data: []byte("payload"), ExpiresAt: 1000,
	}); err != nil {
		t.Fatalf("CreateSession(tok-expired) = %v, want no error", err)
	}
	if _, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: "tok-live", Data: []byte("payload"), ExpiresAt: 5000,
	}); err != nil {
		t.Fatalf("CreateSession(tok-live) = %v, want no error", err)
	}

	if err := q.DeleteExpiredSessions(ctx, 1000); err != nil {
		t.Fatalf("DeleteExpiredSessions() = %v, want no error", err)
	}

	if _, err := q.GetSession(ctx, store.GetSessionParams{Token: "tok-expired", ExpiresAt: 0}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession(tok-expired) after sweep = %v, want sql.ErrNoRows", err)
	}
	if _, err := q.GetSession(ctx, store.GetSessionParams{Token: "tok-live", ExpiresAt: 0}); err != nil {
		t.Errorf("GetSession(tok-live) after sweep = %v, want no error (not yet expired)", err)
	}
}

func TestSessionTokenCannotBeEmpty(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if _, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: "", Data: []byte("payload"), ExpiresAt: 1000,
	}); err == nil {
		t.Fatal("CreateSession with an empty token = nil error, want the CHECK to reject it")
	}
}
