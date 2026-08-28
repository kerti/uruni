package http

import (
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
func newSessionManager(q store.Querier, baseURL string) *scs.SessionManager {
	sm := scs.New()
	sm.Store = newSessionStore(q)
	sm.Lifetime = sessionLifetimeCap
	sm.IdleTimeout = sessionIdleTimeout
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secureFromBaseURL(baseURL)
	return sm
}

func secureFromBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && u.Scheme == "https"
}
