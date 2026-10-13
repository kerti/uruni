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
// (#115). Never wrap a more specific cause into it or return anything else on
// a failed login: a second error value would be the account-existence oracle
// this exists to prevent, since mapAuthError (internal/http/errors.go) turns
// each error into its own response.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// dummyPasswordHash is a decoy argon2id hash naming no real account.
// Authenticate verifies against it on the "no such email" path so that arm
// spends the same argon2id computation as a real check (see Authenticate).
// Computed once at init under password.go's real cost parameters, so the
// decoy is not measurably cheaper or dearer than a genuine comparison.
var dummyPasswordHash string

func init() {
	hash, err := hashPassword("uruni-timing-decoy-not-a-real-account-password")
	if err != nil {
		// hashPassword fails only if crypto/rand.Read fails, which this package
		// cannot recover from or usefully defer: every login needs this decoy before
		// verifying anything.
		panic(fmt.Sprintf("auth: computing the timing-decoy hash: %v", err))
	}
	dummyPasswordHash = hash
}

// Authenticate verifies email and password against the account
// GetUserByEmail(email) names (trimmed, matched case-insensitively), returning
// that row on success.
//
// It answers ErrInvalidCredentials - one sentinel, no other error naming a
// login failure - whether email matches no account or matches one whose
// password does not verify. The cases must be indistinguishable: mapAuthError
// turns this into POST /api/login's 401, and a response varying by cause (a
// code, a body, or even the amount of work before answering) would let a
// caller learn which addresses have an account here. #116's public
// has_account bit deliberately does not answer that, being a bit about
// whether any account exists, not which address.
//
// The "no such email" arm still calls verifyPassword, against
// dummyPasswordHash, so it spends the same argon2id computation as the
// "wrong password" arm; argon2id is deliberately slow (password.go), so
// skipping it is the gap a timing attack measures. Every path calls
// verifyPassword exactly once before a failure return; none returns between
// "we know which case this is" and "the hash comparison has run".
//
// The one other answer is ErrBusy (password_gate.go): the comparison was
// refused a slot, so nothing was checked. It is returned for a known and an
// unknown email alike, so it is no more an oracle than the 401, and the caller
// must not count it as a wrong guess.
func (a *Auth) Authenticate(ctx context.Context, email, password string) (store.User, error) {
	// Looked up by the normalised form, case-insensitively (#500): rows written
	// before normalisation keep their typed case, which the exact arm finds.
	user, err := a.q.GetUserByEmail(ctx, store.GetUserByEmailParams{
		Normalized: NormalizeEmail(email),
		Exact:      strings.TrimSpace(email),
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return store.User{}, fmt.Errorf("looking up account: %w", err)
		}

		if _, verr := a.verify(ctx, password, dummyPasswordHash); verr != nil {
			// dummyPasswordHash is this package's well-formed output; a decoy comparison
			// failing at all means something is broken, not that the attempt was bad.
			return store.User{}, fmt.Errorf("comparing against the timing decoy: %w", verr)
		}
		return store.User{}, ErrInvalidCredentials
	}

	ok, err := a.verify(ctx, password, user.PasswordHash)
	if err != nil {
		// A stored hash that doesn't parse is a corrupted row (see verifyPassword,
		// ErrMalformedHash), not a wrong password: a plain error reaching
		// mapAuthError's default (500), not the 401 a bad guess gets.
		return store.User{}, fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return store.User{}, ErrInvalidCredentials
	}
	return user, nil
}
