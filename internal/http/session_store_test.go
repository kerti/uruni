package http

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
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
