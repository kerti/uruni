package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

const (
	logEmail    = "bendahara.rahasia@example.org"
	logPassword = "correct-horse-battery"
)

// loggedRouter is a router with a registered treasurer (stubbed hashing, so
// lockout tests are quick) whose log lines land in the returned buffer at the
// given level. Requests run one at a time, so the buffer needs no lock.
func loggedRouter(t *testing.T, level slog.Level) (h http.Handler, logs *bytes.Buffer, backupDir string) {
	t.Helper()
	logs = &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level}))
	sqlDB := testStoreDB(t)
	au := auth.New(sqlDB, auth.WithPasswordFuncs(
		func(p string) (string, error) { return "stub:" + p, nil },
		func(p, encoded string) (bool, error) { return encoded == "stub:"+p, nil },
	))
	backupDir = t.TempDir()
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, logger, au, "https://kas.example.org", t.TempDir(), backupDir, nil)
	reg := postRegister(t, r, logEmail, logPassword)
	if reg.Code != http.StatusCreated {
		t.Fatalf("fixture register = %d (body: %s)", reg.Code, reg.Body.String())
	}
	return withSessionCookie{Handler: r, token: sessionCookie(reg)}, logs, backupDir
}

// logLine is the one log line containing every needle, failing when there is
// not exactly one.
func logLine(t *testing.T, logs *bytes.Buffer, needles ...string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(logs.String(), "\n") {
		ok := line != ""
		for _, n := range needles {
			ok = ok && strings.Contains(line, n)
		}
		if ok {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one log line with %q, got %d in:\n%s", needles, len(found), logs.String())
	}
	return found[0]
}

var requestIDAttr = regexp.MustCompile(`request_id=(\S+)`)

func requestIDOf(t *testing.T, line string) string {
	t.Helper()
	m := requestIDAttr.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no request_id in %q", line)
	}
	return m[1]
}

// #519: the request line and a handler's error line for the same request carry
// the same request_id; the next request gets another; a client's X-Request-Id
// is not adopted.
func TestRequestIDJoinsTheRequestLineAndTheHandlerLine(t *testing.T) {
	t.Parallel()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	sqlDB := testStoreDB(t)
	h := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, logger, auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil)
	if err := sqlDB.Close(); err != nil { // GET /api/session then fails in its handler and logs an error
		t.Fatal(err)
	}

	serve := func() string {
		logs.Reset()
		req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		req.Header.Set("X-Request-Id", "attacker-chosen")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("GET /api/session on a closed store = %d, want 500", rec.Code)
		}
		handlerLine := logLine(t, logs, "level=ERROR", "counting users")
		requestLine := logLine(t, logs, "msg=request", "status=500")
		id := requestIDOf(t, handlerLine)
		if got := requestIDOf(t, requestLine); got != id {
			t.Errorf("request line id %q != handler line id %q", got, id)
		}
		if strings.Contains(logs.String(), "attacker-chosen") {
			t.Errorf("an incoming X-Request-Id reached the log: %s", logs.String())
		}
		if len(id) != 12 {
			t.Errorf("request_id = %q, want 12 hex characters", id)
		}
		if strings.Contains(requestLine, "ip=") {
			t.Errorf("a request line carries a client address: %q", requestLine)
		}
		return id
	}
	first, second := serve(), serve()
	if first == second {
		t.Errorf("two requests share request_id %q", first)
	}
}

func panicStack(t *testing.T, v any) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(v) })
	return requestID(logger)(requestLogger(logger)(recoverer(logger)(boom))), logs
}

// #519: a panic under /api answers with the JSON envelope, logs the stack at
// ERROR with the request_id, and the request line still shows 500. The panic
// value (here a string built from a member's name) is reduced to its type.
func TestPanicUnderAPIAnswersTheEnvelopeAndLogsTheStack(t *testing.T) {
	t.Parallel()
	h, logs := panicStack(t, "member Siti Aminah owes 150000")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/anything", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "internal_error" {
		t.Fatalf("body = %q, want the internal_error envelope (err %v)", rec.Body.String(), err)
	}
	panicLine := logLine(t, logs, "level=ERROR", "panic serving request")
	if !strings.Contains(panicLine, "stack=") || !strings.Contains(panicLine, "reqlog_test.go") {
		t.Errorf("panic line has no stack naming the panicking frame: %q", panicLine)
	}
	if strings.Contains(logs.String(), "Siti") {
		t.Errorf("the panic value reached the log: %s", logs.String())
	}
	requestLine := logLine(t, logs, "msg=request")
	if !strings.Contains(requestLine, "status=500") {
		t.Errorf("request line = %q, want status=500", requestLine)
	}
	if requestIDOf(t, panicLine) != requestIDOf(t, requestLine) {
		t.Errorf("panic and request lines carry different ids:\n%s", logs.String())
	}
}

func TestPanicOutsideAPIAnswersPlain500(t *testing.T) {
	t.Parallel()
	h, logs := panicStack(t, "boom")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report/abc", nil))

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("got %d %q, want a plain 500", rec.Code, rec.Body.String())
	}
	logLine(t, logs, "level=ERROR", "panic serving request")
}

// http.ErrAbortHandler is how a handler aborts a response on purpose; it must
// reach net/http, which closes the connection quietly, and is not logged.
func TestRecovererRepanicsErrAbortHandler(t *testing.T) {
	t.Parallel()
	h, logs := panicStack(t, http.ErrAbortHandler)

	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler re-panicked", v)
		}
		if strings.Contains(logs.String(), "panic serving request") {
			t.Errorf("an intentional abort was logged as a panic: %s", logs.String())
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/x", nil))
}

// #519: the probe's request line is debug.
func TestHealthzRequestLineIsDebug(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		level slog.Level
		want  bool
	}{"info": {slog.LevelInfo, false}, "debug": {slog.LevelDebug, true}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, logs, _ := loggedRouter(t, tc.level)
			logs.Reset()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if got := strings.Contains(logs.String(), "path=/healthz"); got != tc.want {
				t.Errorf("/healthz line present = %v at %s, want %v: %q", got, name, tc.want, logs.String())
			}
		})
	}
}

// A security event: its line carries the event, the level and the client
// address (httptest's 192.0.2.1), and the fixture's identifying values appear
// nowhere in the log.
func wantEvent(t *testing.T, logs *bytes.Buffer, event, level string, extra ...string) {
	t.Helper()
	line := logLine(t, logs, append([]string{"event=" + event, "level=" + level, "ip=192.0.2.1", "request_id="}, extra...)...)
	if strings.Contains(line, "msg=request") {
		t.Errorf("%s is on a request line: %q", event, line)
	}
}

func wantNoSecrets(t *testing.T, logs *bytes.Buffer, secrets ...string) {
	t.Helper()
	for _, s := range append([]string{logEmail, logPassword, "not-the-password"}, secrets...) {
		if strings.Contains(logs.String(), s) {
			t.Errorf("log leaks %q:\n%s", s, logs.String())
		}
	}
}

func TestLoginSecurityEvents(t *testing.T) {
	t.Parallel()
	h, logs, _ := loggedRouter(t, slog.LevelInfo)

	logs.Reset()
	if rec := postLoginFrom(t, h, logEmail, "not-the-password", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	wantEvent(t, logs, "login_failed", "INFO")

	logs.Reset()
	if rec := postLoginFrom(t, h, logEmail, logPassword, ""); rec.Code != http.StatusOK {
		t.Fatalf("right password = %d", rec.Code)
	}
	wantEvent(t, logs, "login_succeeded", "INFO")

	// A stranger fills his own budget (a fresh address, no device cookie), then is refused.
	logs.Reset()
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		postLoginFrom(t, h, logEmail, "not-the-password", "198.51.100.7")
	}
	logs.Reset()
	if rec := postLoginFrom(t, h, logEmail, "not-the-password", "198.51.100.7"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit = %d", rec.Code)
	}
	logLine(t, logs, "event=login_locked_out", "level=WARN", "ip=198.51.100.7")
	wantNoSecrets(t, logs)
}

func TestRotatingTheReportLinkIsLoggedWithoutTheSlug(t *testing.T) {
	t.Parallel()
	h, logs, _ := loggedRouter(t, slog.LevelInfo)
	old := setUpFund(t, h).Fund

	logs.Reset()
	rec := postReplaceSlug(h)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d", rec.Code)
	}
	var fund fundResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fund); err != nil {
		t.Fatal(err)
	}
	wantEvent(t, logs, "report_link_rotated", "INFO")
	wantNoSecrets(t, logs, old.ReportSlug, fund.ReportSlug)
}

func TestBackupDownloadsAreLogged(t *testing.T) {
	t.Parallel()
	h, logs, backupDir := loggedRouter(t, slog.LevelInfo)
	setUpFund(t, h)

	logs.Reset()
	zipBytes := downloadRealBackupZip(t, h)
	wantEvent(t, logs, "backup_downloaded", "INFO", "kind=fresh")

	name := backup.BuildDumpName(time.Now(), backup.KindDaily, backup.FormatVersion, "eeeeeeeeeeeeeeee")
	writeTestDumpFile(t, backupDir, name, zipBytes)
	logs.Reset()
	if rec := getStoredBackup(t, h, name); rec.Code != http.StatusOK {
		t.Fatalf("stored download = %d", rec.Code)
	}
	wantEvent(t, logs, "backup_downloaded", "INFO", "kind=stored", "name="+name)
	wantNoSecrets(t, logs)
}

func TestRestoreSecurityEvents(t *testing.T) {
	t.Parallel()
	h, logs, _ := loggedRouter(t, slog.LevelInfo)
	setUpFund(t, h)
	zipBytes := downloadRealBackupZip(t, h)

	logs.Reset()
	inspected := postRestoreInspect(t, h, zipBytes)
	if inspected.Code != http.StatusOK {
		t.Fatalf("inspect = %d (body: %s)", inspected.Code, inspected.Body.String())
	}
	wantEvent(t, logs, "restore_inspected", "INFO")
	token := decodeInspectToken(t, inspected)

	logs.Reset()
	if rec := postRestoreConfirm(t, h, token, "not-the-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password confirm = %d", rec.Code)
	}
	wantEvent(t, logs, "restore_confirm_refused", "WARN", "reason=wrong_password")

	logs.Reset()
	if rec := postRestoreConfirm(t, h, token, logPassword); rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d (body: %s)", rec.Code, rec.Body.String())
	}
	wantEvent(t, logs, "restore_confirmed", "WARN")
	wantNoSecrets(t, logs)
}
