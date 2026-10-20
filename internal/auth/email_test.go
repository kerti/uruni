package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// #500: an email is trimmed and lower-cased wherever stored, looked up or
// counted; rows written before keep their typed case and must still be found.

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"treasurer@example.org":       "treasurer@example.org",
		"  Treasurer@Example.ORG\t\n": "treasurer@example.org",
		"":                            "",
		"  ":                          "",
		"\u00dcnal@Example.org":       "\u00fcnal@example.org",
	} {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func storedEmails(t *testing.T, a *Auth) []string {
	t.Helper()
	users, err := a.q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers() = %v, want no error", err)
	}
	emails := make([]string, len(users))
	for i, u := range users {
		emails[i] = u.Email
	}
	return emails
}

func TestRegisterStoresTheNormalisedEmail(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()

	user, err := a.Register(ctx, "  Treasurer@Example.ORG ", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Register() = %v, want no error", err)
	}
	if user.Email != "treasurer@example.org" {
		t.Errorf("Register() email = %q, want the normalised form", user.Email)
	}
	if got := storedEmails(t, a); len(got) != 1 || got[0] != "treasurer@example.org" {
		t.Errorf("stored emails = %q, want [treasurer@example.org]", got)
	}
}

func TestAuthenticateIgnoresCaseAndPadding(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()
	if _, err := a.Register(ctx, "treasurer@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	for _, email := range []string{"treasurer@example.org", "Treasurer@Example.org", "TREASURER@EXAMPLE.ORG", "  treasurer@example.org\t"} {
		if _, err := a.Authenticate(ctx, email, "correct-horse-battery"); err != nil {
			t.Errorf("Authenticate(%q) = %v, want no error", email, err)
		}
	}
	if _, err := a.Authenticate(ctx, "Treasurer@Example.org", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(wrong password) = %v, want ErrInvalidCredentials", err)
	}
}

func TestSetLoginStoresTheNormalisedEmailAndResetsAcrossCase(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()

	created, err := a.SetLogin(ctx, " Treasurer@Example.org ", "correct-horse-battery")
	if err != nil || !created {
		t.Fatalf("SetLogin() = %v, %v, want created", created, err)
	}
	if got := storedEmails(t, a); len(got) != 1 || got[0] != "treasurer@example.org" {
		t.Errorf("stored emails = %q, want [treasurer@example.org]", got)
	}

	created, err = a.SetLogin(ctx, "TREASURER@example.org", "another-long-password")
	if err != nil || created {
		t.Fatalf("SetLogin(other case) = %v, %v, want a reset, not a second login", created, err)
	}
	if got := storedEmails(t, a); len(got) != 1 {
		t.Errorf("stored emails = %q, want one row", got)
	}
	if _, err := a.Authenticate(ctx, "treasurer@example.org", "another-long-password"); err != nil {
		t.Errorf("Authenticate(new password) = %v, want no error", err)
	}
}

// legacyRow writes a user the way an instance registered before #500 holds
// one: the email exactly as it was typed.
func legacyRow(t *testing.T, a *Auth, email, password string) {
	t.Helper()
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword() = %v", err)
	}
	if _, err := a.q.CreateUser(context.Background(), store.CreateUserParams{Email: email, PasswordHash: hash, CreatedAt: 1}); err != nil {
		t.Fatalf("CreateUser(%q) = %v", email, err)
	}
}

// TestAMixedCaseRowFromBeforeStillLogsIn: the row keeps its case (nothing
// rewrites it) and every spelling finds it.
func TestAMixedCaseRowFromBeforeStillLogsIn(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()
	legacyRow(t, a, "Bendahara.RT05@Example.org", "correct-horse-battery")

	for _, email := range []string{"Bendahara.RT05@Example.org", "bendahara.rt05@example.org", "BENDAHARA.RT05@EXAMPLE.ORG", " bendahara.rt05@example.org "} {
		user, err := a.Authenticate(ctx, email, "correct-horse-battery")
		if err != nil {
			t.Errorf("Authenticate(%q) = %v, want no error", email, err)
			continue
		}
		if user.Email != "Bendahara.RT05@Example.org" {
			t.Errorf("Authenticate(%q) row email = %q, want it left as stored", email, user.Email)
		}
	}
}

// TestCreateUserFindsAMixedCaseRowFromBefore: create-user with the lower-case
// address resets that login instead of refusing it as "another account", and
// leaves the stored address as it was.
func TestCreateUserFindsAMixedCaseRowFromBefore(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()
	legacyRow(t, a, "Bendahara.RT05@Example.org", "forgotten-password")

	created, err := a.SetLogin(ctx, "bendahara.rt05@example.org", "correct-horse-battery")
	if err != nil {
		t.Fatalf("SetLogin() = %v, want a reset of the existing login", err)
	}
	if created {
		t.Error("SetLogin() created a row, want a reset")
	}
	if got := storedEmails(t, a); len(got) != 1 || got[0] != "Bendahara.RT05@Example.org" {
		t.Errorf("stored emails = %q, want the one legacy row untouched", got)
	}
	if _, err := a.Authenticate(ctx, "Bendahara.RT05@Example.org", "correct-horse-battery"); err != nil {
		t.Errorf("Authenticate(new password) = %v, want no error", err)
	}
	if _, err := a.SetLogin(ctx, "someone-else@example.org", "correct-horse-battery"); !errors.Is(err, ErrOtherAccountExists) {
		t.Errorf("SetLogin(different address) = %v, want ErrOtherAccountExists", err)
	}
}

// TestANonASCIICapitalFromBeforeIsFoundOnlyByItsStoredSpelling pins the one case
// the case-insensitive match cannot fully reach. "user".email is COLLATE NOCASE
// (#623), which folds ASCII only, so for a legacy row with a non-ASCII capital
// the Go-normalised form (full Unicode lower-casing) never equals the stored
// text. The row is found by the spelling of its non-ASCII letters as registered
// (its ASCII letters fold, so the domain's case does not matter), and not by a
// lower-cased U-umlaut. Such an address is very unlikely, and the treasurer's way
// out is create-user with the stored spelling.
func TestANonASCIICapitalFromBeforeIsFoundOnlyByItsStoredSpelling(t *testing.T) {
	t.Parallel()
	a, _ := newTestAuth(t)
	ctx := context.Background()
	legacyRow(t, a, "\u00dcnal@Example.org", "correct-horse-battery")

	for _, email := range []string{" \u00dcnal@Example.org ", "\u00dcnal@example.org"} {
		if _, err := a.Authenticate(ctx, email, "correct-horse-battery"); err != nil {
			t.Errorf("Authenticate(%q) = %v, want no error", email, err)
		}
	}
	if _, err := a.Authenticate(ctx, "\u00fcnal@example.org", "correct-horse-battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(lower-cased non-ASCII letter) = %v, want ErrInvalidCredentials", err)
	}
}
