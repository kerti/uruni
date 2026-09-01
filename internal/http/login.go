package http

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/kerti/uruni/internal/auth"
)

// loginRequest is POST /api/login's body - the same two fields
// registerRequest carries, kept as its own type rather than reused so the
// two routes' request shapes can drift independently if either ever needs
// to (they already answer with different response shapes: userResponse on
// success here too, but a distinct 401 body on failure that register has no
// equivalent of).
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login is POST /api/login (issue #115): verifies email and password
// through auth.Authenticate and, on success, logs the treasurer in exactly
// the way register.go already does - RenewToken then Put, so the pattern
// isn't invented twice.
//
// The rate limiter is checked before Authenticate ever runs, so a caller
// already locked out never pays argon2id's cost, and is updated only on
// auth.ErrInvalidCredentials specifically - a malformed body never reaches
// here (decodeJSON returns first) and an infrastructure failure inside
// Authenticate is not a guessed-wrong password, so neither counts as an
// attempt.
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Prefixed so the two keys cannot collide in the limiter's one map:
	// without them a caller could lock a bystander's IP out by submitting
	// that address as an email ten times.
	ip := "ip:" + clientIP(r)
	identifier := "id:" + strings.TrimSpace(req.Email)

	// Checked independently, not "either" short-circuited into one boolean
	// before recording below: both keys have to be live for the "distributed
	// guesser vs. single-account brute force" pairing issue #115 asks for.
	if a.loginLimiter.blocked(ip) || a.loginLimiter.blocked(identifier) {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_requests", "Too many login attempts. Try again later.")
		return
	}

	user, err := a.auth.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			a.loginLimiter.recordFailure(ip)
			a.loginLimiter.recordFailure(identifier)
		}
		mapAuthError(w, a.logger, err)
		return
	}

	// A success clears both counters (issue #115) - the treasurer got it
	// right, so any earlier mistyped attempts should not still be counted
	// against her.
	a.loginLimiter.reset(ip)
	a.loginLimiter.reset(identifier)

	// RenewToken before writing anything into the session: a token issued
	// before the identity was known to it must never be the one that ends
	// up carrying that identity (session fixation) - the same reasoning
	// register.go's own comment gives for the identical two lines below.
	if err := a.sessionManager.RenewToken(r.Context()); err != nil {
		a.logger.Error("renewing session token after login", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	a.sessionManager.Put(r.Context(), sessionKeyUserID, user.ID)

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// clientIP returns the address a request actually came from. Caddy
// (ADR-009) is the only ingress in front of the app in production -
// docker-compose.yml exposes the app only to Caddy's own container, never
// to the host network - so an X-Forwarded-For header reaching this handler
// was set by that one reverse proxy, not forged by a caller who could reach
// the app directly. Its first entry - the original client, per the header's
// own left-to-right convention as proxies append to it - is preferred.
//
// RemoteAddr, with the port stripped, is the fallback for a request with no
// proxy in front of it at all: `make web-dev`'s plain HTTP loopback, and
// every test in this package.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
