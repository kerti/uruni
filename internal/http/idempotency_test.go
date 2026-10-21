package http

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ADR-043 on the one route #634 keys, POST /api/transactions.

const (
	testKey      = "0123456789abcdef0123456789abcdef"
	testOtherKey = "fedcba9876543210fedcba9876543210"
)

// keyedFixture is a fund and a router whose clock the test moves.
type keyedFixture struct {
	r     http.Handler
	sqlDB *sql.DB
	setup setupResponse
	cash  int64
	now   *time.Time
	mu    *sync.Mutex
}

func newKeyedFixture(t *testing.T) keyedFixture {
	t.Helper()
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	mu := &sync.Mutex{}
	sqlDB := testStoreDB(t)
	r := authedRouterAt(t, sqlDB, func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	setup := setUpFund(t, r)
	return keyedFixture{r: r, sqlDB: sqlDB, setup: setup, cash: setup.CashAccountID(t), now: &now, mu: mu}
}

func (f keyedFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.now = f.now.Add(d)
}

func (f keyedFixture) body(amount int64, note string) string {
	return fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in","amount":%d,"occurred_on":"2026-08-12","note":%q}`,
		f.cash, f.setup.MainPurposeID, amount, note)
}

func (f keyedFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.sqlDB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s = %v, want no error", table, err)
	}
	return n
}

// transactions is the ledger rows the fund holds beyond its opening balances.
func (f keyedFixture) transactions(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.sqlDB.QueryRow(`SELECT count(*) FROM "transaction" WHERE kind = 'normal'`).Scan(&n); err != nil {
		t.Fatalf("counting transactions = %v, want no error", err)
	}
	return n
}

func (f keyedFixture) post(path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func TestKeyedTransactionReplayAnswersTheFirstResultAndPostsNothing(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	body := f.body(75_000, "Iuran \"mingguan\" <rt> & kas")

	first := f.post("/api/transactions", testKey, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}
	if got := first.Header().Get("Idempotent-Replayed"); got != "" {
		t.Errorf("first Idempotent-Replayed = %q, want none", got)
	}

	f.advance(40 * time.Second)
	again := f.post("/api/transactions", testKey, body)
	if again.Code != http.StatusCreated {
		t.Fatalf("repeated POST = %d, want the first's %d (body: %s)", again.Code, http.StatusCreated, again.Body.String())
	}
	if got := again.Header().Get("Idempotent-Replayed"); got != "true" {
		t.Errorf("repeated Idempotent-Replayed = %q, want %q", got, "true")
	}
	if again.Body.String() != first.Body.String() {
		t.Errorf("repeated body = %s, want the first's %s", again.Body.String(), first.Body.String())
	}
	if n := f.transactions(t); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
}

func TestKeyedTransactionSameKeyOtherBodyIs409(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	if rec := f.post("/api/transactions", testKey, f.body(75_000, "a")); rec.Code != http.StatusCreated {
		t.Fatalf("first POST = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec := f.post("/api/transactions", testKey, f.body(80_000, "a"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST with the key and another body = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "idempotency_key_reused" {
		t.Errorf("error code = %q, want %q", got, "idempotency_key_reused")
	}
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Error("a refused reuse carried Idempotent-Replayed")
	}
	if n := f.transactions(t); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
}

// A retry from a client that orders its keys or spaces its JSON differently is
// still the same request.
func TestKeyedTransactionCanonicalBodyIsTheSameRequest(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	if rec := f.post("/api/transactions", testKey, f.body(75_000, "a")); rec.Code != http.StatusCreated {
		t.Fatalf("first POST = %d (body: %s)", rec.Code, rec.Body.String())
	}

	reordered := fmt.Sprintf("{ \"note\": \"a\",\n \"occurred_on\": \"2026-08-12\", \"amount\": 75000, \"direction\": \"in\", \"purpose_id\": %d, \"account_id\": %d }",
		f.setup.MainPurposeID, f.cash)
	rec := f.post("/api/transactions", testKey, reordered)
	if rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("reordered retry = %d, replayed %q, want a %d replay (body: %s)", rec.Code, rec.Header().Get("Idempotent-Replayed"), http.StatusCreated, rec.Body.String())
	}
	if n := f.transactions(t); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
}

func TestKeyedTransactionWithoutAHeaderIsUnprotected(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	for range 2 {
		if rec := f.post("/api/transactions", "", f.body(75_000, "a")); rec.Code != http.StatusCreated {
			t.Fatalf("POST without a key = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
	}
	if n := f.transactions(t); n != 2 {
		t.Errorf("transactions = %d, want 2: with no key a repeat is a second row", n)
	}
	if n := f.count(t, "idempotency_key"); n != 0 {
		t.Errorf("idempotency_key rows = %d, want 0", n)
	}
}

func TestKeyedTransactionMalformedKeyIs400AndWritesNothing(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	for name, key := range map[string]string{
		"too short":      strings.Repeat("k", 15),
		"too long":       strings.Repeat("k", 65),
		"a space":        "0123456789abcdef 0123456789abcdef",
		"a slash":        "0123456789abcdef/0123456789abcdef",
		"a non-ASCII":    "0123456789abcdef0123456789abcd\u00e9",
		"a comma list":   testKey + ", " + testOtherKey,
		"an empty value": " ",
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.post("/api/transactions", key, f.body(75_000, "a"))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != "invalid_idempotency_key" {
				t.Errorf("error code = %q, want %q", got, "invalid_idempotency_key")
			}
		})
	}

	// Two headers are ambiguous, so they are malformed too.
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", strings.NewReader(f.body(75_000, "a")))
	req.Header.Add("Idempotency-Key", testKey)
	req.Header.Add("Idempotency-Key", testOtherKey)
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST with two keys = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if n := f.transactions(t); n != 0 {
		t.Errorf("transactions = %d, want 0", n)
	}
}

// The header is read on a keyed route only: on any other a malformed value is
// ignored and a repeated valid one protects nothing.
func TestAnUnkeyedRouteIgnoresTheHeader(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)

	for _, key := range []string{"short", testKey, testKey} {
		rec := f.post("/api/members", key, `{"name":"Jane"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/members with key %q = %d, want %d (body: %s)", key, rec.Code, http.StatusCreated, rec.Body.String())
		}
		if rec.Header().Get("Idempotent-Replayed") != "" {
			t.Error("an unkeyed route carried Idempotent-Replayed")
		}
	}
	if n := f.count(t, "member"); n != 3 {
		t.Errorf("members = %d, want 3", n)
	}
	if n := f.count(t, "idempotency_key"); n != 0 {
		t.Errorf("idempotency_key rows = %d, want 0", n)
	}
}

// Only a commit records: a refusal leaves no key, so the retry is judged afresh.
func TestKeyedTransactionRefusalIsNotRecorded(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)

	rec := f.post("/api/transactions", testKey, f.body(0, "a")) // amount must be positive
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST of a zero amount = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if n := f.count(t, "idempotency_key"); n != 0 {
		t.Fatalf("idempotency_key rows = %d after a refusal, want 0", n)
	}

	// The same key now carries a different, good body: not a 409, a first write.
	rec = f.post("/api/transactions", testKey, f.body(75_000, "a"))
	if rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("POST after a refusal = %d, replayed %q, want a fresh %d (body: %s)", rec.Code, rec.Header().Get("Idempotent-Replayed"), http.StatusCreated, rec.Body.String())
	}

	// Not JSON at all: 400 from the handler, nothing recorded under the new key.
	rec = f.post("/api/transactions", testOtherKey, "not json")
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != "invalid_json" {
		t.Errorf("POST of a non-JSON body = %d, want 400 invalid_json", rec.Code)
	}
	if n := f.count(t, "idempotency_key"); n != 1 {
		t.Errorf("idempotency_key rows = %d, want 1", n)
	}
}

func TestKeyedTransactionBodyCapIs413(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	rec := f.post("/api/transactions", testKey, `{"note":"`+strings.Repeat("x", maxJSONBodyBytes)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge || decodeError(t, rec).Code != "request_too_large" {
		t.Errorf("POST of an oversized body = %d, want 413 request_too_large (body: %s)", rec.Code, rec.Body.String())
	}
}

// 24 hours from the commit: one second before, the record answers; at the
// instant it expires, the same request is a new one.
func TestKeyedTransactionRecordLivesTwentyFourHours(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	body := f.body(75_000, "a")
	if rec := f.post("/api/transactions", testKey, body); rec.Code != http.StatusCreated {
		t.Fatalf("first POST = %d (body: %s)", rec.Code, rec.Body.String())
	}

	f.advance(24*time.Hour - time.Second)
	if rec := f.post("/api/transactions", testKey, body); rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("a second before expiry: replayed %q, want %q", rec.Header().Get("Idempotent-Replayed"), "true")
	}

	f.advance(time.Second)
	rec := f.post("/api/transactions", testKey, body)
	if rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "" {
		t.Errorf("at expiry: %d, replayed %q, want a fresh %d", rec.Code, rec.Header().Get("Idempotent-Replayed"), http.StatusCreated)
	}
	if n := f.transactions(t); n != 2 {
		t.Errorf("transactions = %d, want 2", n)
	}
}

// The replay sits behind the session gate like the write it repeats.
func TestKeyedTransactionReplayIsBehindTheSessionGate(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Idempotency-Key", testKey)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("keyed POST with no session = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// Twenty requests on one key at once: one row, nineteen replays, no third
// answer. Run under -race by make check.
func TestKeyedTransactionConcurrentTwinsPostOnce(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	body := f.body(75_000, "a")

	const twins = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created int
		replays int
		other   []string
	)
	for range twins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := f.post("/api/transactions", testKey, body)
			mu.Lock()
			defer mu.Unlock()
			if rec.Code == http.StatusCreated {
				created++
			} else {
				other = append(other, fmt.Sprintf("%d %s", rec.Code, rec.Body.String()))
			}
			if rec.Header().Get("Idempotent-Replayed") == "true" {
				replays++
			}
		}()
	}
	wg.Wait()

	if len(other) != 0 {
		t.Fatalf("%d twins were not answered 201, first: %s", len(other), other[0])
	}
	if replays != twins-1 {
		t.Errorf("replays = %d, want %d", replays, twins-1)
	}
	if n := f.transactions(t); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
}

// requestFingerprint's contract (ADR-043): what is the same request and what is
// not.
func TestRequestFingerprint(t *testing.T) {
	t.Parallel()
	base := requestFingerprint("POST", "/api/transactions", []byte(`{"a":1,"b":"x"}`))
	if len(base) != 64 {
		t.Fatalf("fingerprint = %q, want 64 hex characters", base)
	}

	same := map[string][]byte{
		"keys in another order": []byte(`{"b":"x","a":1}`),
		"whitespace":            []byte("{ \"a\" : 1,\n\t\"b\" : \"x\" }\n"),
		"an escaped character":  []byte(`{"a":1,"b":"\u0078"}`),
	}
	for name, body := range same {
		if got := requestFingerprint("POST", "/api/transactions", body); got != base {
			t.Errorf("%s: fingerprint differs, want the same request", name)
		}
	}

	different := map[string]string{
		"a number written another way": requestFingerprint("POST", "/api/transactions", []byte(`{"a":1.0,"b":"x"}`)),
		"another value":                requestFingerprint("POST", "/api/transactions", []byte(`{"a":2,"b":"x"}`)),
		"another path":                 requestFingerprint("POST", "/api/transactions/7/purpose-correction", []byte(`{"a":1,"b":"x"}`)),
		"another id in the path":       requestFingerprint("POST", "/api/transactions/8/purpose-correction", []byte(`{"a":1,"b":"x"}`)),
		"another method":               requestFingerprint("PATCH", "/api/transactions", []byte(`{"a":1,"b":"x"}`)),
		"trailing bytes":               requestFingerprint("POST", "/api/transactions", []byte(`{"a":1,"b":"x"} x`)),
		"not JSON":                     requestFingerprint("POST", "/api/transactions", []byte(`{"a":1,`)),
		"an empty body":                requestFingerprint("POST", "/api/transactions", nil),
	}
	seen := map[string]string{base: "the base request"}
	for name, got := range different {
		if prev, dup := seen[got]; dup {
			t.Errorf("%s has the same fingerprint as %s, want a different request", name, prev)
		}
		seen[got] = name
	}

	// Two ids in the path are two requests even with one body.
	if requestFingerprint("POST", "/api/transactions/7/purpose-correction", []byte(`{}`)) == requestFingerprint("POST", "/api/transactions/8/purpose-correction", []byte(`{}`)) {
		t.Error("the path's {id} did not change the fingerprint")
	}
}
