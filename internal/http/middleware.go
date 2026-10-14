package http

import (
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// sessionRequired is the gate #116 adds: every /api route but the four public
// ones (register, login, session, logout) sits behind it. It is an *api
// method because it reaches a.sessionManager, like every handler here.
//
// It checks Exists(sessionKeyUserID), not merely "is there a session":
// LoadAndSave (api.go) attaches a session to every /api request, cookie or
// not, so an empty fresh session and an authenticated one are both "a
// session" and only the stored key tells them apart.
//
// It also requires the session's credential fingerprint to match the login as
// it is now (signedIn, session.go), so a password or email change ends every
// session issued before it, including one a slow request writes back afterwards.
// That is one lookup of the one user row per request.
//
// It injects no context value. ADR-030 decision 2: a session proves "you are
// the treasurer", nothing more; with one treasurer account per v1 instance,
// no handler needs to know which identity passed. That also keeps this from
// touching resolveFund or its call sites.
func (a *api) sessionRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, err := a.signedIn(r)
		if err != nil {
			a.log(r).Error("checking the session against the login", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
			return
		}
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "You must be logged in to do that.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// How long a route that moves a file may take, end to end. The server's own
// ReadTimeout and WriteTimeout (cmd/uruni/main.go) are short, to bound a slow
// client on the ordinary routes; these two are set per request on the routes
// that carry a file, by extendDeadlines.
//
//   - receiptTransferTimeout matches the SPA's UPLOAD_TIMEOUT_MS (web/src/lib/api.ts,
//     120s): the client waits that long for a receipt photo (up to 10 MB) on a
//     poor phone connection, so the server must not give up sooner.
//   - backupTransferTimeout covers a backup download (the zip is held whole in
//     memory) and the restore routes. It is the server's ceiling, not what a
//     phone gets: the SPA gives up on any file upload at UPLOAD_TIMEOUT_MS. The
//     server stays out of the way up to the largest restore it accepts (256 MB,
//     backup.MaxUploadBytes, is 15 minutes at about 2.3 Mbit/s), including the
//     inspect and confirm work after the body arrives. Absolute, not per byte:
//     it bounds one authenticated treasurer's transfer, not a crowd.
const (
	receiptTransferTimeout = 2 * time.Minute
	backupTransferTimeout  = 15 * time.Minute
)

// extendDeadlines gives the routes behind it d to read the request and write
// the response, replacing the server-wide deadlines for this request alone.
// Both are set so a route is one line, whichever way it moves its bytes: the
// read deadline is what an upload runs into, the write deadline what a download
// (or the response after a long restore step) runs into.
//
// http.NewResponseController finds the connection by following Unwrap through
// every wrapper above it (requestLogger's chi wrapper, scs's session writer);
// a wrapper without Unwrap would make the call fail with http.ErrNotSupported.
// That is logged rather than swallowed, because the symptom (a download cut off
// at the server-wide WriteTimeout) shows far from the cause.
func extendDeadlines(logger *slog.Logger, d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)
			deadline := time.Now().Add(d)
			if err := rc.SetReadDeadline(deadline); err != nil {
				loggerFrom(r.Context(), logger).Warn("could not extend the read deadline", "error", err)
			}
			if err := rc.SetWriteDeadline(deadline); err != nil {
				loggerFrom(r.Context(), logger).Warn("could not extend the write deadline", "error", err)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger logs one line per request: method, path, status and duration,
// what ADR-022 promised as M4 middleware. It logs through the request-scoped
// logger (requestID), so the line carries the request_id its handlers' lines
// carry. No client address here: that appears on security events only.
//
// r.URL.Path only, never RawQuery or the body: a query string or body is
// where a member name, a note or an amount would end up, and ADR-022 forbids
// logging those.
//
// The path is redacted where it carries a secret (#447): the report's slug is
// the only thing guarding it (ADR-035) and logs travel, so /report/<slug> and
// its sub-paths log as /report/:slug (see logPath).
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// chi's wrapper makes the status readable afterward; a bare ResponseWriter
			// never exposes what WriteHeader was called with.
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			// /healthz is polled every 30s by the container healthcheck: at info it
			// would be nearly every line in the log. A failing probe still shows,
			// healthz logs its own error.
			level := slog.LevelInfo
			if r.URL.Path == "/healthz" {
				level = slog.LevelDebug
			}
			loggerFrom(r.Context(), logger).Log(r.Context(), level, "request",
				"method", r.Method,
				"path", logPath(r.URL.Path),
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// untrustedForwardedFor warns, once per process, that a private-range peer sent
// X-Forwarded-For while no proxy is trusted (URUNI_TRUSTED_PROXIES empty).
// That is the signature of a reverse proxy the operator has not told the app
// about: the header is ignored, so every client shares the proxy's address in
// the login, register and restore limiters, and one person's failed attempts
// lock everyone out. Router adds it only when the list is empty.
//
// Private or loopback, not any peer: a public peer sending the header is a
// client or a hostile one, not a proxy, and warning on it would let a stranger
// write the operator's log. Neither the header's value nor the peer's address
// is logged (ADR-022).
func untrustedForwardedFor(logger *slog.Logger) func(http.Handler) http.Handler {
	var once sync.Once
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.Header.Values("X-Forwarded-For")) > 0 && privatePeer(remoteAddr(r)) {
				once.Do(func() {
					loggerFrom(r.Context(), logger).Warn("a private-range peer sent X-Forwarded-For but URUNI_TRUSTED_PROXIES is empty: the header is ignored, so behind a reverse proxy every client shares the proxy's address for rate limiting; set URUNI_TRUSTED_PROXIES to the proxy's address")
				})
			}
			next.ServeHTTP(w, r)
		})
	}
}

// privatePeer is RFC 1918 / ULA space or loopback: where a reverse proxy sits
// (Docker's bridge is 172.16.0.0/12; a host-network Caddy arrives from loopback).
func privatePeer(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback()
}

// reportPathPrefix is where the public report's slug sits in a path.
const reportPathPrefix = "/report/"

// logPath is the path requestLogger writes: p, except the segment after
// /report/ (the slug) becomes ":slug". Done on the raw path, not chi's route
// pattern, so a /report/ path no route matches (a typo, a probe) is redacted too.
func logPath(p string) string {
	rest, ok := strings.CutPrefix(p, reportPathPrefix)
	if !ok || rest == "" {
		return p
	}
	_, tail, hasTail := strings.Cut(rest, "/")
	if !hasTail {
		return reportPathPrefix + ":slug"
	}
	return reportPathPrefix + ":slug/" + tail
}

// appCSP is the Content-Security-Policy every response carries unless it sets
// its own (the public report does, renderReport). The SPA is same-origin
// Vite files with no inline script, so scripts, fonts, fetches, the service
// worker and the manifest are 'self' only: an injected <script> or one from
// elsewhere does not run.
//
// style-src keeps 'unsafe-inline' on purpose. Radix's Dialog locks body scroll
// by injecting a <style> computed at runtime (the scrollbar gap), which no
// hash can cover, and a nonce cannot either: the service worker precaches
// index.html, so a per-request nonce would be stale on later launches. Inline
// style cannot run script; script-src is the line that matters, and it holds.
//
// img-src allows blob: for the receipt photo's preview before upload
// (ReceiptPicker). object-src, base-uri and frame-ancestors close the rest:
// no plugins, no <base> rewriting relative URLs, and no framing, so a
// treasurer cannot be clickjacked into a write.
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' blob:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets the headers every response carries (#447), whichever
// route answers: the SPA shell, assets, /api, the public report.
//
//   - nosniff: a response is only the type it says.
//   - Referrer-Policy same-origin: no app URL leaves for another site. The
//     report tightens this to no-referrer (setReportHeaders), its URL being
//     the secret.
//   - X-Frame-Options and appCSP: see appCSP. X-Frame-Options repeats
//     frame-ancestors for older browsers.
//
// HSTS is not here: the app speaks plain HTTP behind Caddy, which terminates
// TLS and sets it (Caddyfile).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", appCSP)
		next.ServeHTTP(w, r)
	})
}

// crossOriginGuard refuses a non-safe request (anything but GET, HEAD and
// OPTIONS) a browser sent from another origin, with 403 and the API's error
// envelope (#481). It is the stdlib's CrossOriginProtection: a request with
// Sec-Fetch-Site is allowed only for same-origin or none; an older browser
// sending only Origin is allowed only when Origin's host is the request's
// Host. A request with neither header (curl, the CLI, e2e API seeding) is not
// a browser acting for someone else and passes.
//
// It works behind both proxies: Caddy passes the browser's Host through, and
// behind Vite's dev proxy (changeOrigin rewrites Host) every current browser
// sends Sec-Fetch-Site, which is checked first.
var crossOriginGuard = func() func(http.Handler) http.Handler {
	p := http.NewCrossOriginProtection()
	p.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusForbidden, "cross_origin_request", "Cross-origin requests are not allowed.")
	}))
	return p.Handler
}()
