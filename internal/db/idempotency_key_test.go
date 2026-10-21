package db

import (
	"strings"
	"testing"
)

// The idempotency_key table's own CHECKs (ADR-043) are the backstop for the
// ledger's helper: a key outside [A-Za-z0-9_-]{16,64}, a fingerprint that is not
// 64 characters and a record that expires at or before it was made are refused,
// and the ledger's crash-between test leans on the last.
func TestIdempotencyKeyTableChecks(t *testing.T) {
	t.Parallel()
	good := strings.Repeat("k", 16)
	fp := strings.Repeat("f", 64)
	tests := []struct {
		name    string
		key     string
		fp      string
		created int64
		expires int64
		wantErr bool
	}{
		{"a valid record", good, fp, 100, 101, false},
		{"the longest key", strings.Repeat("k", 64), fp, 100, 101, false},
		{"hyphen and underscore are allowed", "abc_DEF-123_456-7", fp, 100, 101, false},
		{"a key one short", strings.Repeat("k", 15), fp, 100, 101, true},
		{"a key one long", strings.Repeat("k", 65), fp, 100, 101, true},
		{"a key with a space", "kkkkkkkk kkkkkkkk", fp, 100, 101, true},
		{"a key with a dot", "kkkkkkkk.kkkkkkkk", fp, 100, 101, true},
		{"a fingerprint one short", good, strings.Repeat("f", 63), 100, 101, true},
		{"expiry equal to creation", good, fp, 100, 100, true},
		{"expiry before creation", good, fp, 100, 99, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sqlDB := migratedDB(t)
			_, err := sqlDB.Exec(`INSERT INTO idempotency_key (key, fingerprint, result, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
				tt.key, tt.fp, []byte("{}"), tt.created, tt.expires)
			if (err != nil) != tt.wantErr {
				t.Errorf("insert = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
