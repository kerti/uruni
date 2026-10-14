package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/kerti/uruni/internal/stack"
)

// loggerKey keys the request-scoped logger in a request's context.
type loggerKey struct{}

// requestID gives every request an ID and a logger that carries it, so the
// request line and every line a handler logs while serving it can be joined
// when an operator reconstructs an incident (#519).
//
// The ID is ours, 12 hex characters from crypto/rand: random, so it says
// nothing about the host or the request (chi's middleware.RequestID embeds the
// hostname). An incoming X-Request-Id is ignored, not trusted: it is whatever
// a client chose to write into the operator's log. The ID is not sent back in
// a response header either; it is for the operator's side of the glass.
func requestID(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b [6]byte
			_, _ = rand.Read(b[:])
			l := base.With("request_id", hex.EncodeToString(b[:]))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loggerKey{}, l)))
		})
	}
}

// loggerFrom is the logger for the request ctx belongs to: the one requestID
// attached, or base when ctx carries none (a handler called directly in a test,
// or code that runs outside a request).
func loggerFrom(ctx context.Context, base *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return base
}

// log is the request-scoped logger for r: what every handler logs through, so
// its lines carry the request_id. Startup and the scheduler, which serve no
// request, keep a.logger.
func (a *api) log(r *http.Request) *slog.Logger {
	return loggerFrom(r.Context(), a.logger)
}

// securityEvent logs one line for something an operator reviewing an incident
// needs to see, with the client's address (clientIP, so a trusted proxy's
// X-Forwarded-For is honoured). The address appears here and nowhere else:
// request lines stay free of it. It is operator data, not member data
// (ADR-022); nothing the caller submitted (an email, a password, a slug) is
// ever an attribute. attrs are for fixed vocabulary such as a reason code.
func (a *api) securityEvent(r *http.Request, level slog.Level, event string, attrs ...any) {
	attrs = append([]any{"event", event, "ip", a.clientIP(r)}, attrs...)
	a.log(r).Log(r.Context(), level, "security event", attrs...)
}

// recoverer turns a panic in a handler into an error log and a 500, in place of
// chi's middleware.Recoverer (#519). It sits inside requestID and requestLogger
// (router.go), so the log line carries the request_id and the request line
// still appears, with status 500.
//
// The stack has its argument words stripped and only the panic's type (or a
// runtime error's fixed text) is logged, for the reasons in package stack.
// Under /api the answer is the JSON error envelope the SPA parses; elsewhere a
// plain 500. http.ErrAbortHandler is the stdlib's way to abort a response
// quietly, so it is re-panicked for net/http to handle, unlogged.
func recoverer(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				loggerFrom(r.Context(), base).Error("panic serving request",
					"panic", stack.Describe(v), "stack", stack.WithoutArgs(debug.Stack()))
				if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
					writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
					return
				}
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
