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
// the delete-then-insert commit this store used to do. scs marks every
// loaded session Modified while IdleTimeout is set, so each of these
// requests re-commits the same token; under the old pair, two of them
// interleaved as DELETE / DELETE / INSERT / INSERT and the second INSERT
// tripped session.token's primary key, answering one perfectly ordinary
// request with a 500. Two parallel fetches from one SPA is the everyday
// shape of this, not an exotic one - it reproduced at 7 failures in 60
// requests before UpsertSession made the write a single statement.
func TestConcurrentRequestsOnOneSessionDoNotCollide(t *testing.T) {
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
			// An inbound request cookie: only Name and Value ever cross
			// the wire from a browser, so the flags gosec wants here have
			// nowhere to go. The flags that matter are asserted on the
			// response in register_test.go.
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

	// One cookie must still mean exactly one row, however many requests
	// rewrote it.
	var rows int
	if err := sqlDB.QueryRow("SELECT count(*) FROM session").Scan(&rows); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if rows != 1 {
		t.Errorf("session rows = %d, want 1", rows)
	}
}

// newTestSessionStore returns the store over a real migrated database. The
// tests below drive scs.Store's own contract directly, because the arms
// they cover - an unknown token, an expired row, a database that has gone
// away - are ones a request through the router never produces on purpose.
func newTestSessionStore(t *testing.T) (*sessionStore, *sql.DB) {
	t.Helper()
	sqlDB := testStoreDB(t)
	return newSessionStore(store.New(sqlDB)), sqlDB
}

// TestSessionStoreRoundTripsThroughThePlainInterface exercises Find, Commit
// and Delete - the non-ctx trio scs.Store requires for the field this store
// is assigned to. They are never called in production (scs prefers the Ctx
// forms), so this is the only thing that proves they forward correctly
// rather than, say, all three landing on the same token.
func TestSessionStoreRoundTripsThroughThePlainInterface(t *testing.T) {
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

// TestSessionStoreFindReportsAMissOnAnUnknownOrExpiredToken is scs's
// Store.Find contract: "not found" is not an error, and a row past its idle
// window reads as absent even before the lazy sweep removes it - otherwise
// an expired cookie would keep working until some later request happened to
// commit.
func TestSessionStoreFindReportsAMissOnAnUnknownOrExpiredToken(t *testing.T) {
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

	// The sweep rides on the commit above, so the expired row is gone from
	// the table too, not merely filtered out of the read.
	var rows int
	if err := sqlDB.QueryRow("SELECT count(*) FROM session WHERE token = 'stale'").Scan(&rows); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if rows != 0 {
		t.Errorf("expired session rows = %d, want 0 - the lazy sweep did not run", rows)
	}
}

// TestSessionStoreSurfacesADatabaseError: every arm has to return the error
// rather than swallow it into a silent "no session," which would log the
// treasurer out with no explanation anywhere whenever the database is
// unreachable.
func TestSessionStoreSurfacesADatabaseError(t *testing.T) {
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
