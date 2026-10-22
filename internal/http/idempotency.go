package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
)

// Idempotency keys on the immutable writes (ADR-043). The ledger owns the check,
// the write and the record in one transaction; this file is the HTTP half: read
// the header, fingerprint the request, and tell a replay apart on the way out.

const (
	// idempotencyHeader is minted by the SPA's fetch wrapper: 16 random bytes as
	// 32 hex characters. The server asks only for what the table's CHECK does.
	idempotencyHeader = "Idempotency-Key"

	// idempotentReplayedHeader marks a reply that is the stored result of an
	// earlier identical request. Nothing in the app reads it; a test or curl can
	// tell a replay from a first success without comparing rows.
	idempotentReplayedHeader = "Idempotent-Replayed"

	// idempotencyRetention is how long a record answers: long enough for a night
	// without signal, a restart or a deploy; the client forgets a key after 15
	// minutes anyway.
	idempotencyRetention = 24 * time.Hour
)

type idempotencyCtxKey struct{}

// validIdempotencyKey is the wire rule: 16 to 64 characters of [A-Za-z0-9_-],
// the same as the table's CHECK.
func validIdempotencyKey(key string) bool {
	if len(key) < 16 || len(key) > 64 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// keyed wraps a route whose write is safe to repeat under an idempotency key
// (ADR-043). Without the header it does nothing: the write is simply
// unprotected, so curl, e2e seeding and a stale cached PWA bundle keep working.
// An invalid key is 400 invalid_idempotency_key.
//
// With a valid one it reads the body (same cap and 413 as decodeJSON), puts it
// back for the handler, and stores a *ledger.Idempotency in the request's
// context. It sits behind the session gate, so a replay is as gated as the
// write it repeats.
func (a *api) keyed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values(idempotencyHeader)
		if len(values) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if len(values) > 1 || !validIdempotencyKey(values[0]) {
			writeAPIError(w, http.StatusBadRequest, "invalid_idempotency_key", "The Idempotency-Key header must be 16 to 64 characters of letters, digits, underscore or hyphen.")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The request body is too large.")
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body could not be read.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		now := a.now()
		idem := &ledger.Idempotency{
			Key:         values[0],
			Fingerprint: requestFingerprint(r.Method, r.URL.Path, body),
			Now:         now.Unix(),
			ExpiresAt:   now.Add(idempotencyRetention).Unix(),
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), idempotencyCtxKey{}, idem)))
	})
}

// idempotencyFrom is the key a handler on a keyed route passes to the ledger:
// nil when the request carried none.
func idempotencyFrom(r *http.Request) *ledger.Idempotency {
	idem, _ := r.Context().Value(idempotencyCtxKey{}).(*ledger.Idempotency)
	return idem
}

// markReplay, when the ledger flagged the answer a replay, adds
// Idempotent-Replayed (before the body is written) and logs the route and the
// stored result's age, never the key or the body (ADR-022). The age tells a
// retry (seconds) from a swallowed deliberate entry (minutes).
func markReplay(w http.ResponseWriter, r *http.Request, logger *slog.Logger, idem *ledger.Idempotency) {
	if idem == nil || !idem.Replayed {
		return
	}
	w.Header().Set(idempotentReplayedHeader, "true")
	route := r.URL.Path
	if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
		route = rc.RoutePattern()
	}
	logger.Info("replayed an idempotent write", "method", r.Method, "route", route, "age_seconds", idem.AgeSeconds)
}

// requestFingerprint is the SHA-256 of the method, the request path (so an id in
// the path counts; the route pattern would not) and the canonical body, as hex.
func requestFingerprint(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(canonicalBody(body))
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalBody is the JSON decoded with UseNumber and encoded again, which
// sorts the keys and drops whitespace but keeps each number's own text, so a
// retry from any client that orders its keys differently is the same request,
// while a number written another way is a different one (a 409, never a silent
// replay). A body that is not exactly one JSON value hashes as its raw bytes;
// its handler answers 400 and nothing is recorded.
//
// The wire body is hashed, not the decoded request struct, so that a deploy that
// changes a struct cannot change the fingerprint of a retry still to be made.
func canonicalBody(body []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return body
	}
	// Anything after the first value is something the handler's decoder would
	// ignore and this hash must not.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return body
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return body
	}
	return out.Bytes()
}
