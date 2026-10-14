package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests put the real router behind a real http.Server whose base
// ReadTimeout and WriteTimeout are tiny, then make the request outlast them.
// An httptest.ResponseRecorder cannot do this job: it has no connection, so
// http.NewResponseController would answer ErrNotSupported and the deadline
// extension would go untested. What is under test is that
// ResponseController reaches the connection through the router's real wrapper
// stack (requestLogger's chi writer, scs's session writer), not that
// SetWriteDeadline works.
//
// Each case has a control on an ordinary route with the same delay that must
// fail, so a pass cannot mean "the timeout never fired".

// baseTimeout is the tiny server-wide deadline; delay is how long a request
// outlasts it. The passing side has one clock dependency: the request must
// reach extendDeadlines before baseTimeout runs out. 150ms flaked once under
// -race with the whole package running in parallel; 500ms leaves room for a
// stalled CPU and still fails the control well inside delay.
const (
	baseTimeout = 500 * time.Millisecond
	delay       = 4 * baseTimeout
)

// newTimeoutServer serves h with baseTimeout read and write deadlines.
func newTimeoutServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = baseTimeout
	srv.Config.WriteTimeout = baseTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// noKeepAliveClient dials fresh each request, so one case's timed-out
// connection is never reused by the next.
func noKeepAliveClient() *http.Client {
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
}

// slowClock is a router clock that sleeps once armed. downloadBackup reads the
// clock after building the zip and before writing it, and balances reads it
// before answering, so arming it makes the response start after the server's
// base WriteTimeout has passed.
type slowClock struct{ armed atomic.Bool }

func (c *slowClock) now() time.Time {
	if c.armed.Load() {
		time.Sleep(delay)
	}
	return time.Now()
}

func TestBackupDownloadOutlivesTheServersWriteTimeout(t *testing.T) {
	t.Parallel()
	clock := &slowClock{}
	sqlDB := testStoreDB(t)
	h := authedRouterAt(t, sqlDB, clock.now)
	setUpFund(t, h)
	srv := newTimeoutServer(t, h)
	clock.armed.Store(true)

	// Control: an ordinary route, same delay, loses its response.
	if resp, err := noKeepAliveClient().Get(srv.URL + "/api/balances"); err == nil {
		_, readErr := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && readErr == nil {
			t.Fatal("control GET /api/balances answered after the base WriteTimeout, so this test would pass without the extension")
		}
	}

	resp, err := noKeepAliveClient().Get(srv.URL + "/api/backup")
	if err != nil {
		t.Fatalf("GET /api/backup past the base WriteTimeout = %v, want the response", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("GET /api/backup past the base WriteTimeout = %d, %d bytes, err %v; want 200 and a zip", resp.StatusCode, len(body), err)
	}
}

// trickle returns a request body that sends first, waits delay, then sends
// rest: a slow phone upload, long enough to cross the base ReadTimeout.
func trickle(first, rest string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(first))
		time.Sleep(delay)
		_, err := pw.Write([]byte(rest))
		_ = pw.CloseWithError(err)
	}()
	return pr
}

func TestRestoreUploadOutlivesTheServersReadTimeout(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	h := authedRouterAt(t, sqlDB, time.Now)
	setUpFund(t, h)
	srv := newTimeoutServer(t, h)

	// Control: an ordinary JSON route fed the same way never reads the whole body,
	// so a correct login does not come back 200.
	ctlReq, err := http.NewRequest(http.MethodPost, srv.URL+"/api/login", trickle(`{"email":"treasurer@example.org",`, `"password":"`+fixturePassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctlReq.Header.Set("Content-Type", "application/json")
	if resp, err := noKeepAliveClient().Do(ctlReq); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("control POST /api/login read a body slower than ReadTimeout, so this test would pass without the extension")
		}
	}

	// The multipart body, split mid-file so the pause lands inside the upload.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "backup.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("not a zip file at all"))
	_ = mw.Close()
	whole := buf.String()
	split := strings.Index(whole, "not a zip") + len("not a")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/restore/inspect", trickle(whole[:split], whole[split:]))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := noKeepAliveClient().Do(req)
	if err != nil {
		t.Fatalf("POST /api/restore/inspect slower than the base ReadTimeout = %v, want the handler's answer", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The junk is not a backup, so the handler's own 422 is the proof it read the
	// whole body and could still answer after both base deadlines had passed.
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity || envelope.Error.Code != "restore_invalid_file" {
		t.Errorf("POST /api/restore/inspect = %d %q, want %d restore_invalid_file", resp.StatusCode, envelope.Error.Code, http.StatusUnprocessableEntity)
	}
}

func TestReceiptUploadOutlivesTheServersReadTimeout(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	h := authedRouterAt(t, sqlDB, time.Now)
	txnID := setUpTransactionForReceipt(t, h, setUpFund(t, h))
	srv := newTimeoutServer(t, h)

	// The same trickle as the restore test; TestRestoreUploadOutlivesTheServersReadTimeout
	// holds the control that a short-deadline route cannot read it.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "receipt.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("not an image at all"))
	_ = mw.Close()
	whole := buf.String()
	split := strings.Index(whole, "not an image") + len("not an")

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/transactions/%d/receipts", srv.URL, txnID), trickle(whole[:split], whole[split:]))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := noKeepAliveClient().Do(req)
	if err != nil {
		t.Fatalf("POST receipt slower than the base ReadTimeout = %v, want the handler's answer", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Not an image, so the handler's own 415 proves it read the whole body.
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if resp.StatusCode != http.StatusUnsupportedMediaType || envelope.Error.Code != "unsupported_media_type" {
		t.Errorf("POST receipt = %d %q, want %d unsupported_media_type", resp.StatusCode, envelope.Error.Code, http.StatusUnsupportedMediaType)
	}
}

// With no connection under it (a recorder), the extension cannot apply. The
// request must still be served, and the operator told, because the symptom of
// a broken Unwrap chain would otherwise be a download cut off far from here.
func TestExtendDeadlinesWarnsWhenTheConnectionIsOutOfReach(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	reached := false
	h := extendDeadlines(logger, time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !reached {
		t.Error("handler not reached when the deadline could not be set")
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "could not extend the read deadline") {
		t.Errorf("log = %q, want a warning that the deadline could not be extended", logs.String())
	}
}
