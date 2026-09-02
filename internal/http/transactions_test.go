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

func postTransaction(t *testing.T, r http.Handler, req transactionRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling transaction request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)))
	return rec
}

func getTransactions(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/transactions", nil))
	return rec
}

func TestPostTransactionsRequiresAFund(t *testing.T) {
	rec := postTransaction(t, testRouter(t), transactionRequest{
		AccountID: 1, PurposeID: 1, Direction: "in", Amount: 10_000, OccurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/transactions before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetTransactionsRequiresAFund(t *testing.T) {
	rec := getTransactions(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/transactions before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetTransactionsReturnsAnEmptyListBeforeAnyTransaction(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getTransactions(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("GET /api/transactions before any post = %q, want %q", got, "[]\n")
	}
}

// TestPostTransactionsRoundTripsThroughGetAndMovesTheBalance is the slice's
// headline acceptance criterion: a posted transaction shows up unchanged
// through GET /api/transactions, and the fund balance moves by exactly its
// amount - no more, no less.
func TestPostTransactionsRoundTripsThroughGetAndMovesTheBalance(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)

	note := "Kas awal kegiatan 17-an"
	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 150_000, OccurredOn: "2026-08-12", Note: &note,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var posted transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	if posted.ID == 0 {
		t.Error("transaction.id is zero")
	}
	if posted.Kind != "normal" {
		t.Errorf("transaction.kind = %q, want %q", posted.Kind, "normal")
	}
	if posted.Amount != 150_000 {
		t.Errorf("transaction.amount = %d, want %d", posted.Amount, 150_000)
	}
	if posted.AccountID != setup.CashAccountID(t) {
		t.Errorf("transaction.account_id = %d, want %d", posted.AccountID, setup.CashAccountID(t))
	}
	if posted.Note == nil || *posted.Note != note {
		t.Errorf("transaction.note = %v, want %q", posted.Note, note)
	}

	list := getTransactions(t, r)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}
	var got []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, list.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("GET /api/transactions returned %d rows, want 1 (body: %s)", len(got), list.Body.String())
	}
	if got[0].ID != posted.ID || got[0].Amount != posted.Amount || got[0].Kind != posted.Kind ||
		got[0].Direction != posted.Direction || got[0].AccountID != posted.AccountID ||
		got[0].PurposeID != posted.PurposeID || got[0].OccurredOn != posted.OccurredOn ||
		got[0].Note == nil || posted.Note == nil || *got[0].Note != *posted.Note {
		t.Errorf("GET /api/transactions = %+v, want the same row POST /api/transactions returned (%+v)", got[0], posted)
	}

	fundBal, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal.Int64() != 150_000 {
		t.Errorf("FundBalance() = %d, want %d - the fund started at zero and this is the only posted row", fundBal.Int64(), 150_000)
	}
}

// TestPostTransactionsOutDirectionMovesTheBalanceDown checks the other
// boundary: a direction='out' entry decreases the fund balance by exactly
// its amount, mirroring the 'in' case above rather than merely summing to
// something nonzero.
func TestPostTransactionsOutDirectionMovesTheBalanceDown(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	ctx := context.Background()

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 200_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions (in) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 60_000, OccurredOn: "2026-08-05",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions (out) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	fundBal, err := l.FundBalance(ctx, setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal.Int64() != 140_000 {
		t.Errorf("FundBalance() = %d, want %d (200000 in, 60000 out)", fundBal.Int64(), 140_000)
	}
}

// TestPostTransactionsIsAdjustmentPostsAnAdjustmentKind proves the ADR-027
// intent surface: IsAdjustment on the wire selects kind='adjustment' rather
// than a raw kind field the caller could otherwise set to anything.
func TestPostTransactionsIsAdjustmentPostsAnAdjustmentKind(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-12",
		IsAdjustment: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	if posted.Kind != "adjustment" {
		t.Errorf("transaction.kind = %q, want %q", posted.Kind, "adjustment")
	}
}

// TestPostTransactionsRejectsNonPositiveAmount covers the acceptance
// criterion end to end: a non-positive amount surfaces as 400 through the
// real HTTP path, never re-checked here - PostTransaction's own check is
// what answers.
func TestPostTransactionsRejectsNonPositiveAmount(t *testing.T) {
	for _, amount := range []int64{0, -1, -50_000} {
		r := testRouter(t)
		setup := setUpFund(t, r)

		rec := postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "in", Amount: amount, OccurredOn: "2026-08-12",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/transactions (amount=%d) = %d, want %d (body: %s)", amount, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

// TestPostTransactionsRejectsAMalformedOccurredOn is the acceptance
// criterion's other half: a calendar-invalid date surfaces as 400 too.
func TestPostTransactionsRejectsAMalformedOccurredOn(t *testing.T) {
	for _, occurredOn := range []string{"2026-02-30", "not-a-date", "2026-8-12"} {
		r := testRouter(t)
		setup := setUpFund(t, r)

		rec := postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "in", Amount: 10_000, OccurredOn: occurredOn,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/transactions (occurred_on=%q) = %d, want %d (body: %s)", occurredOn, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

func TestPostTransactionsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transactions", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transactions with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestPostTransactionsRejectsAnUnrecognizedDirection is the argument-shape
// case PostTransaction itself validates: "sideways" is neither "in" nor
// "out". Asserted here to prove the handler passes the raw string through
// rather than pre-checking it against an allow-list of its own.
func TestPostTransactionsRejectsAnUnrecognizedDirection(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "sideways", Amount: 10_000, OccurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transactions (direction=sideways) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}
