package auth

import "errors"

// ErrInvalidArgument mirrors internal/ledger's category (ADR-027): the
// caller's input failed a shape check before reaching the database - an empty
// or malformed email, a password under MinPasswordLength. Every returned error
// wraps this with %w, so callers use errors.Is, not string matching.
var ErrInvalidArgument = errors.New("auth: invalid argument")

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
