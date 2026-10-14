package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/store"
)

// The treasurer's own login (#386, ADR-007/ADR-030): GET /api/user reads it,
// POST /api/user/email and POST /api/user/password edit it. Still the instance's
// one login: "user" is the table and the SPA's AuthUser (CONTEXT.md keeps it
// apart from an account, a place money sits, which is /api/accounts).
//
// Both writes ask for the CURRENT password and share their rate-limit keys, so
// guessing it through email changes and through password changes spends one
// budget. The budget is the login limiter's (restore confirm borrows it the
// same way), under "user-ip:" and "user:" prefixes of its own so it neither
// spends nor refills login's or restore's. Two keys, as login has two kinds: the
// client network (10 an interval, escalating) and, per user, the looser flat
// ceiling (50 an hour) that a guesser rotating addresses cannot walk around. The
// caller already holds a session, so the threat is a stolen cookie guessing the
// password on the way to taking the login over.
//
// Either write signs out every other session, keeps this one, and re-binds it to
// the new credential (credentialFingerprint, session.go).

// changeEmailRequest is POST /api/user/email's body.
type changeEmailRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
}

// changePasswordRequest is POST /api/user/password's body. The two-field typed
// confirmation is the browser's; the server takes the one new password.
type changePasswordRequest struct {
	NewPassword     string `json:"new_password"`
	CurrentPassword string `json:"current_password"`
}

// getUser is GET /api/user: the signed-in login, for Pengaturan's card. It is
// behind the session gate, so the address is shown to the treasurer and to no
// one else; GET /api/session stays free of it (ADR-030).
func (a *api) getUser(w http.ResponseWriter, r *http.Request) {
	user, err := a.queries.GetUserByID(r.Context(), a.sessionManager.GetInt64(r.Context(), sessionKeyUserID))
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// changeEmail is POST /api/user/email: 200 and the updated user, or the errors
// credentialChange maps.
func (a *api) changeEmail(w http.ResponseWriter, r *http.Request) {
	var req changeEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, ok := a.credentialChange(w, r, "email", func(userID int64, keep string) (store.User, error) {
		return a.auth.ChangeEmail(r.Context(), userID, req.CurrentPassword, req.Email, keep)
	})
	if ok {
		writeJSON(w, http.StatusOK, toUserResponse(user))
	}
}

// changePassword is POST /api/user/password: 204, or the errors
// credentialChange maps.
func (a *api) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, ok := a.credentialChange(w, r, "password", func(userID int64, keep string) (store.User, error) {
		return a.auth.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword, keep)
	}); ok {
		w.WriteHeader(http.StatusNoContent)
	}
}

// credentialChange is what the two writes share: the rate limit, the session
// they keep, and the security events. change runs the auth call and returns the
// updated row. On a failure this answers and returns false; on success it
// returns true and the caller writes the response, after the session has been
// updated here (scs commits the session when the first byte is written).
//
// The attempt is reserved before the password is checked, as in login.go
// (#495), so concurrent guesses are counted in flight. The reservation stands
// for a wrong current password (a real guess) and is handed back for anything
// that was not one: a malformed new email or short new password (refused before
// the password is looked at), a busy hasher, a broken database. A success clears
// the network key, as a restore confirm does, and not the per-user ceiling, as
// login leaves its own.
//
// On success the session is re-bound to the new credential and given a fresh
// token. The old token's row is deleted by scs on renewal, and the other
// sessions' rows were deleted with the change; any of those that a request in
// flight writes back later carries the old fingerprint and is refused by
// signedIn.
func (a *api) credentialChange(w http.ResponseWriter, r *http.Request, what string, change func(userID int64, keepSession string) (store.User, error)) (store.User, bool) {
	// The session this request rides: the one that survives the change. Loaded
	// from the store by sessionRequired, so its token is never empty here; an
	// empty keep would sign out this session too.
	ctx := r.Context()
	userID := a.sessionManager.GetInt64(ctx, sessionKeyUserID)
	keep := a.sessionManager.Token(ctx)
	if userID == 0 || keep == "" {
		a.log(r).Error("credential change without a session identity", "what", what)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return store.User{}, false
	}

	network := "user-ip:" + a.clientNetwork(r)
	reservation, ok := a.loginLimiter.reserveRules(
		keyRule{key: network, rule: a.loginLimiter.rule},
		keyRule{key: userKey(userID), rule: limitRule{limit: loginIdentifierCeiling, window: loginIdentifierCeilingWindow}},
	)
	if !ok {
		a.securityEvent(r, slog.LevelWarn, what+"_change_refused", "reason", "locked_out")
		writeAPIError(w, http.StatusTooManyRequests, "too_many_requests", "Too many attempts. Try again later.")
		return store.User{}, false
	}

	user, err := change(userID, keep)
	if err != nil {
		if errors.Is(err, auth.ErrWrongCurrentPassword) {
			a.securityEvent(r, slog.LevelWarn, what+"_change_refused", "reason", "wrong_password")
		} else {
			reservation.release()
		}
		mapAuthError(w, a.log(r), err)
		return store.User{}, false
	}
	a.loginLimiter.reset(network)

	// The change has committed; what follows is this session's bookkeeping, and
	// its failure is not a reason to report the change as failed. Putting the new
	// fingerprint is memory only. A failed renewal keeps the old token, still
	// valid under the new fingerprint.
	a.sessionManager.Put(ctx, sessionKeyCredential, credentialFingerprint(user))
	if err := a.sessionManager.RenewToken(ctx); err != nil {
		a.log(r).Error("renewing the session token after a credential change", "error", err)
	}
	a.securityEvent(r, slog.LevelInfo, what+"_changed")
	return user, true
}

// userKey is the per-user ceiling's limiter key (user_id, not the email, which
// this very request may change).
func userKey(id int64) string {
	return "user:" + strconv.FormatInt(id, 10)
}
