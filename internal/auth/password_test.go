package auth

import (
	"errors"
	"strings"
	"testing"
)

// TestPasswordHashRoundTrips is the money/ledger-grade bit CLAUDE.md asks
// for on this path: what hashPassword produces, verifyPassword must accept
// back for the same password, and reject for a different one.
func TestPasswordHashRoundTrips(t *testing.T) {
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

// TestPasswordHashIsSaltedPerCall: hashing the identical password twice must
// not produce the identical stored value - otherwise two treasurers who
// happened to pick the same password would be visibly identical in the
// database, and a precomputed dictionary would work across every row at
// once instead of needing a fresh attempt per row.
func TestPasswordHashIsSaltedPerCall(t *testing.T) {
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

// TestVerifyPasswordRejectsAMalformedHash covers a corrupted or foreign-
// format row: verifyPassword must answer with ErrMalformedHash rather than
// panicking or silently reporting a match.
func TestVerifyPasswordRejectsAMalformedHash(t *testing.T) {
	for _, encoded := range []string{
		"",
		"not-a-hash-at-all",
		"$argon2id$v=19$m=19456,t=2,p=1$onlyonefield",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		// A hand-edited row truncated to an empty hash field. It still
		// splits into six parts, so only the key-length check catches it -
		// and it has to be caught: argon2.IDKey panics outright on a zero
		// key length. ADR-030 puts an operator at this column by design.
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$",
		// A hash of the right shape but the wrong length - a partial paste.
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		// The version field itself unreadable - a row written by some
		// other tool that happens to start "$argon2id$".
		"$argon2id$version=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		// The parameter field in a shape Sscanf cannot read back, so the
		// cost parameters this hash was actually made with are unknown -
		// verifying under today's constants instead would silently compare
		// against a different hash.
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
