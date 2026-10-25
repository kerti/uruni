package http

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// #601: the restore promises that everyone signed in is signed out. A request
// in flight on another device loaded its session before the restore and commits
// after it; the commit must not put the deleted row back (the fingerprint of
// #386 cannot catch this: a restore keeps the login, so it still matches).
func TestARequestInFlightCannotResurrectASessionAcrossARestore(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	r := New(Deps{Assets: testAssets(), Build: testBuild, Ledger: ledger.New(sqlDB), Queries: store.New(sqlDB), DB: sqlDB, Logger: testLogger(), Auth: auth.New(sqlDB), BaseURL: "", UploadsDir: t.TempDir(), BackupDir: t.TempDir()})

	reg := postRegister(t, r, userEmail, fixturePassword)
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	confirming := withSessionCookie{Handler: r, token: sessionCookie(reg)}
	setUpFund(t, confirming)
	other := loginWith(t, r, userEmail, fixturePassword, "198.51.100.7")
	if other.Code != http.StatusOK {
		t.Fatalf("second login = %d, want %d", other.Code, http.StatusOK)
	}
	otherToken := sessionCookie(other)

	inspected := postRestoreInspect(t, confirming, downloadRealBackupZip(t, confirming))
	if inspected.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect = %d, want %d (body: %s)", inspected.Code, http.StatusOK, inspected.Body.String())
	}

	// The other device's request is held mid-flight, past the session gate.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	body := &signalBody{r: pr, started: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, "/api/members", body)
	req.RemoteAddr = net.JoinHostPort("198.51.100.99", "1234")
	req.AddCookie(requestCookie("session", otherToken))
	held := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(held, req)
	}()
	select {
	case <-body.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request never reached its handler")
	}

	if rec := postRestoreConfirm(t, confirming, decodeInspectToken(t, inspected), fixturePassword); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var rows int
	if err := sqlDB.QueryRow("SELECT count(*) FROM session").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("session rows after the restore = %d (err %v), want 0", rows, err)
	}

	// The held request finishes; scs tries to write its session back.
	if _, err := pw.Write([]byte("{}")); err != nil {
		t.Fatalf("releasing the held request: %v", err)
	}
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request never finished")
	}
	requireSessionCommitRan(t, held, otherToken)

	if err := sqlDB.QueryRow("SELECT count(*) FROM session").Scan(&rows); err != nil || rows != 0 {
		t.Errorf("session rows after the held request = %d (err %v), want 0 - its commit resurrected a session", rows, err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/fund", nil)
	req.AddCookie(requestCookie("session", otherToken))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund with the held request's cookie = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
