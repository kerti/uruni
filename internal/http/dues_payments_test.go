package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postDuesPayment(t *testing.T, r http.Handler, req duesPaymentRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling dues payment request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dues-payments", bytes.NewReader(body)))
	return rec
}

func TestPostDuesPaymentsRequiresAFund(t *testing.T) {
	rec := postDuesPayment(t, testRouter(t), duesPaymentRequest{
		AccountID: 1, PurposeID: 1, MemberID: 1, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/dues-payments before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestPostDuesPaymentsSeveralPeriodsYieldsOneRowPerPeriod is the slice's
// dues-payments acceptance criterion: several periods paid in one call post
// as separate rows - never flattened into one - and the response echoes
// that shape back rather than collapsing it.
func TestPostDuesPaymentsSeveralPeriodsYieldsOneRowPerPeriod(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFundForTransactions(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	periods := []duesPaymentPeriod{
		{DuesPeriod: "2026-06", Amount: 25_000},
		{DuesPeriod: "2026-07", Amount: 25_000},
		{DuesPeriod: "2026-08", Amount: 25_000},
	}
	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
		Periods: periods,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var posted []transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if len(posted) != len(periods) {
		t.Fatalf("POST /api/dues-payments posted %d rows, want %d - one per period", len(posted), len(periods))
	}

	seen := map[string]bool{}
	ids := map[int64]bool{}
	for _, row := range posted {
		if row.Kind != "dues" {
			t.Errorf("row.kind = %q, want %q", row.Kind, "dues")
		}
		if row.MemberID == nil || *row.MemberID != member.ID {
			t.Errorf("row.member_id = %v, want %d", row.MemberID, member.ID)
		}
		if row.DuesPeriod == nil {
			t.Fatal("row.dues_period = nil, want a period")
		}
		seen[*row.DuesPeriod] = true
		ids[row.ID] = true
	}
	for _, p := range periods {
		if !seen[p.DuesPeriod] {
			t.Errorf("period %q missing from the response, got %v", p.DuesPeriod, seen)
		}
	}
	if len(ids) != len(periods) {
		t.Errorf("response has %d distinct transaction ids, want %d - one row per period, not one row reused", len(ids), len(periods))
	}

	fundBal, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal.Int64() != 75_000 {
		t.Errorf("FundBalance() = %d, want %d - three periods at 25000 each", fundBal.Int64(), 75_000)
	}

	list := getTransactions(t, r)
	var allTx []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&allTx); err != nil {
		t.Fatalf("decoding GET /api/transactions response: %v", err)
	}
	if len(allTx) != len(periods) {
		t.Fatalf("GET /api/transactions returned %d rows, want %d", len(allTx), len(periods))
	}
}

func TestPostDuesPaymentsRejectsEmptyPeriods(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
		Periods: nil,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dues-payments (no periods) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}

	list := getTransactions(t, r)
	var allTx []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&allTx); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(allTx) != 0 {
		t.Errorf("GET /api/transactions after a rejected empty-periods post = %d rows, want 0", len(allTx))
	}
}

// TestPostDuesPaymentsRejectsNonPositiveAmount is the dues-payments half of
// the slice's "non-positive amount surfaces as 400" acceptance criterion -
// PostDuesPayments' own check answers, not a second one in the handler.
func TestPostDuesPaymentsRejectsNonPositiveAmount(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 0}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dues-payments (amount=0) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestPostDuesPaymentsRejectsAMalformedDuesPeriod is the acceptance
// criterion's malformed-date case for this route: a dues_period that is not
// a real "YYYY-MM" month.
func TestPostDuesPaymentsRejectsAMalformedDuesPeriod(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-13", Amount: 25_000}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dues-payments (dues_period=2026-13) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostDuesPaymentsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dues-payments", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dues-payments with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestPostDuesPaymentsAMidBatchFailureWritesNothing is the HTTP-level half
// of #96's regression test: a batch whose second period is malformed must
// post ZERO rows, not the first period that would have succeeded on its
// own. Before the fix, each period was posted by its own independently
// committed call, so the first period's row was left standing when the
// second failed; PostDuesPayments now validates every period before writing
// any of them, and writes every row inside one database transaction, so a
// mid-batch failure leaves nothing behind at all.
func TestPostDuesPaymentsAMidBatchFailureWritesNothing(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFundForTransactions(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{
			{DuesPeriod: "2026-06", Amount: 25_000},
			{DuesPeriod: "not-a-period", Amount: 25_000},
			{DuesPeriod: "2026-08", Amount: 25_000},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dues-payments (bad second period) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	fundBal, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal.Int64() != 0 {
		t.Errorf("FundBalance() = %d, want 0 - a failure anywhere in the batch must leave nothing written, not just the periods before it", fundBal.Int64())
	}

	list := getTransactions(t, r)
	var allTx []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&allTx); err != nil {
		t.Fatalf("decoding GET /api/transactions response: %v", err)
	}
	if len(allTx) != 0 {
		t.Errorf("GET /api/transactions after a mid-batch failure returned %d rows, want 0", len(allTx))
	}
}
