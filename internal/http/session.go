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

// sessionIdleTimeout is the 30-day sliding timeout #113 built the schema
// for - TouchSession's own doc comment says "there is no absolute cap to
// enforce here," so this is the only timeout that actually binds. Setting
// scs's IdleTimeout is what makes every loaded session Modified on every
// request (scs's data.go), which in turn is why sessionStore.CommitCtx has
// to upsert rather than insert - see its own comment.
const sessionIdleTimeout = 30 * 24 * time.Hour

// sessionLifetimeCap is scs's absolute session deadline, computed once at
// session creation and never moved forward afterward - unlike the idle
// timeout above, which is recomputed from "now" on every request. It has to
// be a real, finite duration for scs's own zero-value checks, but is set
// far longer than any idle timeout could ever coast to (a century) so it
// never becomes the binding minimum in min(deadline, idle-expiry). Without
// this margin, an absolute cap would silently reappear the moment
// deadline first falls behind a still-active session's idle-refreshed
// expiry - exactly the "no absolute cap" ADR-030/#113 rule this constant
// exists to honour.
const sessionLifetimeCap = 100 * 365 * 24 * time.Hour

// sessionKeyUserID is the one thing a session carries. ADR-030 decision 2:
// "a session proves the treasurer, not a fund" - there is no fund key here,
// on purpose, and there never will be under that ADR.
const sessionKeyUserID = "user_id"

// newSessionManager wires github.com/alexedwards/scs/v2's LoadAndSave over
// sessionStore (session_store.go) - our own store over #113's session
// table, never scs's bundled sqlite3store (ADR-005).
//
// Secure is derived from baseURL's scheme rather than a new environment
// variable - ADR-019's runtime-config table stays closed, per issue #114.
// https means the operator has put a real TLS origin in front (Caddy,
// ADR-009); http or an unset baseURL means local dev on plain-HTTP
// loopback, where a Secure cookie is simply never sent by the browser at
// all - getting this backwards would make `make web-dev` silently never
// receive the session cookie, with no error anywhere to explain why login
// looks broken.
func newSessionManager(q store.Querier, baseURL string, logger *slog.Logger) *scs.SessionManager {
	sm := scs.New()
	sm.Store = newSessionStore(q)
	sm.Lifetime = sessionLifetimeCap
	sm.IdleTimeout = sessionIdleTimeout
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secureFromBaseURL(baseURL)

	// scs's own default ErrorFunc answers a session-store failure - the
	// store being unreadable, not "no session found" - with a plain-text
	// "Internal Server Error" via the stdlib's http.Error, bypassing
	// writeAPIError entirely. Before #116 that path was effectively
	// unreachable in practice: LoadAndSave only touches the store at all
	// when a request carries a token (scs's own Load, data.go), and every
	// route a caller could reach without one first. Gating the surface
	// changed that - reaching any of the newly-gated routes requires a
	// session cookie, so a store outage is now hit here, ahead of every
	// handler's own error mapping, for ordinary authenticated traffic. This
	// is what keeps that failure inside the one JSON envelope api.go's own
	// NotFound/MethodNotAllowed comment already promises for the rest of
	// /api, rather than a second, foreign error shape a client would have
	// to learn to parse.
	sm.ErrorFunc = func(w http.ResponseWriter, _ *http.Request, err error) {
		logger.Error("session store failure", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}

	return sm
}

func secureFromBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && u.Scheme == "https"
}

// sessionStatusResponse is GET /api/session's entire body (#116) - and
// deliberately nothing more. Fund and setup state stay out on purpose: an
// authenticated client gets those from GET /api/fund for free, and putting
// them here would reopen the same "how much can an unauthenticated caller
// learn" question ADR-030 just closed by moving every other route behind
// the gate.
type sessionStatusResponse struct {
	Authenticated bool `json:"authenticated"`
	HasAccount    bool `json:"has_account"`
}

// getSession is GET /api/session, the other route that never answers 401 -
// see sessionRequired's own comment for why. It is the one read a booting,
// logged-out client can make at all: session cookies are httpOnly, so
// there is no other way for the SPA to tell whether to render register or
// login before the treasurer has done either.
//
// has_account is a chosen existence oracle, not an oversight - the same bit
// POST /api/register's 409 already leaks the moment a second registration
// is attempted, so exposing it here on a plain GET costs nothing that
// wasn't already discoverable, and buys the SPA the one thing it cannot
// derive from authenticated alone: whether to offer "register" or "log in"
// on the screen a fresh visitor lands on.
func (a *api) getSession(w http.ResponseWriter, r *http.Request) {
	authenticated := a.sessionManager.Exists(r.Context(), sessionKeyUserID)

	count, err := a.queries.CountUsers(r.Context())
	if err != nil {
		a.logger.Error("counting users for GET /api/session", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	writeJSON(w, http.StatusOK, sessionStatusResponse{
		Authenticated: authenticated,
		HasAccount:    count > 0,
	})
}
