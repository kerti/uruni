package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// TestConcurrentRequestsOnOneSessionDoNotCollide is the regression test for
// the old delete-then-insert commit. scs marks every loaded session Modified
// while IdleTimeout is set, so each request re-commits the same token; under
// the old pair, two interleaved as DELETE / DELETE / INSERT / INSERT and the
// second INSERT tripped session.token's primary key, a 500 on an ordinary
// request. Two parallel fetches from one SPA is everyday, not exotic: it
// reproduced at 7 failures in 60 requests before UpsertSession made the write
// one statement.
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
	return newSessionStore(store.New(sqlDB)), sqlDB
}

// TestSessionStoreRoundTripsThroughThePlainInterface exercises Find, Commit and
// Delete, the non-ctx trio scs.Store requires. Production never calls them
// (scs prefers the Ctx forms), so this alone proves they forward correctly,
// not all three landing on one token.
func TestSessionStoreRoundTripsThroughThePlainInterface(t *testing.T) {
	t.Parallel()
	s, _ := newTestSessionStore(t)

	if err := s.Commit("a-token", []byte("session-data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Commit() = %v, want no error", err)
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

	if err := s.CommitCtx(context.Background(), "stale", []byte("d"), time.Now().Add(-time.Minute)); err != nil {
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
	if err := s.CommitCtx(ctx, "a-token", []byte("d"), time.Now().Add(time.Hour)); err == nil {
		t.Error("CommitCtx() on a closed database = nil, want an error")
	}
	if err := s.DeleteCtx(ctx, "a-token"); err == nil {
		t.Error("DeleteCtx() on a closed database = nil, want an error")
	}
}
