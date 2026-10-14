package http

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/kerti/uruni/internal/store"
)

// sessionIdleTimeout is the 30-day sliding timeout #113 built the schema for;
// TouchSession notes there is no absolute cap, so this is the only timeout
// that binds. scs's IdleTimeout marks every loaded session Modified on every
// request (scs's data.go), which is why sessionStore.CommitCtx upserts.
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
	sm.Store = newSessionStore(q)
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
	authenticated := a.sessionManager.Exists(r.Context(), sessionKeyUserID)

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
