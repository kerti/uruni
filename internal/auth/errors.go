package auth

import (
	"errors"
	"fmt"
)

// ErrInvalidArgument mirrors internal/ledger's category (ADR-027): the
// caller's input failed a shape check before reaching the database - an empty
// or malformed email, a password under MinPasswordLength. Every returned error
// wraps this with %w, so callers use errors.Is, not string matching.
var ErrInvalidArgument = errors.New("auth: invalid argument")

// FieldError is internal/ledger's FieldError for this package (#530): an
// ErrInvalidArgument naming the JSON key at fault, which internal/http reads
// through InvalidField. Its text is unchanged from the %w form.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return ErrInvalidArgument.Error() + ": " + e.Reason }

func (e *FieldError) Unwrap() error { return ErrInvalidArgument }

// InvalidField is what internal/http reads the field through.
func (e *FieldError) InvalidField() string { return e.Field }

// ErrPasswordTooCommon is returned wherever a new password enters (Register,
// SetLogin, the account change) when it is on the embedded common-passwords
// list (#528). It wraps ErrInvalidArgument, so a caller that only cares that the
// input was refused still matches that; mapAuthError checks this one first to
// give it its own wire code.
var ErrPasswordTooCommon = fmt.Errorf("%w: password is too common", ErrInvalidArgument)

// ErrAlreadyRegistered is returned by Register when CountUsers finds any
// existing row: ADR-030 decision 2's one-shot bootstrap gate. Keyed to the
// count, not a duplicate email (see Register for why).
var ErrAlreadyRegistered = errors.New("auth: an account has already been registered")

// ErrOtherAccountExists is returned by SetLogin when the instance already has a
// login under a different email. The instance has one login (ADR-030 decision
// 2), so SetLogin resets it or creates the first, never a second.
var ErrOtherAccountExists = errors.New("auth: this instance already has a login under a different email")

// ErrBusy is returned by anything that hashes or verifies a password when every
// slot of the process-wide gate (password_gate.go) stayed taken for the whole
// wait. It says nothing about the caller or password: nothing was computed, so
// a caller counting attempts must not count this one.
var ErrBusy = errors.New("auth: password hashing is busy")

// ErrWrongCurrentPassword is returned by ChangeEmail and ChangePassword when
// the current password they were given does not verify. Its own sentinel, not
// ErrInvalidCredentials: that one is Authenticate's single no-oracle answer for
// a login, and mapAuthError turns it into the login's 401. Here the caller
// already holds a session, so naming the failure leaks nothing, and the SPA
// has its own sentence for it.
var ErrWrongCurrentPassword = errors.New("auth: wrong current password")
