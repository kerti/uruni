package http

import (
	"log/slog"
	"net/http"
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

// requestLogger logs one line per request: method, path, status and duration —
// exactly what ADR-022 promised would arrive as middleware at M4, no more.
//
// r.URL.Path only, never RawQuery or the body: a query string or a posted body
// is where a member name, a note or an amount would end up, and ADR-022
// forbids logging any of those. Method, path and status are route shape, not
// payload.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// chi's wrapper is what makes the status readable after the fact —
			// a bare http.ResponseWriter never exposes what WriteHeader was
			// called with.
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
