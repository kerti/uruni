package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postDuesPaymentReversal(t *testing.T, r http.Handler, transactionID int64, req reverseDuesPaymentRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling reversal request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-payments/%d/reversal", transactionID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

// TestPostDuesPaymentReversalReturnsThePostedRow is the route's success
// path: the response is the reversal row, wearing the same
// transactionResponse shape POST /api/dues-payments and GET
// /api/transactions already use.
func TestPostDuesPaymentReversalReturnsThePostedRow(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	payRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
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
	rec := postDuesPaymentReversal(t, r, paymentID, reverseDuesPaymentRequest{
		OccurredOn: "2026-08-15", Note: &note,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
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
	// The link is the whole point of the row: without it on the wire a client
	// cannot tell this apart from an ordinary correction, or say which
	// payment it undid (ADR-029).
	if reversal.ReversesTransactionID == nil || *reversal.ReversesTransactionID != paymentID {
		t.Errorf("ReversesTransactionID = %v, want %d - the response must name the payment it reversed",
			reversal.ReversesTransactionID, paymentID)
	}

	// And it survives the round trip into the recent-transactions list the
	// reconcile flow reads, not only the response to the call that made it.
	var listed []transactionResponse
	if err := json.NewDecoder(getTransactions(t, r).Body).Decode(&listed); err != nil {
		t.Fatalf("decoding GET /api/transactions response: %v", err)
	}
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

// TestPostDuesPaymentReversalNoSuchTransactionIs404: reversing a
// transaction id that does not exist in this fund answers 404, the same
// "not_found" shape every other missing-resource route in this package
// already uses.
func TestPostDuesPaymentReversalNoSuchTransactionIs404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := postDuesPaymentReversal(t, r, 999_999, reverseDuesPaymentRequest{OccurredOn: "2026-08-15"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST reversal of a nonexistent transaction = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestPostDuesPaymentReversalNonDuesTransactionIs400: PRD §4 keeps this
// route exactly as wide as dues - reversing an ordinary transaction is a
// caller mistake, not a resource-state conflict, so it maps to 400 like
// ErrInvalidArgument does everywhere else in this mapper.
func TestPostDuesPaymentReversalNonDuesTransactionIs400(t *testing.T) {
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

	rec := postDuesPaymentReversal(t, r, tx.ID, reverseDuesPaymentRequest{OccurredOn: "2026-08-02"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST reversal of an ordinary transaction = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestPostDuesPaymentReversalTwiceIs409: a payment is reversible at most
// once - the second attempt is a resource-state conflict, mapped the same
// way ErrReimbursementAlreadySettled already is.
func TestPostDuesPaymentReversalTwiceIs409(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	payRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-12",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	var posted []transactionResponse
	if err := json.NewDecoder(payRec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}
	paymentID := posted[0].ID

	first := postDuesPaymentReversal(t, r, paymentID, reverseDuesPaymentRequest{OccurredOn: "2026-08-15"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first reversal = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postDuesPaymentReversal(t, r, paymentID, reverseDuesPaymentRequest{OccurredOn: "2026-08-20"})
	if second.Code != http.StatusConflict {
		t.Fatalf("second reversal = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "dues_payment_already_reversed" {
		t.Errorf("error code = %q, want %q", got.Code, "dues_payment_already_reversed")
	}
}

// TestPostDuesPaymentReversalRequiresAFund mirrors every other route's
// before-setup behavior in this package.
func TestPostDuesPaymentReversalRequiresAFund(t *testing.T) {
	rec := postDuesPaymentReversal(t, testRouter(t), 1, reverseDuesPaymentRequest{OccurredOn: "2026-08-15"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST reversal before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}
