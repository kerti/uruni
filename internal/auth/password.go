package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's Password Storage Cheat Sheet second baseline
// (m=19 MiB, t=2, p=1), a reasonable cost for one small self-hosted instance
// rather than the memory-heaviest option, as the same process holds SQLite's
// one connection (ADR-004) and serves every request. Named constants so a
// retune touches this block; the encoded hash carries its parameters (see
// hashPassword), so retuning never invalidates an old hash.
const (
	argonMemoryKiB   = 19 * 1024 // 19 MiB, in KiB - argon2.IDKey's own unit
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
)

// ErrMalformedHash is returned by verifyPassword when a stored hash is not in
// hashPassword's format: a corrupted row, distinct from a wrong password.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// hashPassword returns a self-describing PHC-like string (algorithm, version,
// parameters, salt and hash in one column), so verifyPassword reads the
// parameters back from the string, not today's constants. That lets
// argonMemoryKiB/argonIterations/argonParallelism be retuned without a
// migration: an old row verifies under the parameters it was hashed with.
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// verifyPassword reports whether password matches the encoded hash
// hashPassword produced. The comparison is constant-time (crypto/subtle), so
// timing cannot leak how many leading bytes matched.
//
// Authenticate (POST /api/login) is the production caller; the round-trip test
// proves hashPassword's output is checkable.
func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// "$argon2id$v=19$m=...,t=...,p=...$salt$hash" splits on "$" into 6 fields,
	// the first empty (a leading separator).
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrMalformedHash
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, ErrMalformedHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrMalformedHash
	}
	// A hash this package wrote is exactly argonKeyLength (32) bytes, so anything
	// else is a corrupted row, and ADR-030's recovery story ("a forgotten password
	// is recovered by the host at the database") makes a hand-edited column a real
	// path. A truncated paste must fail closed: an empty final field still splits
	// into six parts and decodes to a zero-length want, and argon2.IDKey panics on
	// a zero key length. Pinning the length also settles the int -> uint32
	// conversion's bounds.
	if len(want) != argonKeyLength {
		return false, ErrMalformedHash
	}

	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, argonKeyLength)

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
