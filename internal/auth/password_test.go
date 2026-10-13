package auth

import (
	"errors"
	"strings"
	"testing"
)

// TestPasswordHashRoundTrips: what hashPassword produces, verifyPassword
// accepts for the same password and rejects for another (CLAUDE.md's
// money/ledger-grade bar for this path).
func TestPasswordHashRoundTrips(t *testing.T) {
	t.Parallel()
	encoded, err := hashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hashPassword() = %v, want no error", err)
	}

	if encoded == "correct-horse-battery-staple" {
		t.Fatal("hashPassword returned the plaintext password unchanged")
	}
	if strings.Contains(encoded, "correct-horse-battery-staple") {
		t.Fatal("the encoded hash embeds the plaintext password")
	}

	ok, err := verifyPassword("correct-horse-battery-staple", encoded)
	if err != nil {
		t.Fatalf("verifyPassword(correct password) = %v, want no error", err)
	}
	if !ok {
		t.Error("verifyPassword(correct password) = false, want true")
	}

	ok, err = verifyPassword("wrong-password-entirely", encoded)
	if err != nil {
		t.Fatalf("verifyPassword(wrong password) = %v, want no error", err)
	}
	if ok {
		t.Error("verifyPassword(wrong password) = true, want false")
	}
}

// TestPasswordHashIsSaltedPerCall: hashing one password twice must not give
// the same stored value, or two treasurers choosing the same password would
// look identical in the database and a precomputed dictionary would work
// across every row.
func TestPasswordHashIsSaltedPerCall(t *testing.T) {
	t.Parallel()
	first, err := hashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hashPassword() = %v, want no error", err)
	}
	second, err := hashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hashPassword() = %v, want no error", err)
	}

	if first == second {
		t.Error("hashing the same password twice produced the same encoded hash - salt is not varying per call")
	}
}

// TestVerifyPasswordRejectsAMalformedHash covers a corrupted or foreign-format
// row: ErrMalformedHash, not a panic or a silent match.
func TestVerifyPasswordRejectsAMalformedHash(t *testing.T) {
	t.Parallel()
	for _, encoded := range []string{
		"",
		"not-a-hash-at-all",
		"$argon2id$v=19$m=19456,t=2,p=1$onlyonefield",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		// A hand-edited row truncated to an empty hash field still splits into six
		// parts, so only the key-length check catches it, and it must be:
		// argon2.IDKey panics on a zero key length. ADR-030 puts an operator at this
		// column by design.
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$",
		// The right shape but the wrong length: a partial paste.
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		// The version field unreadable: a row from another tool starting "$argon2id$".
		"$argon2id$version=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		// The parameter field in a shape Sscanf cannot read, so the hash's cost
		// parameters are unknown; verifying under today's constants would silently
		// compare against a different hash.
		"$argon2id$v=19$memory=19456$c2FsdA$aGFzaA",
		// Salt and hash fields that are not base64 at all.
		"$argon2id$v=19$m=19456,t=2,p=1$sa!t$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGF6!A",
	} {
		if _, err := verifyPassword("whatever", encoded); !errors.Is(err, ErrMalformedHash) {
			t.Errorf("verifyPassword(_, %q) error = %v, want ErrMalformedHash", encoded, err)
		}
	}
}
