package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postPurposeCorrection(t *testing.T, r http.Handler, transactionID int64, req purposeCorrectionRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling purpose correction request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/transactions/%d/purpose-correction", transactionID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

// decodeTransactionsList fetches and decodes every posted transaction, the
// same way this package's other test files read the row back to find one
// they need the id of (opening balances, dues rows) that their own POST
// response never carries directly.
func decodeTransactionsList(t *testing.T, r http.Handler) []transactionResponse {
	t.Helper()
	rec := getTransactions(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var page transactionsPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decoding transactions page: %v", err)
	}
	return page.Transactions
}

func findTransactionByKind(t *testing.T, rows []transactionResponse, kind string) transactionResponse {
	t.Helper()
	for _, row := range rows {
		if row.Kind == kind {
			return row
		}
	}
	t.Fatalf("no transaction of kind %q among %+v", kind, rows)
	return transactionResponse{}
}

// The success path: the response wears transferResponse's own shape, the
// same one POST /api/transfers already answers with (ADR-033).
func TestPostPurposeCorrectionReturnsThePostedTransfer(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	titipanRec := postPassThroughPurpose(t, r, "Titipan")
	if titipanRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/pass-through-purposes = %d, want %d (body: %s)", titipanRec.Code, http.StatusCreated, titipanRec.Body.String())
	}
	var titipan purposeResponse
	if err := json.NewDecoder(titipanRec.Body).Decode(&titipan); err != nil {
		t.Fatalf("decoding purpose response: %v", err)
	}

	postedRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: titipan.ID,
		Direction: "out", Amount: 250_000, OccurredOn: "2026-09-05",
	})
	if postedRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", postedRec.Code, http.StatusCreated, postedRec.Body.String())
	}
	var posted transactionResponse
	if err := json.NewDecoder(postedRec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}

	rec := postPurposeCorrection(t, r, posted.ID, purposeCorrectionRequest{PurposeID: setup.MainPurposeID})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/transactions/{id}/purpose-correction = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var transfer transferResponse
	if err := json.NewDecoder(rec.Body).Decode(&transfer); err != nil {
		t.Fatalf("decoding transfer response: %v (body: %s)", err, rec.Body.String())
	}
	if transfer.Kind != "reclass_purpose" {
		t.Errorf("Kind = %q, want %q", transfer.Kind, "reclass_purpose")
	}

	// The original row still shows its stored tag (ADR-033: the list keeps
	// showing the stored tag), and GET /api/transactions now marks it
	// corrected while marking the correction's own legs by their link.
	rows := decodeTransactionsList(t, r)
	original := findTransactionByKind(t, rows, "normal")
	if original.PurposeID != titipan.ID {
		t.Errorf("original PurposeID = %d, want %d (the stored tag, unchanged)", original.PurposeID, titipan.ID)
	}
	if !original.IsCorrected {
		t.Error("original.IsCorrected = false, want true")
	}

	var correctionLegs int
	for _, row := range rows {
		if row.Kind == "transfer" && row.TransferCorrectsTransactionID != nil && *row.TransferCorrectsTransactionID == posted.ID {
			correctionLegs++
		}
	}
	if correctionLegs != 2 {
		t.Errorf("found %d transfer legs with transfer_corrects_transaction_id = %d, want 2", correctionLegs, posted.ID)
	}
}

func TestPostPurposeCorrectionUnknownTransactionIDIs404(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postPurposeCorrection(t, r, 999_999, purposeCorrectionRequest{PurposeID: setup.MainPurposeID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// Every named refusal answers 409 with its own distinct code (ADR-033).
func TestPostPurposeCorrectionRefusalsHaveDistinctCodes(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	titipanRec := postPassThroughPurpose(t, r, "Titipan")
	var titipan purposeResponse
	if err := json.NewDecoder(titipanRec.Body).Decode(&titipan); err != nil {
		t.Fatalf("decoding purpose response: %v", err)
	}

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	// kind='opening': the setup's own cash account opening balance is 0, so
	// seed a fresh account with a positive one through the accounts route.
	acctRec := postAccount(t, r, accountRequest{
		Kind: "cash", Name: "Extra Cash",
		OpeningBalance: &openingBalanceRequest{Amount: 100_000, OccurredOn: "2026-09-01"},
	})
	if acctRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = %d, want %d (body: %s)", acctRec.Code, http.StatusCreated, acctRec.Body.String())
	}

	// kind='dues'
	duesRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-09-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-09", Amount: 25_000}},
	})
	if duesRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", duesRec.Code, http.StatusCreated, duesRec.Body.String())
	}
	var duesPosted []transactionResponse
	if err := json.NewDecoder(duesRec.Body).Decode(&duesPosted); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}
	duesPaymentID := duesPosted[0].ID

	// kind='reimbursement'
	claimRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: member.ID, PurposeID: setup.MainPurposeID,
		Amount: 60_000, IncurredOn: "2026-09-01",
	})
	var claim reimbursementResponse
	if err := json.NewDecoder(claimRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	settleRec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-09-06",
	})
	if settleRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements/{id}/settle = %d, want %d (body: %s)", settleRec.Code, http.StatusCreated, settleRec.Body.String())
	}
	var settlement transactionResponse
	if err := json.NewDecoder(settleRec.Body).Decode(&settlement); err != nil {
		t.Fatalf("decoding settlement response: %v", err)
	}

	// kind='transfer'
	transferRec := postTransfer(t, r, transferRequest{
		PurposeID:     setup.MainPurposeID,
		FromAccountID: setup.CashAccountID(t), ToAccountID: setup.BankAccountID(t),
		Amount: 15_000, OccurredOn: "2026-09-05",
	})
	if transferRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d, want %d (body: %s)", transferRec.Code, http.StatusCreated, transferRec.Body.String())
	}
	transferLeg := findTransactionByKind(t, decodeTransactionsList(t, r), "transfer")

	// A dues reversal: kind='adjustment' with reverses_transaction_id set.
	reversalRec := postDuesPaymentReversal(t, r, duesPaymentID, reverseDuesPaymentRequest{OccurredOn: "2026-09-07"})
	if reversalRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal = %d, want %d (body: %s)", reversalRec.Code, http.StatusCreated, reversalRec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(reversalRec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}

	// A closed incidental, for both the target-closed and source-closed cases.
	incRec := postIncidental(t, r, openIncidentalRequest{Occasion: "Closed Envelope", OpenedOn: "2026-09-01"})
	var envelope incidentalResponse
	if err := json.NewDecoder(incRec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding incidental response: %v", err)
	}
	closeRec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-09-02",
	})
	if closeRec.Code != http.StatusOK {
		t.Fatalf("POST /api/incidentals/{purposeID}/close = %d, want %d (body: %s)", closeRec.Code, http.StatusOK, closeRec.Body.String())
	}

	sourceEnvRec := postIncidental(t, r, openIncidentalRequest{Occasion: "Birthday", OpenedOn: "2026-09-01"})
	var sourceEnvelope incidentalResponse
	if err := json.NewDecoder(sourceEnvRec.Body).Decode(&sourceEnvelope); err != nil {
		t.Fatalf("decoding incidental response: %v", err)
	}
	sourceRowRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: sourceEnvelope.PurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-09-02",
	})
	var sourceRow transactionResponse
	if err := json.NewDecoder(sourceRowRec.Body).Decode(&sourceRow); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	if rec := postCloseIncidental(t, r, sourceEnvelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-09-10",
	}); rec.Code != http.StatusOK {
		t.Fatalf("closing source envelope = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	// The no-op row: an ordinary transaction already tagged to Titipan.
	noopRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: titipan.ID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-05",
	})
	var noopRow transactionResponse
	if err := json.NewDecoder(noopRec.Body).Decode(&noopRow); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}

	openingRow := findTransactionByKind(t, decodeTransactionsList(t, r), "opening")

	tests := []struct {
		name          string
		transactionID int64
		purposeID     int64
		wantCode      string
	}{
		{"opening", openingRow.ID, titipan.ID, "purpose_correction_opening"},
		{"dues", duesPaymentID, titipan.ID, "purpose_correction_dues"},
		{"reimbursement", settlement.ID, titipan.ID, "purpose_correction_reimbursement"},
		{"transfer leg", transferLeg.ID, titipan.ID, "purpose_correction_transfer"},
		{"dues reversal", reversal.ID, titipan.ID, "purpose_correction_dues_reversal"},
		{"target closed", noopRow.ID, envelope.PurposeID, "purpose_correction_target_closed"},
		{"source closed", sourceRow.ID, titipan.ID, "purpose_correction_source_closed"},
		{"no-op", noopRow.ID, titipan.ID, "purpose_correction_noop"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := postPurposeCorrection(t, r, tc.transactionID, purposeCorrectionRequest{PurposeID: tc.purposeID})
			if rec.Code != http.StatusConflict {
				t.Fatalf("code = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", got.Code, tc.wantCode)
			}
		})
	}
}
