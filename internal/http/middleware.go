package http

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// sessionRequired is the gate #116 adds: every /api route but the four
// public ones (register, login, session, logout) sits behind this. It is an
// *api method rather than a free function because it has to reach
// a.sessionManager - the same reason every other handler in this package
// hangs off *api instead of being built as closures.
//
// It checks Exists(sessionKeyUserID), not merely "is there a session" -
// LoadAndSave (api.go) attaches a session to every /api request whether or
// not the caller sent a cookie, so an empty, freshly-created session and an
// authenticated one are both "a session" and only the stored key tells them
// apart.
//
// Deliberately injects no context value. ADR-030 decision 2: a session
// proves "you are the treasurer," nothing more - there is exactly one
// treasurer account for the life of a v1 instance, so no handler downstream
// ever needs to know *which* identity passed the gate, only that one did.
// That is also what keeps this change from touching resolveFund or any of
// its 29 call sites: they already resolve the one fund a session-bearing
// request is allowed to touch, with no user id to thread through to get
// there.
func (a *api) sessionRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.sessionManager.Exists(r.Context(), sessionKeyUserID) {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "You must be logged in to do that.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestLogger logs one line per request: method, path, status and duration -
// exactly what ADR-022 promised would arrive as middleware at M4, no more.
//
// r.URL.Path only, never RawQuery or the body: a query string or a posted body
// is where a member name, a note or an amount would end up, and ADR-022
// forbids logging any of those. Method, path and status are route shape, not
// payload.
//
// The path itself is redacted where it carries a secret (#447): the public
// report's slug is the only thing guarding it (ADR-035), and logs travel -
// pasted into a bug report, shipped to a collector - so /report/<slug> and
// its sub-paths log as /report/:slug (see logPath).
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// chi's wrapper is what makes the status readable after the fact -
			// a bare http.ResponseWriter never exposes what WriteHeader was
			// called with.
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("request",
				"method", r.Method,
				"path", logPath(r.URL.Path),
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// reportPathPrefix is where the public report's slug sits in a path.
const reportPathPrefix = "/report/"

// logPath is the path requestLogger writes: p itself, except that the
// segment after /report/ - the report's slug - becomes ":slug". Done on the
// raw path rather than chi's route pattern so a /report/ path no route
// matches (a mistyped sub-path, a probe) is redacted too.
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

// appCSP is the Content-Security-Policy every response carries unless it
// sets its own (the public report does, renderReport). The SPA is built by
// Vite into same-origin files with no inline script, so scripts, fonts,
// fetches, the service worker and the manifest are 'self' only - an injected
// <script> or a script from anywhere else does not run.
//
// style-src keeps 'unsafe-inline' on purpose. Radix's Dialog locks body
// scroll by injecting a <style> whose text is computed at runtime (the
// scrollbar gap), so no hash can cover it, and a nonce cannot either: the
// service worker precaches index.html, so a per-request nonce would be stale
// on every later launch. Inline style cannot run script; script-src is the
// line that matters, and it holds.
//
// img-src allows blob: for the receipt photo's preview before upload
// (ReceiptPicker). object-src, base-uri and frame-ancestors close the
// remaining ways in: no plugins, no <base> rewriting relative URLs, and no
// framing, so a treasurer cannot be clickjacked into a write.
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' blob:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets the headers every response carries (#447), whatever
// route answers it - the SPA shell, its assets, /api, the public report.
//
//   - nosniff: a response is only ever the type it says it is.
//   - Referrer-Policy same-origin: no app URL leaves for another site. The
//     public report tightens this to no-referrer itself (setReportHeaders),
//     since its own URL is the secret.
//   - X-Frame-Options and appCSP: see appCSP. X-Frame-Options repeats
//     frame-ancestors for browsers that predate it.
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
// OPTIONS) that a browser sent from another origin, with 403 and the API's
// error envelope (#481). It is the stdlib's CrossOriginProtection: a request
// carrying Sec-Fetch-Site is allowed only when that says same-origin or none;
// an older browser that sends only Origin is allowed only when Origin's host
// is the request's Host. A request with neither header - curl, the CLI, the
// e2e API seeding - is not a browser acting for someone else, and passes.
//
// Both proxies in front of the app keep this working: Caddy passes the
// browser's Host through, and behind Vite's dev proxy (changeOrigin rewrites
// Host) every current browser sends Sec-Fetch-Site, which is checked first.
var crossOriginGuard = func() func(http.Handler) http.Handler {
	p := http.NewCrossOriginProtection()
	p.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusForbidden, "cross_origin_request", "Cross-origin requests are not allowed.")
	}))
	return p.Handler
}()
