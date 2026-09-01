package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kerti/uruni/internal/store"
)

// ErrInvalidCredentials is Authenticate's one failure sentinel, returned
// identically whether email names no account or the password does not match
// the account it names (issue #115). Never wrap a more specific cause into
// it and never return anything else on a failed login - a second error
// value here would be exactly the account-existence oracle this type
// exists to prevent, since mapAuthError (internal/http/errors.go) turns
// each error into its own response.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// dummyPasswordHash is a decoy argon2id hash that names no real account.
// Authenticate verifies against it on the "no such email" path so that arm
// spends the same argon2id computation a real password check would - see
// Authenticate's own comment for why that matters. Computed once at package
// init, under the same cost parameters password.go's real hashes use, so
// the decoy comparison is not measurably cheaper or more expensive than a
// genuine one.
var dummyPasswordHash string

func init() {
	hash, err := hashPassword("uruni-timing-decoy-not-a-real-account-password")
	if err != nil {
		// hashPassword's only failure mode is crypto/rand.Read failing,
		// which is not a condition this package can recover from or
		// usefully defer - every login would need this decoy hash before
		// verifying anything.
		panic(fmt.Sprintf("auth: computing the timing-decoy hash: %v", err))
	}
	dummyPasswordHash = hash
}

// Authenticate verifies email and password against the account
// GetUserByEmail(email) names, returning that row on success.
//
// It answers ErrInvalidCredentials - one sentinel, no other error naming a
// login failure - whether email matches no account at all or matches one
// whose password does not verify. The two cases must be indistinguishable
// to the caller: mapAuthError turns this into POST /api/login's 401, and a
// response that varied by cause (a different code, a different body, or
// even just a different amount of work done before answering) would let a
// caller learn which email addresses have an account on this instance
// simply by trying them - "which address is it", the thing #116's public
// has_account bit deliberately does not answer either, because unlike that
// bit this would name one specific address, not just whether any account
// exists.
//
// The "no such email" arm still calls verifyPassword, against
// dummyPasswordHash rather than skipping straight to ErrInvalidCredentials,
// so it spends the same argon2id computation as the "wrong password" arm -
// argon2id is deliberately slow (password.go), so skipping that work is
// exactly the kind of gap a timing attack measures. Every path through this
// function calls verifyPassword exactly once before it can return a
// failure; there is no return between "we know which case this is" and
// "the hash comparison has run".
func (a *Auth) Authenticate(ctx context.Context, email, password string) (store.User, error) {
	email = strings.TrimSpace(email)

	user, err := a.q.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return store.User{}, fmt.Errorf("looking up account: %w", err)
		}

		if _, verr := verifyPassword(password, dummyPasswordHash); verr != nil {
			// dummyPasswordHash is this package's own well-formed output;
			// a decoy comparison failing at all means something is
			// actually broken, not that the login attempt was bad.
			return store.User{}, fmt.Errorf("comparing against the timing decoy: %w", verr)
		}
		return store.User{}, ErrInvalidCredentials
	}

	ok, err := verifyPassword(password, user.PasswordHash)
	if err != nil {
		// A stored hash that doesn't parse is a corrupted row (see
		// verifyPassword's own doc comment on ErrMalformedHash), not a
		// wrong password - surfaced as a plain error so it reaches
		// mapAuthError's default case (500), not the 401 a bad guess gets.
		return store.User{}, fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return store.User{}, ErrInvalidCredentials
	}
	return user, nil
}
