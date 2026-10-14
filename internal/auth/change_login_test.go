package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

const (
	changeEmail    = "treasurer@example.org"
	changePassword = "correct-horse-battery"
)

// registered is an Auth with the one account registered (real argon2id) and
// three sessions: "mine", the one making the change, and two others.
func registered(t *testing.T) (*Auth, store.User, *store.Queries) {
	t.Helper()
	a, sqlDB := newTestAuth(t)
	ctx := context.Background()
	user, err := a.Register(ctx, changeEmail, changePassword)
	if err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}
	q := store.New(sqlDB)
	for _, token := range []string{"mine", "other-1", "other-2"} {
		if _, err := q.CreateSession(ctx, store.CreateSessionParams{Token: token, Data: []byte("x"), ExpiresAt: time.Now().Unix() + 3600}); err != nil {
			t.Fatalf("CreateSession(%q) = %v", token, err)
		}
	}
	return a, user, q
}

func sessionAlive(t *testing.T, q *store.Queries, token string) bool {
	t.Helper()
	_, err := q.GetSession(context.Background(), store.GetSessionParams{Token: token, ExpiresAt: time.Now().Unix()})
	return err == nil
}

func wantOnlyMineAlive(t *testing.T, q *store.Queries) {
	t.Helper()
	if !sessionAlive(t, q, "mine") {
		t.Error("the session making the change was deleted")
	}
	for _, token := range []string{"other-1", "other-2"} {
		if sessionAlive(t, q, token) {
			t.Errorf("session %q survived the change", token)
		}
	}
}

func wantNoSessionTouched(t *testing.T, q *store.Queries) {
	t.Helper()
	for _, token := range []string{"mine", "other-1", "other-2"} {
		if !sessionAlive(t, q, token) {
			t.Errorf("session %q was deleted by a refused change", token)
		}
	}
}

func TestChangeEmailNormalisesSwitchesTheLoginAndKeepsOnlyTheCallersSession(t *testing.T) {
	t.Parallel()
	a, user, q := registered(t)
	ctx := context.Background()

	got, err := a.ChangeEmail(ctx, user.ID, changePassword, "  New@Example.ORG ", "mine")
	if err != nil {
		t.Fatalf("ChangeEmail() = %v, want no error", err)
	}
	if got.Email != "new@example.org" || got.ID != user.ID {
		t.Errorf("ChangeEmail() = id %d %q, want id %d %q", got.ID, got.Email, user.ID, "new@example.org")
	}
	if _, err := a.Authenticate(ctx, "new@example.org", changePassword); err != nil {
		t.Errorf("Authenticate(new email) = %v, want no error", err)
	}
	if _, err := a.Authenticate(ctx, changeEmail, changePassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(old email) = %v, want ErrInvalidCredentials", err)
	}
	if n, _ := q.CountUsers(ctx); n != 1 {
		t.Errorf("CountUsers() = %d, want 1", n)
	}
	wantOnlyMineAlive(t, q)
}

func TestChangePasswordSwitchesTheLoginAndKeepsOnlyTheCallersSession(t *testing.T) {
	t.Parallel()
	a, user, q := registered(t)
	ctx := context.Background()

	updated, err := a.ChangePassword(ctx, user.ID, changePassword, "a-brand-new-secret", "mine")
	if err != nil {
		t.Fatalf("ChangePassword() = %v, want no error", err)
	}
	// The returned row carries the new hash: it is what the caller re-binds the
	// session to.
	if updated.PasswordHash == "" || updated.PasswordHash == user.PasswordHash {
		t.Errorf("ChangePassword() returned hash %q, want the new one", updated.PasswordHash)
	}
	if _, err := a.Authenticate(ctx, changeEmail, "a-brand-new-secret"); err != nil {
		t.Errorf("Authenticate(new password) = %v, want no error", err)
	}
	if _, err := a.Authenticate(ctx, changeEmail, changePassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(old password) = %v, want ErrInvalidCredentials", err)
	}
	wantOnlyMineAlive(t, q)
}

func TestAWrongCurrentPasswordChangesNothing(t *testing.T) {
	t.Parallel()
	a, user, q := registered(t)
	ctx := context.Background()

	if _, err := a.ChangeEmail(ctx, user.ID, "not-the-password", "new@example.org", "mine"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("ChangeEmail(wrong password) = %v, want ErrWrongCurrentPassword", err)
	}
	if _, err := a.ChangePassword(ctx, user.ID, "not-the-password", "a-brand-new-secret", "mine"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("ChangePassword(wrong password) = %v, want ErrWrongCurrentPassword", err)
	}
	if _, err := a.ChangePassword(ctx, user.ID, "", "a-brand-new-secret", "mine"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("ChangePassword(empty current) = %v, want ErrWrongCurrentPassword", err)
	}
	if _, err := a.Authenticate(ctx, changeEmail, changePassword); err != nil {
		t.Errorf("Authenticate(original login) = %v, want it untouched", err)
	}
	wantNoSessionTouched(t, q)
}

// The shape checks come before the password is looked at: a malformed request
// is refused without a hash being spent and without being mistaken for a guess.
func TestShapeChecksRunBeforeAnyPasswordWork(t *testing.T) {
	t.Parallel()
	p := newProbe(8)
	a, insertUser := newProbedAuth(t, p)
	insertUser(changeEmail)
	ctx := context.Background()

	for _, email := range []string{"", "   ", "no-at-sign"} {
		if _, err := a.ChangeEmail(ctx, 1, "whatever", email, "mine"); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("ChangeEmail(%q) = %v, want ErrInvalidArgument", email, err)
		}
	}
	for _, password := range []string{"", "short", "1234567"} {
		if _, err := a.ChangePassword(ctx, 1, "whatever", password, "mine"); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("ChangePassword(%q) = %v, want ErrInvalidArgument", password, err)
		}
	}
	if v, h := p.verifies.Load(), p.hashes.Load(); v != 0 || h != 0 {
		t.Errorf("verifies = %d, hashes = %d, want no password work for a malformed request", v, h)
	}
}

// A busy hasher is neither a right nor a wrong password: nothing is changed and
// ErrBusy comes back for the handler to leave uncounted.
func TestChangeLoginAnswersErrBusyWhenNoSlotIsFree(t *testing.T) {
	t.Parallel()
	p := newProbe(8)
	p.park = make(chan struct{})
	a, insertUser := newProbedAuth(t, p, WithMaxConcurrentHashes(1), WithSlotWait(20*time.Millisecond))
	insertUser(changeEmail)
	ctx := context.Background()

	// Parks inside the one slot until the test ends.
	go func() { _, _ = a.verify(ctx, "x", "y") }()
	<-p.started
	t.Cleanup(func() { close(p.park) })

	if _, err := a.ChangeEmail(ctx, 1, "pw", "new@example.org", "mine"); !errors.Is(err, ErrBusy) {
		t.Errorf("ChangeEmail() with no free slot = %v, want ErrBusy", err)
	}
	if _, err := a.ChangePassword(ctx, 1, "pw", "a-brand-new-secret", "mine"); !errors.Is(err, ErrBusy) {
		t.Errorf("ChangePassword() with no free slot = %v, want ErrBusy", err)
	}
}

func TestChangeLoginForAnUnknownUserIsNotAWrongPassword(t *testing.T) {
	t.Parallel()
	a, _, _ := registered(t)
	ctx := context.Background()

	_, err := a.ChangeEmail(ctx, 9999, changePassword, "new@example.org", "mine")
	if err == nil || errors.Is(err, ErrWrongCurrentPassword) || errors.Is(err, ErrInvalidArgument) {
		t.Errorf("ChangeEmail(unknown id) = %v, want a plain error", err)
	}
}
