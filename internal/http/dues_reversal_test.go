package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

func postReversal(t *testing.T, r http.Handler, transactionID int64, req reverseTransactionRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling reversal request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/transactions/%d/reversal", transactionID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

// TestPostReversalOfADuesPaymentReturnsThePostedRow is the success path: the
// response is the reversal row in the transactionResponse shape of POST
// /api/dues-payments and GET /api/transactions.
func TestPostReversalOfADuesPaymentReturnsThePostedRow(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	payRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t),
		MemberID:  member.ID, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if payRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", payRec.Code, http.StatusCreated, payRec.Body.String())
	}
	var posted []transactionResponse
	if err := json.NewDecoder(payRec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}
	paymentID := posted[0].ID

	note := "entered against the wrong member"
	rec := postReversal(t, r, paymentID, reverseTransactionRequest{Note: &note})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions/{id}/reversal = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var reversal transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v (body: %s)", err, rec.Body.String())
	}
	if reversal.Kind != "adjustment" {
		t.Errorf("Kind = %q, want %q", reversal.Kind, "adjustment")
	}
	if reversal.Direction != "out" {
		t.Errorf("Direction = %q, want %q", reversal.Direction, "out")
	}
	if reversal.Amount != 25_000 {
		t.Errorf("Amount = %d, want 25000 (copied from the original payment)", reversal.Amount)
	}
	if reversal.MemberID == nil || *reversal.MemberID != member.ID {
		t.Errorf("MemberID = %v, want %d", reversal.MemberID, member.ID)
	}
	if reversal.DuesPeriod == nil || *reversal.DuesPeriod != "2026-08" {
		t.Errorf("DuesPeriod = %v, want %q", reversal.DuesPeriod, "2026-08")
	}
	if reversal.ID == paymentID {
		t.Error("reversal.ID equals the original payment's id, want a distinct new row")
	}
	// The link is the point of the row: without it on the wire a client cannot
	// tell this from an ordinary correction, or say which payment it undid
	// (ADR-029).
	if reversal.ReversesTransactionID == nil || *reversal.ReversesTransactionID != paymentID {
		t.Errorf("ReversesTransactionID = %v, want %d - the response must name the payment it reversed",
			reversal.ReversesTransactionID, paymentID)
	}

	// And it survives into the recent-transactions list the reconcile flow reads,
	// not only the response that made it.
	listed := decodeTransactionsPage(t, getTransactions(t, r)).Transactions
	var found *transactionResponse
	for i := range listed {
		if listed[i].ID == reversal.ID {
			found = &listed[i]
		}
	}
	if found == nil {
		t.Fatalf("GET /api/transactions does not list the reversal (id %d)", reversal.ID)
	}
	if found.ReversesTransactionID == nil || *found.ReversesTransactionID != paymentID {
		t.Errorf("listed reversal's ReversesTransactionID = %v, want %d", found.ReversesTransactionID, paymentID)
	}
}

// TestPostReversalNoSuchTransactionIs404: a transaction id not in
// this fund answers 404 "not_found", as every missing-resource route here.
func TestPostReversalNoSuchTransactionIs404(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := postReversal(t, r, 999_999, reverseTransactionRequest{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST reversal of a nonexistent transaction = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestPostReversalOfAnOrdinaryTransactionIs201 (ADR-042 flipped this test: it
// used to assert 400 for an ordinary transaction, "as wide as dues"). The
// reversal goes the other way and names its original.
func TestPostReversalOfAnOrdinaryTransactionIs201(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	txRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-01",
	})
	if txRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", txRec.Code, http.StatusCreated, txRec.Body.String())
	}
	var tx transactionResponse
	if err := json.NewDecoder(txRec.Body).Decode(&tx); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}

	rec := postReversal(t, r, tx.ID, reverseTransactionRequest{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST reversal of an ordinary transaction = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}
	if reversal.Direction != "out" || reversal.Amount != 50_000 || reversal.OccurredOn != "2026-08-01" ||
		reversal.ReversesTransactionID == nil || *reversal.ReversesTransactionID != tx.ID {
		t.Errorf("reversal = %+v, want an out of 50000 on the original's day naming it", reversal)
	}
}

// The body is a note and nothing else: a stray occurred_on, amount or purpose is
// dropped, never honoured (ADR-042). The old route took a date; a stale client
// that still sends one gets the ledger's own pick.
func TestPostReversalTakesTheNoteAlone(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	var tx transactionResponse
	txRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-01",
	})
	if err := json.NewDecoder(txRec.Body).Decode(&tx); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	path := fmt.Sprintf("/api/transactions/%d/reversal", tx.ID)

	// A reversal copies the original; a caller naming its own day or amount is
	// refused (#530), not quietly overruled.
	rec := httptest.NewRecorder()
	body := `{"amount":1,"note":"salah arah"}`
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST reversal with an amount = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "invalid_argument" || got.Field != "amount" {
		t.Errorf("error = %+v, want invalid_argument naming amount", got)
	}

	// The refusal reversed nothing: the note alone still reverses the original.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"note":"salah arah"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST reversal = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}
	if reversal.OccurredOn != "2026-08-01" || reversal.Amount != 50_000 || reversal.Direction != "in" || reversal.PurposeID != setup.MainPurposeID {
		t.Errorf("reversal = %+v, want the original's day, amount and purpose and the opposite direction", reversal)
	}
	if reversal.Note == nil || *reversal.Note != "salah arah" {
		t.Errorf("reversal Note = %v, want the note sent", reversal.Note)
	}
}

// The old address is gone, with no alias (ADR-042): a stale bundle calling it gets
// a refusal, never a wrong write.
func TestTheDuesPaymentReversalRouteIsGone(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	for _, path := range []string{"/api/dues-payments/1/reversal"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 404 or 405: the route is removed", path, rec.Code)
		}
	}
}

// Each refusal reaches the wire under its own code (ADR-042), 409 for a state the
// ledger will not take.
func TestPostReversalRefusalsHaveTheirOwnCodes(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cash, main := setup.CashAccountID(t), setup.MainPurposeID

	post := func(direction string, amount int64) transactionResponse {
		t.Helper()
		rec := postTransaction(t, r, transactionRequest{AccountID: cash, PurposeID: main, Direction: direction, Amount: amount, OccurredOn: "2026-08-01"})
		var tx transactionResponse
		if err := json.NewDecoder(rec.Body).Decode(&tx); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/transactions = %d (%v), body: %s", rec.Code, err, rec.Body.String())
		}
		return tx
	}
	post("in", 100_000)
	adjustment := func() transactionResponse {
		rec := postTransaction(t, r, transactionRequest{AccountID: cash, PurposeID: main, Direction: "out", Amount: 1_000, OccurredOn: "2026-08-01", IsAdjustment: true})
		var tx transactionResponse
		if err := json.NewDecoder(rec.Body).Decode(&tx); err != nil {
			t.Fatal(err)
		}
		return tx
	}()
	reversed := post("out", 2_000)
	first := postReversal(t, r, reversed.ID, reverseTransactionRequest{})
	var reversal transactionResponse
	if err := json.NewDecoder(first.Body).Decode(&reversal); err != nil || first.Code != http.StatusCreated {
		t.Fatalf("first reversal = %d (%v), body: %s", first.Code, err, first.Body.String())
	}

	for _, tc := range []struct {
		name string
		id   int64
		code string
	}{
		{"an adjustment", adjustment.ID, "reversal_adjustment"},
		{"a reversal", reversal.ID, "reversal_of_reversal"},
		{"a second reversal", reversed.ID, "transaction_already_reversed"},
	} {
		rec := postReversal(t, r, tc.id, reverseTransactionRequest{})
		if rec.Code != http.StatusConflict {
			t.Errorf("%s: POST reversal = %d, want %d (body: %s)", tc.name, rec.Code, http.StatusConflict, rec.Body.String())
			continue
		}
		if got := decodeError(t, rec); got.Code != tc.code {
			t.Errorf("%s: error code = %q, want %q", tc.name, got.Code, tc.code)
		}
	}
}

// TestPostReversalTwiceIs409: a payment is reversible at most once;
// the second attempt is a state conflict, mapped as ErrReimbursementAlreadySettled.
func TestPostReversalTwiceIs409(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	payRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t),
		MemberID:  member.ID, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	var posted []transactionResponse
	if err := json.NewDecoder(payRec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}
	paymentID := posted[0].ID

	first := postReversal(t, r, paymentID, reverseTransactionRequest{})
	if first.Code != http.StatusCreated {
		t.Fatalf("first reversal = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postReversal(t, r, paymentID, reverseTransactionRequest{})
	if second.Code != http.StatusConflict {
		t.Fatalf("second reversal = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "transaction_already_reversed" {
		t.Errorf("error code = %q, want %q", got.Code, "transaction_already_reversed")
	}
}

// TestPostReversalRequiresAFund mirrors every route's before-setup
// behavior here.
func TestPostReversalRequiresAFund(t *testing.T) {
	t.Parallel()
	rec := postReversal(t, testRouter(t), 1, reverseTransactionRequest{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST reversal before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// postReversalRowOn writes the row Ledger.ReverseTransaction would write for the
// original, dated on a day the ledger would not pick (the original's own day, or
// today once a cek kas has counted it, ADR-042). It is for a test whose subject is
// a report, a total or a list over reversals dated in a later month than their
// originals, the shape a count and the injected clock produce in production, and
// which would otherwise need a cek kas that the test is not about.
func postReversalRowOn(t *testing.T, sqlDB *sql.DB, fundID, originalID int64, on string) store.Transaction {
	t.Helper()
	ctx := context.Background()
	q := store.New(sqlDB)
	original, err := q.GetReversalTarget(ctx, store.GetReversalTargetParams{FundID: fundID, ID: originalID})
	if err != nil {
		t.Fatalf("GetReversalTarget(%d) = %v", originalID, err)
	}
	direction := "in"
	if original.Direction == "in" {
		direction = "out"
	}
	row, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fundID, AccountID: original.AccountID, PurposeID: original.EffectivePurposeID,
		Direction: direction, Amount: original.Amount, OccurredOn: on, Kind: "adjustment",
		MemberID: original.MemberID, DuesPeriod: original.DuesPeriod,
		ReversesTransactionID: &originalID, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("posting the reversal of %d on %s = %v", originalID, on, err)
	}
	return row
}
