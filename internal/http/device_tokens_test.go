package http

import (
	"fmt"
	"testing"
	"time"
)

func TestDeviceTokensLookupIsPerIdentifierAndExpires(t *testing.T) {
	t.Parallel()
	d := newDeviceTokens()
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }

	token, err := d.issue("alice")
	if err != nil {
		t.Fatalf("issue() = %v", err)
	}
	if _, ok := d.lookup(token, "alice"); !ok {
		t.Fatal("lookup(own identifier) = false")
	}
	if _, ok := d.lookup(token, "bob"); ok {
		t.Error("lookup(another identifier) = true, want false")
	}
	if _, ok := d.lookup("", "alice"); ok {
		t.Error("lookup(empty token) = true")
	}
	if _, ok := d.lookup(token+"x", "alice"); ok {
		t.Error("lookup(forged token) = true")
	}

	now = now.Add(deviceTokenTTL - time.Nanosecond)
	digest, ok := d.lookup(token, "alice")
	if !ok {
		t.Fatal("lookup just inside the TTL = false")
	}
	d.refresh(digest)
	now = now.Add(time.Nanosecond)
	if _, ok := d.lookup(token, "alice"); !ok {
		t.Error("lookup after refresh = false, want the TTL restarted")
	}
	now = now.Add(deviceTokenTTL)
	if _, ok := d.lookup(token, "alice"); ok {
		t.Error("lookup at the refreshed TTL = true, want expired")
	}
}

// TestDeviceTokensStoreOnlyTheHash: the raw token appears nowhere in the map.
func TestDeviceTokensStoreOnlyTheHash(t *testing.T) {
	t.Parallel()
	d := newDeviceTokens()
	token, _ := d.issue("alice")
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.entries[token]; ok {
		t.Error("the raw token is a key in the store")
	}
	if _, ok := d.entries[tokenDigest(token)]; !ok {
		t.Error("the token's sha256 is not a key in the store")
	}
}

func TestDeviceTokensStayBounded(t *testing.T) {
	t.Parallel()
	d := newDeviceTokens()
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }

	tokens := make([]string, 0, deviceTokensMax*3)
	for i := 0; i < deviceTokensMax*3; i++ {
		now = now.Add(time.Minute)
		tok, err := d.issue(fmt.Sprintf("who-%d", i))
		if err != nil {
			t.Fatalf("issue() = %v", err)
		}
		tokens = append(tokens, tok)
	}
	d.mu.Lock()
	n := len(d.entries)
	d.mu.Unlock()
	if n != deviceTokensMax {
		t.Errorf("entries = %d, want the cap %d", n, deviceTokensMax)
	}
	last := len(tokens) - 1
	if _, ok := d.lookup(tokens[last], fmt.Sprintf("who-%d", last)); !ok {
		t.Error("the newest token was evicted, want the oldest to go")
	}
	if _, ok := d.lookup(tokens[0], "who-0"); ok {
		t.Error("the oldest token survived the cap")
	}

	now = now.Add(deviceTokenTTL)
	if _, err := d.issue("fresh"); err != nil {
		t.Fatalf("issue() = %v", err)
	}
	d.mu.Lock()
	n = len(d.entries)
	d.mu.Unlock()
	if n != 1 {
		t.Errorf("entries after everything expired = %d, want 1", n)
	}
}
