package http

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/store"
)

// sessionIdleTimeout is the 30-day sliding timeout #113 built the schema for;
// TouchSession notes there is no absolute cap, so this is the only timeout
// that binds. scs's IdleTimeout marks every loaded session Modified on every
// request (scs's data.go), which is why sessionStore.CommitCtx must refuse to
// re-create a row that was deleted after the request loaded it.
const sessionIdleTimeout = 30 * 24 * time.Hour

// sessionLifetimeCap is scs's absolute session deadline, computed once at
// creation and never moved, unlike the idle timeout recomputed each request.
// It must be a finite duration for scs's zero-value checks but is a century,
// so it never becomes the binding minimum in min(deadline, idle-expiry).
// Otherwise an absolute cap would reappear once deadline falls behind a
// still-active session's idle-refreshed expiry, breaking ADR-030/#113's
// "no absolute cap".
const sessionLifetimeCap = 100 * 365 * 24 * time.Hour

// sessionKeyUserID is the one thing a session carries. ADR-030 decision 2: "a
// session proves the treasurer, not a fund", so there is no fund key.
const sessionKeyUserID = "user_id"

// sessionKeyCredential is the other thing a session carries: a fingerprint of
// the login it was issued for (credentialFingerprint), checked on every gated
// request by signedIn.
const sessionKeyCredential = "credential"

// credentialFingerprint is what binds a session to the login behind it: a hex
// SHA-256 over the stored password hash and the normalised email. The hash
// carries its own salt, so any password change, even to the same text, gives a
// new one; so does an email change. It is stored in the session, never sent, and
// never a secret worth more than the hash it was made from.
//
// Why a session needs it (#386): with IdleTimeout set, scs marks every loaded
// session Modified, so every authenticated request ends by committing its
// session (the store writes an unchanged one back at most hourly, #529).
// Deleting rows revokes the other sessions, and sessionStore.CommitCtx (#601)
// does not put a deleted one back. This is the second line: it does not
// depend on the row being gone, so a session that did come back (an older build,
// a restored database file) carries the old fingerprint, and the next request
// finds the mismatch and destroys it. It covers credential changes only; a
// restore keeps the live login (ADR-012), so its fingerprints do not change and
// the store's rule is what keeps its sessions signed out.
func credentialFingerprint(u store.User) string {
	sum := sha256.Sum256([]byte(u.PasswordHash + "\x00" + auth.NormalizeEmail(u.Email)))
	return hex.EncodeToString(sum[:])
}

// signedIn reports whether the request's session proves the treasurer: it names
// a user who still exists, and carries that user's current credentialFingerprint.
// A session that does not (a password or email changed since, an unknown user,
// or one issued before the fingerprint existed, which has none) is destroyed, so
// the row is gone and scs does not write it back at the end of the request. No
// session at all is simply false. Strict on purpose: a session without a
// fingerprint is never stamped on the fly, since a resurrected one would be
// stamped with the credential it was meant to lose. An error is a store failure.
func (a *api) signedIn(r *http.Request) (bool, error) {
	ctx := r.Context()
	if !a.sessionManager.Exists(ctx, sessionKeyUserID) {
		return false, nil
	}
	user, err := a.queries.GetUserByID(ctx, a.sessionManager.GetInt64(ctx, sessionKeyUserID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		have := []byte(a.sessionManager.GetString(ctx, sessionKeyCredential))
		if subtle.ConstantTimeCompare(have, []byte(credentialFingerprint(user))) == 1 {
			return true, nil
		}
	}
	if err := a.sessionManager.Destroy(ctx); err != nil {
		// Still refused: a session that fails the check proves nothing, destroyed or not.
		a.log(r).Error("destroying a session whose credential no longer matches", "error", err)
	}
	return false, nil
}

// newSessionManager wires scs/v2's LoadAndSave over sessionStore
// (session_store.go), our store over #113's session table, never scs's
// sqlite3store (ADR-005).
//
// Secure is derived from baseURL's scheme, not a new environment variable
// (ADR-019's table stays closed). https means a real TLS origin in front
// (Caddy, ADR-009); http or unset means plain-HTTP loopback in local dev,
// where the browser never sends a Secure cookie. Getting this backwards would
// make `make web-dev` silently never receive the session cookie, with nothing
// to explain why login looks broken.
func newSessionManager(q store.Querier, baseURL string, logger *slog.Logger) *scs.SessionManager {
	sm := scs.New()
	sm.Store = newSessionStore(q, sm.Codec)
	sm.Lifetime = sessionLifetimeCap
	sm.IdleTimeout = sessionIdleTimeout
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secureFromBaseURL(baseURL)

	// scs's default ErrorFunc answers a session-store failure (store unreadable,
	// not "no session found") with plain-text "Internal Server Error" via
	// http.Error, bypassing writeAPIError. Before #116 that was practically
	// unreachable: LoadAndSave touches the store only when a request carries a
	// token, and every reachable route had none. Gating the surface changed
	// that: a store outage is now hit here, ahead of handler error mapping, for
	// ordinary authenticated traffic. This keeps the failure inside the one JSON
	// envelope api.go promises for /api.
	sm.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) {
		loggerFrom(r.Context(), logger).Error("session store failure", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}

	return sm
}

func secureFromBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && u.Scheme == "https"
}

// sessionStatusResponse is GET /api/session's entire body (#116), nothing
// more. Fund and setup state stay out: an authenticated client gets them from
// GET /api/fund, and putting them here would reopen the "how much can an
// unauthenticated caller learn" question ADR-030 closed.
type sessionStatusResponse struct {
	Authenticated bool `json:"authenticated"`
	HasAccount    bool `json:"has_account"`
}

// getSession is GET /api/session, the other route that never answers 401 (see
// sessionRequired). It is the one read a booting, logged-out client can make:
// session cookies are httpOnly, so the SPA has no other way to tell whether
// to render register or login.
//
// has_account is a chosen existence oracle: POST /api/register's 409 already
// leaks the same bit on a second registration, so exposing it on a GET
// costs nothing new, and gives the SPA the one thing it cannot derive from
// authenticated alone.
func (a *api) getSession(w http.ResponseWriter, r *http.Request) {
	// signedIn, not Exists: a session revoked by a credential change must read as
	// logged out here too, or the SPA boots into screens that all answer 401.
	authenticated, err := a.signedIn(r)
	if err != nil {
		a.log(r).Error("checking the session for GET /api/session", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	count, err := a.queries.CountUsers(r.Context())
	if err != nil {
		a.log(r).Error("counting users for GET /api/session", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	writeJSON(w, http.StatusOK, sessionStatusResponse{
		Authenticated: authenticated,
		HasAccount:    count > 0,
	})
}
