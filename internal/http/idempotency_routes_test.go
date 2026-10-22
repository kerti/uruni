package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ADR-043 on the routes keyed after POST /api/transactions, and on
// the writes beside them that stay unkeyed. idempotency_test.go holds the
// middleware's own cases (fingerprint, malformed keys, retention, twins) on
// POST /api/transactions; these show each new route is behind it.

func (f keyedFixture) send(method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

// createdID is the id of a created resource's reply: "id", or "purpose_id" for
// an envelope, which is named by its purpose.
func createdID(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	var got struct {
		ID        int64 `json:"id"`
		PurposeID int64 `json:"purpose_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ID+got.PurposeID == 0 {
		t.Fatalf("reply %q has no id (%v)", rec.Body.String(), err)
	}
	return max(got.ID, got.PurposeID)
}

// create is an unkeyed write that must succeed, for the state a test starts
// from; it answers the new id.
func (f keyedFixture) create(t *testing.T, path, body string) int64 {
	t.Helper()
	rec := f.send(http.MethodPost, path, "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s = %d, want %d (body: %s)", path, rec.Code, http.StatusCreated, rec.Body.String())
	}
	return createdID(t, rec)
}

// replay sends the request under testKey, then again 40 seconds later, and
// checks what every keyed route owes: the first is a plain success with the
// route's own status, the second is that status again with Idempotent-Replayed
// and the very same body. It answers the first.
func (f keyedFixture) replay(t *testing.T, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	first := f.send(http.MethodPost, path, testKey, body)
	if first.Code != status {
		t.Fatalf("first POST %s = %d, want %d (body: %s)", path, first.Code, status, first.Body.String())
	}
	if got := first.Header().Get("Idempotent-Replayed"); got != "" {
		t.Errorf("first Idempotent-Replayed = %q, want none", got)
	}

	f.advance(40 * time.Second)
	again := f.send(http.MethodPost, path, testKey, body)
	if again.Code != status {
		t.Fatalf("repeated POST %s = %d, want the first's %d (body: %s)", path, again.Code, status, again.Body.String())
	}
	if got := again.Header().Get("Idempotent-Replayed"); got != "true" {
		t.Errorf("repeated Idempotent-Replayed = %q, want %q", got, "true")
	}
	if again.Body.String() != first.Body.String() {
		t.Errorf("repeated body = %s, want the first's %s", again.Body.String(), first.Body.String())
	}
	return first
}

// balance is the fund's recorded balance, summed from the ledger.
func (f keyedFixture) balance(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := f.sqlDB.QueryRow(`SELECT coalesce(sum(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0) FROM "transaction"`).Scan(&n); err != nil {
		t.Fatalf("summing the ledger = %v, want no error", err)
	}
	return n
}

func (f keyedFixture) wantCount(t *testing.T, table string, want int) {
	t.Helper()
	if n := f.count(t, table); n != want {
		t.Errorf("%s rows = %d, want %d", table, n, want)
	}
}

// fund puts an ordinary entry into Kas Utama, unkeyed, for a route that needs
// something to move.
func (f keyedFixture) fund(t *testing.T, amount int64) {
	t.Helper()
	f.create(t, "/api/transactions", f.body(amount, "modal"))
}

func TestKeyedDuesPaymentReplayAnswersTheWholeArrayAndPostsNoSecondSet(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	member := f.create(t, "/api/members", `{"name":"Jane"}`)
	body := fmt.Sprintf(`{"account_id":%d,"member_id":%d,"occurred_on":"2026-08-12","note":"Iuran \"Q3\" <rt>",`+
		`"periods":[{"dues_period":"2026-06","amount":50000},{"dues_period":"2026-07","amount":50000},{"dues_period":"2026-08","amount":60000}]}`,
		f.cash, member)

	first := f.replay(t, "/api/dues-payments", body, http.StatusCreated)

	var rows []json.RawMessage
	if err := json.Unmarshal(first.Body.Bytes(), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("reply = %s (%v), want an array of 3 rows", first.Body.String(), err)
	}
	if n := f.count(t, `"transaction" WHERE kind = 'dues'`); n != 3 {
		t.Errorf("dues rows = %d, want 3", n)
	}
	if got := f.balance(t); got != 160_000 {
		t.Errorf("recorded balance = %d, want 160000", got)
	}
}

func TestKeyedTransferReplayPostsNoSecondPair(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	f.fund(t, 500_000)
	body := fmt.Sprintf(`{"purpose_id":%d,"from_account_id":%d,"to_account_id":%d,"amount":200000,"occurred_on":"2026-08-12","note":"Setor"}`,
		f.setup.MainPurposeID, f.cash, f.setup.BankAccountID(t))

	f.replay(t, "/api/transfers", body, http.StatusCreated)

	f.wantCount(t, "transfer", 1)
	if n := f.count(t, `"transaction" WHERE kind = 'transfer'`); n != 2 {
		t.Errorf("transfer legs = %d, want 2", n)
	}
	if got := f.balance(t); got != 500_000 {
		t.Errorf("recorded balance = %d, want 500000: a transfer moves no total", got)
	}
}

func TestKeyedPurposeMoveReplayPostsNoSecondPair(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	f.fund(t, 300_000)
	envelope := f.create(t, "/api/incidentals", `{"occasion":"Sunatan","opened_on":"2026-08-01"}`)
	body := fmt.Sprintf(`{"from_purpose_id":%d,"to_purpose_id":%d,"account_id":%d,"amount":300000,"occurred_on":"2026-08-12"}`,
		f.setup.MainPurposeID, envelope, f.cash)

	f.replay(t, "/api/purpose-moves", body, http.StatusCreated)

	f.wantCount(t, "transfer", 1)
	if n := f.count(t, `"transaction" WHERE kind = 'transfer'`); n != 2 {
		t.Errorf("transfer legs = %d, want 2", n)
	}
	// The whole balance moved, so an unkeyed repeat has nothing left to move.
	if rec := f.send(http.MethodPost, "/api/purpose-moves", "", body); rec.Code != http.StatusConflict {
		t.Errorf("unkeyed repeat = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// The purpose correction answers 200, and its replay does too. Keyed, the repeat
// of a correction that worked is that 200; unkeyed it is the 409 no-op. The row's
// id is in the path, so the same key on another row is another request.
func TestKeyedPurposeCorrectionReplayAnswers200AndPostsNoSecondPair(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	f.fund(t, 500_000)
	wrong := f.create(t, "/api/transactions", fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"out","amount":40000,"occurred_on":"2026-08-12"}`,
		f.cash, f.setup.MainPurposeID))
	other := f.create(t, "/api/transactions", fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"out","amount":10000,"occurred_on":"2026-08-12"}`,
		f.cash, f.setup.MainPurposeID))
	titipan := f.create(t, "/api/pass-through-purposes", `{"name":"Titipan"}`)
	body := fmt.Sprintf(`{"purpose_id":%d}`, titipan)
	path := fmt.Sprintf("/api/transactions/%d/purpose-correction", wrong)

	f.replay(t, path, body, http.StatusOK)

	f.wantCount(t, "transfer", 1)
	if n := f.count(t, `"transaction" WHERE kind = 'transfer'`); n != 2 {
		t.Errorf("correction legs = %d, want 2", n)
	}

	rec := f.send(http.MethodPost, path, "", body)
	if rec.Code != http.StatusConflict || decodeError(t, rec).Code != "purpose_correction_noop" {
		t.Errorf("unkeyed repeat = %d, want 409 purpose_correction_noop", rec.Code)
	}

	// Same key, same body, another row: not a replay.
	rec = f.send(http.MethodPost, fmt.Sprintf("/api/transactions/%d/purpose-correction", other), testKey, body)
	if rec.Code != http.StatusConflict || decodeError(t, rec).Code != "idempotency_key_reused" {
		t.Errorf("same key on another row = %d, want 409 idempotency_key_reused (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestKeyedIncidentalReplayOpensNoSecondEnvelope(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	body := `{"occasion":"Sunatan \"Budi\" <RT 5>","target_amount":5000000,"opened_on":"2026-08-01","minimum_per_member":50000}`
	purposes := f.count(t, "purpose")

	f.replay(t, "/api/incidentals", body, http.StatusCreated)

	f.wantCount(t, "purpose", purposes+1)
	f.wantCount(t, "incidental", 1)
}

// The handler reads the snapshot's detail after the commit, on a replay as on a
// first success: the lines come back in the replay too.
func TestKeyedReconciliationReplayReadsTheDetailAndPostsNoSecondFix(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	f.fund(t, 80_000)
	body := fmt.Sprintf(`{"note":"Akhir bulan","counts":[`+
		`{"account_id":%d,"actual_amount":100000,"resolution":"entry_added","fix":{"purpose_id":%d,"direction":"in","amount":20000,"occurred_on":"2026-08-12"}},`+
		`{"account_id":%d,"actual_amount":0,"resolution":"matched"}]}`,
		f.cash, f.setup.MainPurposeID, f.setup.BankAccountID(t))
	transactions := f.count(t, `"transaction"`)

	first := f.replay(t, "/api/reconciliations", body, http.StatusCreated)

	var detail struct {
		Lines []json.RawMessage `json:"lines"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &detail); err != nil || len(detail.Lines) != 2 {
		t.Fatalf("reply = %s (%v), want a snapshot with 2 lines", first.Body.String(), err)
	}
	f.wantCount(t, "reconciliation", 1)
	f.wantCount(t, "reconciliation_line", 2)
	f.wantCount(t, `"transaction"`, transactions+1)
	if got := f.balance(t); got != 100_000 {
		t.Errorf("recorded balance = %d, want 100000: the fix landed once", got)
	}
}

func TestKeyedAccountReplayWithAStartingBalanceWritesNoSecondOpeningRow(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	accounts := f.count(t, "account")
	body := `{"kind":"bank","name":"Bank \"BRI\"","opening_balance":{"amount":500000,"occurred_on":"2026-08-01","note":"Saldo awal"}}`

	f.replay(t, "/api/accounts", body, http.StatusCreated)

	f.wantCount(t, "account", accounts+1)
	if n := f.count(t, `"transaction" WHERE kind = 'opening'`); n != 1 {
		t.Errorf("opening rows = %d, want exactly 1", n)
	}
	if got := f.balance(t); got != 500_000 {
		t.Errorf("recorded balance = %d, want 500000: the repeat doubled the money", got)
	}
}

func TestKeyedAccountReplayWithoutAStartingBalanceMakesNoSecondLocation(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	accounts := f.count(t, "account")

	f.replay(t, "/api/accounts", `{"kind":"cash","name":"Kas kecil"}`, http.StatusCreated)

	f.wantCount(t, "account", accounts+1)
	if n := f.count(t, `"transaction" WHERE kind = 'opening'`); n != 0 {
		t.Errorf("opening rows = %d, want 0", n)
	}
}

// The same key under another body is a 409 on the new routes as on the first.
func TestKeyedNewRoutesSameKeyOtherBodyIs409(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	member := f.create(t, "/api/members", `{"name":"Jane"}`)
	dues := func(amount int) string {
		return fmt.Sprintf(`{"account_id":%d,"member_id":%d,"occurred_on":"2026-08-12","periods":[{"dues_period":"2026-08","amount":%d}]}`, f.cash, member, amount)
	}

	for _, tc := range []struct {
		name, key, path, first, other string
		table                         string
		rows                          int
	}{
		{"accounts", testKey, "/api/accounts", `{"kind":"cash","name":"Kas kecil"}`, `{"kind":"cash","name":"Kas besar"}`, "account", f.count(t, "account") + 1},
		{"dues payments", testOtherKey, "/api/dues-payments", dues(50_000), dues(60_000), `"transaction" WHERE kind = 'dues'`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.key
			if rec := f.send(http.MethodPost, tc.path, key, tc.first); rec.Code != http.StatusCreated {
				t.Fatalf("first POST = %d (body: %s)", rec.Code, rec.Body.String())
			}
			rec := f.send(http.MethodPost, tc.path, key, tc.other)
			if rec.Code != http.StatusConflict || decodeError(t, rec).Code != "idempotency_key_reused" {
				t.Fatalf("POST with the key and another body = %d, want 409 idempotency_key_reused (body: %s)", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Idempotent-Replayed") != "" {
				t.Error("a refused reuse carried Idempotent-Replayed")
			}
			f.wantCount(t, tc.table, tc.rows)
		})
	}
}

// A keyed route without the header is simply unprotected, as POST
// /api/transactions is: two creates, two locations.
func TestKeyedAccountWithoutAHeaderIsUnprotected(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	accounts := f.count(t, "account")
	for range 2 {
		f.create(t, "/api/accounts", `{"kind":"cash","name":"Kas kecil"}`)
	}
	f.wantCount(t, "account", accounts+2)
	f.wantCount(t, "idempotency_key", 0)
}

// A malformed key on a new keyed route is the same 400, and writes nothing.
func TestKeyedAccountMalformedKeyIs400(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	accounts := f.count(t, "account")
	rec := f.send(http.MethodPost, "/api/accounts", "short", `{"kind":"cash","name":"Kas kecil"}`)
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != "invalid_idempotency_key" {
		t.Fatalf("POST with a short key = %d, want 400 invalid_idempotency_key (body: %s)", rec.Code, rec.Body.String())
	}
	f.wantCount(t, "account", accounts)
}

// The rest of /api/accounts is unkeyed: the header is not read, so a malformed
// value is no 400, a repeated valid one is no 409 for another body, and nothing
// is recorded.
func TestTheRestOfAccountsIgnoresTheHeader(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	one := f.create(t, "/api/accounts", `{"kind":"cash","name":"Kas satu"}`)
	two := f.create(t, "/api/accounts", `{"kind":"cash","name":"Kas dua"}`)
	path := func(id int64) string { return fmt.Sprintf("/api/accounts/%d", id) }

	for _, step := range []struct {
		name, method, path, key, body string
		want                          int
	}{
		{"rename", http.MethodPatch, path(one), "short", `{"name":"Kas ganti"}`, http.StatusOK},
		{"rename again under a valid key", http.MethodPatch, path(one), testKey, `{"name":"Kas lagi"}`, http.StatusOK},
		{"rename with another body, same key", http.MethodPatch, path(one), testKey, `{"name":"Kas akhir"}`, http.StatusOK},
		{"retire", http.MethodPatch, path(one), testKey, `{"inactive_on":"2026-08-12"}`, http.StatusOK},
		{"reinstate, same key", http.MethodPatch, path(one), testKey, `{"inactive_on":null}`, http.StatusOK},
		{"delete", http.MethodDelete, path(two), testKey, "", http.StatusNoContent},
		{"delete the other, same key", http.MethodDelete, path(one), testKey, "", http.StatusNoContent},
	} {
		rec := f.send(step.method, step.path, step.key, step.body)
		if rec.Code != step.want {
			t.Fatalf("%s: %s %s = %d, want %d (body: %s)", step.name, step.method, step.path, rec.Code, step.want, rec.Body.String())
		}
		if rec.Header().Get("Idempotent-Replayed") != "" {
			t.Errorf("%s: an unkeyed route carried Idempotent-Replayed", step.name)
		}
	}
	f.wantCount(t, "idempotency_key", 0)
}

// A pass-through purpose moves no money and stays unkeyed, an accepted risk
// (ADR-043 finding 2): the same key twice makes two.
func TestPassThroughPurposeIgnoresTheHeader(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	for range 2 {
		rec := f.send(http.MethodPost, "/api/pass-through-purposes", testKey, `{"name":"Titipan"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/pass-through-purposes = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
		if rec.Header().Get("Idempotent-Replayed") != "" {
			t.Error("an unkeyed route carried Idempotent-Replayed")
		}
	}
	if n := f.count(t, `purpose WHERE kind = 'pass_through'`); n != 2 {
		t.Errorf("pass-through purposes = %d, want 2", n)
	}
	f.wantCount(t, "idempotency_key", 0)
}

// A config route: two different tiers under one key are both made, where a
// keyed route would answer the second with a 409.
func TestConfigRouteIgnoresTheHeader(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	for _, name := range []string{"Biasa", "Khusus"} {
		rec := f.send(http.MethodPost, "/api/dues-tiers", testKey, fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/dues-tiers %q = %d, want %d (body: %s)", name, rec.Code, http.StatusCreated, rec.Body.String())
		}
	}
	f.wantCount(t, "dues_tier", 2)
	f.wantCount(t, "idempotency_key", 0)
}

// A once-only route stays unkeyed (ADR-043): a reversal repeated under the same
// valid key is its own 409, not a replay of the first success, and records no
// key.
func TestOnceOnlyReversalKeepsItsConflictUnderAKey(t *testing.T) {
	t.Parallel()
	f := newKeyedFixture(t)
	id := f.create(t, "/api/transactions", f.body(25_000, "salah arah"))
	path := fmt.Sprintf("/api/transactions/%d/reversal", id)

	if rec := f.send(http.MethodPost, path, testKey, `{"note":null}`); rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("first reversal = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec := f.send(http.MethodPost, path, testKey, `{"note":null}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("repeated reversal under the same key = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Error("a once-only route answered a replay")
	}
	f.wantCount(t, "idempotency_key", 0)
}
