package http

import (
	"crypto/sha256"
	"encoding/hex"
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
// The attempt is reserved with the rate limiter before Authenticate runs
// (#495), so concurrent attempts are counted while they are still in flight:
// a burst of N wrong guesses admits exactly the limit's worth and refuses the
// rest before any of them pays argon2id's cost. The reservation stands for an
// ErrInvalidCredentials (a real wrong guess), is handed back for anything
// else - a busy hasher, a broken database - since none of those is a guess,
// and the keys are cleared by a success. A malformed body never reaches here
// (decodeJSON returns first).
//
// Three keys are reserved together (#500), because each one alone fails
// somebody:
//
//   - the client network (ip:): one source trying many accounts. A /64 for
//     IPv6, the address for IPv4 (clientNetwork).
//   - the identifier together with that network (id-ip:): one source trying
//     one account. It is only ever filled by guesses from one network, so a
//     stranger naming the treasurer's email from elsewhere spends a pair of his
//     own and hers is untouched. Keyed on the identifier alone, as it was, ten
//     guesses from anyone locked her out from everywhere, renewably.
//   - the identifier alone (id:): every source trying one account. Without it
//     the pair key leaves a distributed guesser a fresh ten per address, so
//     single-account brute force would be bounded only by how many addresses
//     he can borrow. This is the looser, flat ceiling (loginIdentifierCeiling
//     an hour), and the one place a stranger can still close the account to
//     the treasurer - for the rest of an hour, at the price of fifty guesses
//     that were never going to succeed.
//
// A browser that has logged in before carries a device cookie (device_tokens.go).
// Presented for the identifier it was issued to, it moves the attempt onto one
// key of its own, dev:<digest>, and off all three shared ones: neither her
// network nor an attack on her account can lock her out of a device she has
// used. The device key is the same 10 per 5 minutes with the same ladder, so a
// guess costs exactly what it costs anyone; a missing, expired, forged or
// other-identifier cookie is simply no cookie and gets the three shared keys.
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Prefixed so the keys cannot collide in the limiter's one map:
	// without them a caller could lock a bystander's network out by submitting
	// that address as an email ten times. The identifier is the normalised
	// email (auth.NormalizeEmail), so "Treasurer@X " and "treasurer@x" share
	// every budget, and is digested so a megabyte-long "email" costs the map
	// thirty-two bytes a key, not a megabyte.
	network := a.clientNetwork(r)
	who := identifierDigest(auth.NormalizeEmail(req.Email))
	ip := "ip:" + network
	pair := "id-ip:" + who + "|" + network
	identifier := "id:" + who

	base := a.loginLimiter.rule
	ceiling := limitRule{limit: loginIdentifierCeiling, window: loginIdentifierCeilingWindow}

	var deviceDigest string
	var viaDevice bool
	if c, err := r.Cookie(deviceCookieName); err == nil {
		deviceDigest, viaDevice = a.devices.lookup(c.Value, who)
	}

	// Reserved together or none: a refused request must not spend the other
	// keys' budgets.
	var held *reservation
	var ok bool
	if viaDevice {
		held, ok = a.loginLimiter.reserveRules(keyRule{key: "dev:" + deviceDigest, rule: base})
	} else {
		held, ok = a.loginLimiter.reserveRules(
			keyRule{key: ip, rule: base},
			keyRule{key: pair, rule: base},
			keyRule{key: identifier, rule: ceiling},
		)
	}
	if !ok {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_requests", "Too many login attempts. Try again later.")
		return
	}

	user, err := a.auth.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			held.release()
		}
		mapAuthError(w, a.logger, err)
		return
	}

	// A success clears the counters the attempt was reserved under (issue
	// #115) - the treasurer got it right, so any earlier mistyped attempts
	// should not still be counted against her. The identifier ceiling is not
	// cleared: it counts what every source has tried, and a login from one of
	// them is no reason to forgive the rest. A login on a device key leaves
	// the shared keys alone, which it never touched and which may be someone
	// else's lockout.
	//
	// Then the device is (re)issued its cookie: the one it presented, with a
	// fresh 30 days, or a new token if it had none that was good.
	if viaDevice {
		a.loginLimiter.reset("dev:" + deviceDigest)
		a.devices.refresh(deviceDigest)
		if c, err := r.Cookie(deviceCookieName); err == nil {
			http.SetCookie(w, a.deviceCookie(c.Value))
		}
	} else {
		a.loginLimiter.reset(ip, pair)
		if token, err := a.devices.issue(who); err != nil {
			// Not fatal: she is logged in, only without the device cookie.
			a.logger.Error("issuing a device cookie after login", "error", err)
		} else {
			http.SetCookie(w, a.deviceCookie(token))
		}
	}

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

// identifierDigest is the limiter's stand-in for a normalised email: a hash,
// so key size does not depend on what a caller submitted. Not a secret and not
// for storage - the limiter's map lives and dies with the process.
func identifierDigest(identifier string) string {
	sum := sha256.Sum256([]byte(identifier))
	return hex.EncodeToString(sum[:16])
}

// clientNetwork is the part of the client's address the limiters count under
// (#500): the whole address for IPv4, the /64 for IPv6. A /64 is the smallest
// block an ISP hands one subscriber, so one household or one rented host
// owns billions of addresses inside it; keyed per address, an attacker on a
// single /64 would have a fresh ten attempts for each. An IPv4-mapped IPv6
// address is its IPv4 address (clientIP already unmaps it), and anything that
// does not parse is keyed as it came, so it still counts against something.
func (a *api) clientNetwork(r *http.Request) string {
	return networkOf(a.clientIP(r))
}

func networkOf(client string) string {
	addr, err := netip.ParseAddr(client)
	if err != nil {
		return client
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

// clientIP returns the address a request actually came from, the address
// clientNetwork reduces to the key the login, register and restore-confirm
// limiters count under.
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
