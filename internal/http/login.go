package http

import (
	"errors"
	"net/http"
	"net/netip"
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
	ip := "ip:" + a.clientIP(r)
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

// clientIP returns the address a request actually came from, the key the
// login and restore-confirm limiters count under.
//
// The TCP peer is the answer unless it is one of a.trustedProxies
// (URUNI_TRUSTED_PROXIES, #447). Only then is X-Forwarded-For read, from the
// right: each proxy appends the address it received from, so the right-most
// entry that is not itself a trusted proxy is the first hop nobody trusted -
// the client. Entries left of it are whatever that client wrote and are
// never read. Without the setting the header is ignored outright: anyone who
// can reach the app directly could otherwise name a fresh address per
// request and never be counted.
//
// The shipped stack sets the setting for Caddy (ADR-009), which overwrites
// the header with the peer it saw. With no proxy at all - `make web-dev`'s
// loopback, and the tests in this package - the peer is the client.
func (a *api) clientIP(r *http.Request) string {
	peer := remoteAddr(r)
	if !a.trusted(peer) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// A hop that is not an address cannot be keyed on; the last
			// address that could be trusted is the honest answer.
			break
		}
		hop = hop.Unmap()
		if !a.trusted(hop) {
			return hop.String()
		}
		peer = hop
	}
	return peer.String()
}

// trusted reports whether addr is one of a.trustedProxies.
func (a *api) trusted(addr netip.Addr) bool {
	for _, p := range a.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// remoteAddr is r.RemoteAddr without its port, as an address. A RemoteAddr
// that does not parse (never, from net/http's own server) is the zero Addr,
// which no range contains and which keys as "invalid IP".
func remoteAddr(r *http.Request) netip.Addr {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap()
	}
	addr, _ := netip.ParseAddr(r.RemoteAddr)
	return addr.Unmap()
}
