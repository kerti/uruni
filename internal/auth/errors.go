package auth

import "errors"

// ErrInvalidArgument mirrors internal/ledger's own category (ADR-027): the
// caller's own input failed a shape check before anything reached the
// database - an empty or malformed email, a password under
// MinPasswordLength. Every returned error wraps this with %w, so a caller
// branches with errors.Is(err, ErrInvalidArgument) rather than matching a
// string.
var ErrInvalidArgument = errors.New("auth: invalid argument")

// ErrAlreadyRegistered is returned by Register the moment CountUsers finds
// any existing row - ADR-030 decision 2's one-shot bootstrap gate. Keyed to
// the count, not to a duplicate email: see Register's own doc comment for
// why a uniqueness collision alone is not the guarantee this needs.
var ErrAlreadyRegistered = errors.New("auth: an account has already been registered")
