package http

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/kerti/uruni/internal/store"
)

// TestConcurrentRequestsOnOneSessionDoNotCollide is the regression test for
// the old delete-then-insert commit. scs marks every loaded session Modified
// while IdleTimeout is set, so each request re-commits the same token; under
// the old pair, two interleaved as DELETE / DELETE / INSERT / INSERT and the
// second INSERT tripped session.token's primary key, a 500 on an ordinary
// request. Two parallel fetches from one SPA is everyday, not exotic: it
// reproduced at 7 failures in 60 requests before the write became one statement.
func TestConcurrentRequestsOnOneSessionDoNotCollide(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	rec := postRegister(t, r, "bendahara@example.org", "kata-sandi-panjang")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", rec.Code, http.StatusCreated)
	}
	token := sessionCookie(rec)
	if token == "" {
		t.Fatal("register set no session cookie")
	}

	const parallel = 24
	codes := make([]int, parallel)
	var wg sync.WaitGroup
	for i := range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/fund", nil)
			// An inbound request cookie: only Name and Value cross the wire from a
			// browser, so the flags gosec wants have nowhere to go. The flags that matter
			// are asserted on the response in register_test.go.
			req.AddCookie(&http.Cookie{Name: "session", Value: token}) //nolint:gosec // request-side cookie carries name and value only
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			codes[i] = w.Code
		}()
	}
	wg.Wait()

	for i, code := range codes {
		if code >= 500 {
			t.Errorf("request %d status = %d, want no server error - the session commit collided", i, code)
		}
	}

	// One cookie still means one row, however many requests rewrote it.
	var rows int
	if err := sqlDB.QueryRow("SELECT count(*) FROM session").Scan(&rows); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if rows != 1 {
		t.Errorf("session rows = %d, want 1", rows)
	}
}

// newTestSessionStore returns the store over a real migrated database. The
// tests drive scs.Store's contract directly, because their arms (an unknown
// token, an expired row, a database gone away) are ones a router request never
// produces on purpose.
func newTestSessionStore(t *testing.T) (*sessionStore, *sql.DB) {
	t.Helper()
	sqlDB := testStoreDB(t)
	return newSessionStore(store.New(sqlDB), scs.GobCodec{}), sqlDB
}

// requestCtx is what trackSessionLoads gives a request: a context that records
// the tokens FindCtx finds. The store tests drive the scs.Store contract
// directly, so they build one by hand.
func requestCtx() context.Context {
	return context.WithValue(context.Background(), sessionLoadsKey{}, &sessionLoads{})
}

func sessionRowCount(t *testing.T, sqlDB *sql.DB, token string) int {
	t.Helper()
	var rows int
	if err := sqlDB.QueryRow("SELECT count(*) FROM session WHERE token = ?", token).Scan(&rows); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	return rows
}

func sessionExpiry(t *testing.T, sqlDB *sql.DB, token string) int64 {
	t.Helper()
	var at int64
	if err := sqlDB.QueryRow("SELECT expires_at FROM session WHERE token = ?", token).Scan(&at); err != nil {
		t.Fatalf("reading expires_at of %q: %v", token, err)
	}
	return at
}

// TestSessionStoreRoundTripsThroughThePlainInterface exercises Find and Delete,
// the non-ctx forms scs.Store requires. Production never calls them (scs prefers
// the Ctx forms), so this alone proves they forward correctly. Commit has no
// request context to say which arm applies, so it refuses (#601).
func TestSessionStoreRoundTripsThroughThePlainInterface(t *testing.T) {
	t.Parallel()
	s, _ := newTestSessionStore(t)

	if err := s.CommitCtx(requestCtx(), "a-token", []byte("session-data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CommitCtx() = %v, want no error", err)
	}
	if err := s.Commit("a-token", []byte("again"), time.Now().Add(time.Hour)); !errors.Is(err, errNoSessionLoads) {
		t.Errorf("Commit() = %v, want errNoSessionLoads", err)
	}

	data, found, err := s.Find("a-token")
	if err != nil {
		t.Fatalf("Find() = %v, want no error", err)
	}
	if !found {
		t.Fatal("Find(a committed token) found nothing")
	}
	if string(data) != "session-data" {
		t.Errorf("Find() data = %q, want %q", data, "session-data")
	}

	if err := s.Delete("a-token"); err != nil {
		t.Fatalf("Delete() = %v, want no error", err)
	}
	if _, found, err = s.Find("a-token"); err != nil || found {
		t.Errorf("Find(a deleted token) = found %v, err %v, want false and no error", found, err)
	}
}

// TestSessionStoreCommitWithoutALoadRecordFails: a context that cannot say
// whether the token was loaded is refused, never guessed (#601). Neither arm
// leaves a row.
func TestSessionStoreCommitWithoutALoadRecordFails(t *testing.T) {
	t.Parallel()
	s, sqlDB := newTestSessionStore(t)

	if err := s.CommitCtx(context.Background(), "a-token", []byte("d"), time.Now().Add(time.Hour)); !errors.Is(err, errNoSessionLoads) {
		t.Errorf("CommitCtx(no record) = %v, want errNoSessionLoads", err)
	}
	if n := sessionRowCount(t, sqlDB, "a-token"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

// TestSessionStoreWritesBackOnlyWhatTheRequestLoaded is #601's rule at the
// store: a loaded token that was deleted meanwhile stays deleted; a loaded one
// that is still there is updated (the sliding expiry moves); a token the
// request never loaded is inserted.
func TestSessionStoreWritesBackOnlyWhatTheRequestLoaded(t *testing.T) {
	t.Parallel()
	s, sqlDB := newTestSessionStore(t)
	soon := time.Now().Add(time.Hour)
	later := time.Now().Add(48 * time.Hour)

	t.Run("a loaded token deleted since stays deleted", func(t *testing.T) {
		ctx := requestCtx()
		if err := s.CommitCtx(ctx, "gone", []byte("v1"), soon); err != nil { // new token: inserted
			t.Fatalf("CommitCtx(new) = %v", err)
		}
		if _, found, err := s.FindCtx(ctx, "gone"); err != nil || !found {
			t.Fatalf("FindCtx() = found %v, err %v, want a hit", found, err)
		}
		if err := s.DeleteCtx(context.Background(), "gone"); err != nil { // a restore, a password change
			t.Fatalf("DeleteCtx() = %v", err)
		}
		if err := s.CommitCtx(ctx, "gone", []byte("v2"), later); err != nil {
			t.Fatalf("CommitCtx(revoked) = %v, want no error: a revoked session is not a failure", err)
		}
		if n := sessionRowCount(t, sqlDB, "gone"); n != 0 {
			t.Errorf("rows = %d, want 0 - the commit resurrected a deleted session", n)
		}
	})

	t.Run("a loaded token still there is updated", func(t *testing.T) {
		ctx := requestCtx()
		if err := s.CommitCtx(ctx, "live", []byte("v1"), soon); err != nil {
			t.Fatalf("CommitCtx(new) = %v", err)
		}
		if _, found, err := s.FindCtx(ctx, "live"); err != nil || !found {
			t.Fatalf("FindCtx() = found %v, err %v, want a hit", found, err)
		}
		if err := s.CommitCtx(ctx, "live", []byte("v2"), later); err != nil {
			t.Fatalf("CommitCtx(loaded) = %v", err)
		}
		data, _, _ := s.FindCtx(context.Background(), "live")
		if string(data) != "v2" || sessionExpiry(t, sqlDB, "live") != later.Unix() {
			t.Errorf("row = %q expiring %d, want v2 expiring %d", data, sessionExpiry(t, sqlDB, "live"), later.Unix())
		}
	})

	t.Run("a new token is inserted", func(t *testing.T) {
		if err := s.CommitCtx(requestCtx(), "fresh", []byte("v1"), soon); err != nil {
			t.Fatalf("CommitCtx(new) = %v", err)
		}
		if n := sessionRowCount(t, sqlDB, "fresh"); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
	})

	t.Run("another request's load does not license this one", func(t *testing.T) {
		if err := s.CommitCtx(requestCtx(), "shared", []byte("v1"), soon); err != nil {
			t.Fatalf("CommitCtx(new) = %v", err)
		}
		other := requestCtx()
		if _, found, _ := s.FindCtx(other, "shared"); !found {
			t.Fatal("FindCtx() found nothing")
		}
		if err := s.DeleteCtx(context.Background(), "shared"); err != nil {
			t.Fatalf("DeleteCtx() = %v", err)
		}
		// A request that never loaded the token commits it as new, and the PK
		// is free; the other request's record changes nothing for this one.
		if err := s.CommitCtx(requestCtx(), "shared", []byte("v2"), soon); err != nil {
			t.Fatalf("CommitCtx() = %v", err)
		}
		if n := sessionRowCount(t, sqlDB, "shared"); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
	})
}

// TestSessionStoreFindReportsAMissOnAnUnknownOrExpiredToken is scs's Store.Find
// contract: "not found" is not an error, and a row past its idle window reads
// absent even before the lazy sweep, or an expired cookie would work until a
// later request committed.
func TestSessionStoreFindReportsAMissOnAnUnknownOrExpiredToken(t *testing.T) {
	t.Parallel()
	s, sqlDB := newTestSessionStore(t)

	data, found, err := s.FindCtx(context.Background(), "never-issued")
	if err != nil {
		t.Fatalf("FindCtx(unknown token) = %v, want no error", err)
	}
	if found || data != nil {
		t.Errorf("FindCtx(unknown token) = %q, %v, want nil, false", data, found)
	}

	if err := s.CommitCtx(requestCtx(), "stale", []byte("d"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CommitCtx() = %v, want no error", err)
	}
	if _, found, err = s.FindCtx(context.Background(), "stale"); err != nil || found {
		t.Errorf("FindCtx(expired token) = found %v, err %v, want false and no error", found, err)
	}

	// The sweep rides on the commit above, so the expired row is gone from the
	// table too, not merely filtered from the read.
	var rows int
	if err := sqlDB.QueryRow("SELECT count(*) FROM session WHERE token = 'stale'").Scan(&rows); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if rows != 0 {
		t.Errorf("expired session rows = %d, want 0 - the lazy sweep did not run", rows)
	}
}

// TestSessionStoreSurfacesADatabaseError: every arm returns the error rather
// than swallow it into a silent "no session", which would log the treasurer
// out unexplained whenever the database is unreachable.
func TestSessionStoreSurfacesADatabaseError(t *testing.T) {
	t.Parallel()
	s, sqlDB := newTestSessionStore(t)
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("Close() = %v, want no error", err)
	}
	ctx := context.Background()

	if _, found, err := s.FindCtx(ctx, "a-token"); err == nil || found {
		t.Errorf("FindCtx() on a closed database = found %v, err %v, want an error", found, err)
	}
	if err := s.CommitCtx(requestCtx(), "a-token", []byte("d"), time.Now().Add(time.Hour)); err == nil {
		t.Error("CommitCtx() on a closed database = nil, want an error")
	}
	if err := s.DeleteCtx(ctx, "a-token"); err == nil {
		t.Error("DeleteCtx() on a closed database = nil, want an error")
	}
}

// encodedSession gob-encodes a session the way scs does. Two calls with equal
// values may differ in bytes, since the codec walks the map in random order.
func encodedSession(t *testing.T, deadline time.Time, values map[string]any) []byte {
	t.Helper()
	b, err := scs.GobCodec{}.Encode(deadline, values)
	if err != nil {
		t.Fatalf("encoding a session: %v", err)
	}
	return b
}

// TestSessionStoreRefreshesAnUnchangedSessionAtMostHourly is #529: an idle
// refresh of an unchanged session is skipped until its expiry would move by an
// hour or more, so the idle timeout still slides, an hour at a time. A change
// to the session's contents is always written.
func TestSessionStoreRefreshesAnUnchangedSessionAtMostHourly(t *testing.T) {
	t.Parallel()
	s, sqlDB := newTestSessionStore(t)
	start := time.Now()
	s.now = func() time.Time { return start }
	deadline := start.Add(sessionLifetimeCap)
	// Sixteen keys, so most encodings differ in order: two keys come out
	// identical about seven times in eight, which made this loop flaky.
	values := map[string]any{sessionKeyUserID: int64(1), sessionKeyCredential: "fingerprint"}
	for i := range 14 {
		values[fmt.Sprintf("pad-%02d", i)] = int64(i)
	}

	// Equal values encoded to different bytes, so the skip is decided on the
	// decoded session and not on the bytes.
	first := encodedSession(t, deadline, values)
	second := first
	for range 64 {
		if second = encodedSession(t, deadline, values); !bytes.Equal(first, second) {
			break
		}
	}
	if bytes.Equal(first, second) {
		t.Fatal("64 encodings of a sixteen-key map came out identical; the codec no longer shows the reordering this test needs")
	}

	expiry := start.Add(sessionIdleTimeout)
	if err := s.CommitCtx(requestCtx(), "idle", first, expiry); err != nil {
		t.Fatalf("CommitCtx(new) = %v", err)
	}

	commitLoaded := func(b []byte, at time.Time) {
		t.Helper()
		ctx := requestCtx()
		if _, found, err := s.FindCtx(ctx, "idle"); err != nil || !found {
			t.Fatalf("FindCtx() = found %v, err %v, want a hit", found, err)
		}
		if err := s.CommitCtx(ctx, "idle", b, at); err != nil {
			t.Fatalf("CommitCtx(loaded) = %v", err)
		}
	}

	commitLoaded(second, expiry.Add(59*time.Minute))
	if got := sessionExpiry(t, sqlDB, "idle"); got != expiry.Unix() {
		t.Errorf("expires_at after a refresh 59 minutes on = %d, want %d unchanged - an idle GET wrote the row", got, expiry.Unix())
	}

	slid := expiry.Add(time.Hour)
	commitLoaded(second, slid)
	if got := sessionExpiry(t, sqlDB, "idle"); got != slid.Unix() {
		t.Errorf("expires_at after a refresh an hour on = %d, want %d - the idle timeout stopped sliding", got, slid.Unix())
	}

	// A changed value, and the same number under another Go type (gob keeps
	// the type), are both changes and are written within the hour.
	retyped := map[string]any{}
	for k, v := range values {
		retyped[k] = v
	}
	retyped[sessionKeyUserID] = 1
	for i, b := range [][]byte{
		encodedSession(t, deadline, map[string]any{sessionKeyUserID: int64(1), sessionKeyCredential: "new fingerprint"}),
		encodedSession(t, deadline, retyped),
	} {
		commitLoaded(b, slid.Add(time.Duration(i+1)*time.Minute))
		data, _, _ := s.FindCtx(context.Background(), "idle")
		if !bytes.Equal(data, b) {
			t.Errorf("change %d committed within the hour was not written", i)
		}
	}
}

// TestAnIdleGetDoesNotWriteTheSession is #529 through the real router: a plain
// authenticated GET whose session is unchanged and was written less than an
// hour ago leaves the row alone. The row is backdated ten minutes first, so a
// write would show as expires_at moving forward.
func TestAnIdleGetDoesNotWriteTheSession(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	rec := postRegister(t, r, "bendahara@example.org", "kata-sandi-panjang")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", rec.Code, http.StatusCreated)
	}
	token := sessionCookie(rec)
	if _, err := sqlDB.Exec("UPDATE session SET expires_at = expires_at - 600 WHERE token = ?", token); err != nil {
		t.Fatalf("backdating the session: %v", err)
	}
	before := sessionExpiry(t, sqlDB, token)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token}) //nolint:gosec // request-side cookie carries name and value only
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/session status = %d, want %d", w.Code, http.StatusOK)
	}
	if after := sessionExpiry(t, sqlDB, token); after != before {
		t.Errorf("expires_at = %d after an idle GET, want %d - the GET wrote the session", after, before)
	}
}

// TestSessionStoreSweepsExpiredRowsAtMostHourly is #529's other half: the
// expired-session sweep rides on a commit, but no more than once an hour.
func TestSessionStoreSweepsExpiredRowsAtMostHourly(t *testing.T) {
	t.Parallel()
	s, sqlDB := newTestSessionStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	expired := func(token string) {
		t.Helper()
		if _, err := sqlDB.Exec("INSERT INTO session (token, data, expires_at) VALUES (?, x'00', ?)", token, now.Add(-time.Minute).Unix()); err != nil {
			t.Fatalf("inserting an expired session: %v", err)
		}
	}
	commit := func(token string) {
		t.Helper()
		if err := s.CommitCtx(requestCtx(), token, []byte("v"), now.Add(time.Hour)); err != nil {
			t.Fatalf("CommitCtx(%q) = %v", token, err)
		}
	}

	expired("old-1")
	commit("a")
	if n := sessionRowCount(t, sqlDB, "old-1"); n != 0 {
		t.Errorf("first commit left the expired row (rows = %d), want it swept", n)
	}

	expired("old-2")
	now = now.Add(59 * time.Minute)
	commit("b")
	if n := sessionRowCount(t, sqlDB, "old-2"); n != 1 {
		t.Errorf("a commit 59 minutes after the last sweep swept again (rows = %d), want the row left", n)
	}

	now = now.Add(time.Minute)
	commit("c")
	if n := sessionRowCount(t, sqlDB, "old-2"); n != 0 {
		t.Errorf("a commit an hour after the last sweep left the expired row (rows = %d), want it swept", n)
	}
}
