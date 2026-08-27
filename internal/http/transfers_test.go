package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postTransfer(t *testing.T, r http.Handler, req transferRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling transfer request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transfers", bytes.NewReader(body)))
	return rec
}

func TestPostTransfersRequiresAFund(t *testing.T) {
	rec := postTransfer(t, testRouter(t), transferRequest{
		PurposeID: 1, FromAccountID: 1, ToAccountID: 2, Amount: 100_000, OccurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/transfers before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestPostTransfersPostsTwoOppositeLegsAgainstOneTransfer is the route's
// success path and its fund-neutrality proof at this layer: the two rows
// GET /api/transactions returns carry the same amount in opposite
// directions, against the same purpose and the same transfer id, so they
// sum to zero however the fund's total is derived.
// internal/ledger/transfer_test.go asserts the balances themselves.
func TestPostTransfersPostsTwoOppositeLegsAgainstOneTransfer(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	// Cash has to exist before it can be banked.
	depositRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 500_000, OccurredOn: "2026-08-10",
	})
	if depositRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", depositRec.Code, http.StatusCreated, depositRec.Body.String())
	}

	rec := postTransfer(t, r, transferRequest{
		PurposeID:     setup.MainPurposeID,
		FromAccountID: setup.CashAccountID,
		ToAccountID:   setup.BankAccountID,
		Amount:        300_000,
		OccurredOn:    "2026-08-12",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var transfer transferResponse
	if err := json.NewDecoder(rec.Body).Decode(&transfer); err != nil {
		t.Fatalf("decoding transfer response: %v", err)
	}
	if transfer.ID == 0 {
		t.Error("transfer id = 0, want a real id")
	}
	if transfer.Kind != "between_accounts" {
		t.Errorf("transfer kind = %q, want %q", transfer.Kind, "between_accounts")
	}

	listRec := getTransactions(t, r)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var rows []transactionResponse
	if err := json.NewDecoder(listRec.Body).Decode(&rows); err != nil {
		t.Fatalf("decoding transactions: %v", err)
	}

	legs := map[string]transactionResponse{}
	for _, row := range rows {
		if row.TransferID != nil && *row.TransferID == transfer.ID {
			legs[row.Direction] = row
		}
	}
	if len(legs) != 2 {
		t.Fatalf("legs referencing transfer %d = %d, want 2 (rows: %+v)", transfer.ID, len(legs), rows)
	}

	out, in := legs["out"], legs["in"]
	if out.Amount != 300_000 || in.Amount != 300_000 {
		t.Errorf("leg amounts = out %d / in %d, want 300000 for both", out.Amount, in.Amount)
	}
	if out.AccountID != setup.CashAccountID {
		t.Errorf("out leg account = %d, want cash %d", out.AccountID, setup.CashAccountID)
	}
	if in.AccountID != setup.BankAccountID {
		t.Errorf("in leg account = %d, want bank %d", in.AccountID, setup.BankAccountID)
	}
	if out.PurposeID != setup.MainPurposeID || in.PurposeID != setup.MainPurposeID {
		t.Errorf("leg purposes = out %d / in %d, want %d for both - a transfer never changes what money is for",
			out.PurposeID, in.PurposeID, setup.MainPurposeID)
	}
	for _, leg := range []transactionResponse{out, in} {
		if leg.Kind != "transfer" {
			t.Errorf("leg %s kind = %q, want %q", leg.Direction, leg.Kind, "transfer")
		}
		if leg.OccurredOn != "2026-08-12" {
			t.Errorf("leg %s occurred_on = %q, want %q", leg.Direction, leg.OccurredOn, "2026-08-12")
		}
	}
}

// TestPostTransfersRejectsIdenticalAccounts is the acceptance criterion for
// the movement that would mean nothing: same account on both legs, which
// every schema CHECK would happily accept.
func TestPostTransfersRejectsIdenticalAccounts(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	rec := postTransfer(t, r, transferRequest{
		PurposeID:     setup.MainPurposeID,
		FromAccountID: setup.CashAccountID,
		ToAccountID:   setup.CashAccountID,
		Amount:        100_000,
		OccurredOn:    "2026-08-12",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transfers (same account both legs) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostTransfersRejectsNonPositiveAmount(t *testing.T) {
	for _, amount := range []int64{0, -1, -50_000} {
		r := testRouter(t)
		setup := setUpFundForTransactions(t, r)

		rec := postTransfer(t, r, transferRequest{
			PurposeID:     setup.MainPurposeID,
			FromAccountID: setup.CashAccountID,
			ToAccountID:   setup.BankAccountID,
			Amount:        amount,
			OccurredOn:    "2026-08-12",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/transfers (amount=%d) = %d, want %d (body: %s)", amount, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

func TestPostTransfersRejectsAMalformedOccurredOn(t *testing.T) {
	for _, occurredOn := range []string{"2026-02-30", "not-a-date", "2026-8-12"} {
		r := testRouter(t)
		setup := setUpFundForTransactions(t, r)

		rec := postTransfer(t, r, transferRequest{
			PurposeID:     setup.MainPurposeID,
			FromAccountID: setup.CashAccountID,
			ToAccountID:   setup.BankAccountID,
			Amount:        100_000,
			OccurredOn:    occurredOn,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/transfers (occurred_on=%q) = %d, want %d (body: %s)", occurredOn, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

func TestPostTransfersRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transfers", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transfers with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}
