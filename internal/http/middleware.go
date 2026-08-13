package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

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
