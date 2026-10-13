package http

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// Device cookies (#500, the OWASP "device cookies" pattern).
//
// Login's shared keys (the client network and the identifier-wide ceiling) are
// shared with strangers: whoever fills them closes them to the treasurer too.
// A device cookie tells her from a stranger without trusting her address. A
// successful login hands the browser an opaque random token; presenting it
// again for the same identifier moves the attempt onto its own key
// (dev:<digest>) and off the shared ones, so neither her network nor an attack
// on the account can lock her out. It does not lower a guess's cost: the
// device key is the same 10 attempts per 5 minutes, escalating to an hour, and
// the cookie is issued only by a correct password. A stranger has no token and
// stays on the shared keys.
//
// The server remembers only sha256(token), mapped to the identifier digest and
// an expiry, in memory: a restart forgets every device and she is on the
// shared keys until her next login, which is accepted. The raw token exists
// only in the cookie.
const (
	deviceCookieName = "device"
	deviceTokenTTL   = 30 * 24 * time.Hour
	// deviceTokensMax caps the store. Only a correct password adds an entry and an
	// instance has one login, so it bounds memory, not a number anyone reaches; at
	// the cap the entry nearest expiry goes.
	deviceTokensMax = 64
)

type deviceEntry struct {
	who   string
	until time.Time
}

type deviceTokens struct {
	mu      sync.Mutex
	entries map[string]deviceEntry // sha256(token), hex -> entry
	now     func() time.Time
}

func newDeviceTokens() *deviceTokens {
	return &deviceTokens{entries: make(map[string]deviceEntry), now: time.Now}
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// lookup reports the device key digest for token if live and issued for
// identifier who. A token for another identifier, expired, unknown or forged
// all answer false alike.
func (d *deviceTokens) lookup(token, who string) (string, bool) {
	if token == "" {
		return "", false
	}
	digest := tokenDigest(token)
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[digest]
	if !ok || e.who != who || !d.now().Before(e.until) {
		return "", false
	}
	return digest, true
}

// issue mints a token for who and returns it. The caller sets it as a cookie;
// only its hash is kept.
func (d *deviceTokens) issue(who string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweepLocked()
	if len(d.entries) >= deviceTokensMax {
		d.evictOldestLocked()
	}
	d.entries[tokenDigest(token)] = deviceEntry{who: who, until: d.now().Add(deviceTokenTTL)}
	return token, nil
}

// refresh pushes a live token's expiry out to a full TTL from now: a device
// that keeps logging in stays known.
func (d *deviceTokens) refresh(digest string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.entries[digest]; ok {
		e.until = d.now().Add(deviceTokenTTL)
		d.entries[digest] = e
	}
}

func (d *deviceTokens) sweepLocked() {
	now := d.now()
	for k, e := range d.entries {
		if !now.Before(e.until) {
			delete(d.entries, k)
		}
	}
}

func (d *deviceTokens) evictOldestLocked() {
	var oldest string
	var oldestUntil time.Time
	for k, e := range d.entries {
		if oldest == "" || e.until.Before(oldestUntil) {
			oldest, oldestUntil = k, e.until
		}
	}
	delete(d.entries, oldest)
}

// deviceCookie builds the cookie with the session cookie's attributes (scs's
// Cookie settings, newSessionManager): Path, HttpOnly, SameSite and Secure
// (from the base URL's scheme). Not Session-scoped: it outlives the session
// and logout on purpose, being no session.
func (a *api) deviceCookie(token string) *http.Cookie {
	c := a.sessionManager.Cookie
	//nolint:gosec // G124: Secure, HttpOnly and SameSite are copied from the session cookie's configuration (Secure follows the base URL's scheme), which the linter cannot see
	return &http.Cookie{
		Name:     deviceCookieName,
		Value:    token,
		Path:     c.Path,
		Domain:   c.Domain,
		HttpOnly: c.HttpOnly,
		SameSite: c.SameSite,
		Secure:   c.Secure,
		MaxAge:   int(deviceTokenTTL / time.Second),
		Expires:  a.devices.now().Add(deviceTokenTTL),
	}
}
