package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/money"
)

// The ceiling (#499) end to end: the wire accepts money.MaxAmount, and one
// rupiah more is a 400 with its own code, amount_too_large, written to
// nothing. The ledger paths are pinned one by one in internal/ledger; these
// pin the mapping and the two routes that write without a ledger method.

func TestPostTransactionsRefusesAnAmountAboveTheCeiling(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	post := func(amount int64) *httptest.ResponseRecorder {
		return postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "in", Amount: amount, OccurredOn: "2026-08-12",
		})
	}

	over := post(money.MaxAmount.Int64() + 1)
	if over.Code != http.StatusBadRequest {
		t.Fatalf("POST one over the ceiling = %d, want %d (body: %s)", over.Code, http.StatusBadRequest, over.Body.String())
	}
	if got := decodeError(t, over); got.Code != "amount_too_large" {
		t.Errorf("error code = %q, want %q", got.Code, "amount_too_large")
	}

	if at := post(money.MaxAmount.Int64()); at.Code != http.StatusCreated {
		t.Fatalf("POST at the ceiling = %d, want %d (body: %s)", at.Code, http.StatusCreated, at.Body.String())
	}
	page := decodeTransactionsPage(t, getTransactions(t, r))
	if len(page.Transactions) != 1 {
		t.Errorf("transactions = %d, want 1: only the one at the ceiling", len(page.Transactions))
	}
}

func TestDuesRatesRefuseAnAmountAboveTheCeiling(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	ceiling := money.MaxAmount.Int64()

	over := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: ceiling + 1, EffectiveFrom: "2026-01"})
	if over.Code != http.StatusBadRequest {
		t.Fatalf("POST rate one over the ceiling = %d, want %d (body: %s)", over.Code, http.StatusBadRequest, over.Body.String())
	}
	if got := decodeError(t, over); got.Code != "amount_too_large" {
		t.Errorf("error code = %q, want %q", got.Code, "amount_too_large")
	}

	at := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: ceiling, EffectiveFrom: "2026-01"})
	if at.Code != http.StatusCreated {
		t.Fatalf("POST rate at the ceiling = %d, want %d (body: %s)", at.Code, http.StatusCreated, at.Body.String())
	}
	var rate duesRateResponse
	if err := json.NewDecoder(at.Body).Decode(&rate); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	// The correction route is the second door: a rate at a sane figure must
	// not be correctable up past the ceiling either.
	small := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-02"})
	var smallRate duesRateResponse
	if err := json.NewDecoder(small.Body).Decode(&smallRate); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	patchOver := patchDuesRate(t, r, smallRate.ID, ceiling+1)
	if patchOver.Code != http.StatusBadRequest {
		t.Fatalf("PATCH rate one over the ceiling = %d, want %d (body: %s)", patchOver.Code, http.StatusBadRequest, patchOver.Body.String())
	}
	if got := decodeError(t, patchOver); got.Code != "amount_too_large" {
		t.Errorf("PATCH error code = %q, want %q", got.Code, "amount_too_large")
	}
	if patchAt := patchDuesRate(t, r, smallRate.ID, ceiling); patchAt.Code != http.StatusOK {
		t.Fatalf("PATCH rate at the ceiling = %d, want %d (body: %s)", patchAt.Code, http.StatusOK, patchAt.Body.String())
	}

	// The refused PATCH left the first rate's figure alone.
	rates := listedRates(t, r, tier.ID)
	if len(rates) != 2 {
		t.Fatalf("rates = %d, want 2 (the refused POST wrote nothing)", len(rates))
	}
}

func TestSetupRefusesAnOpeningBalanceAboveTheCeiling(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	body, err := json.Marshal(map[string]any{
		"name": "Test Fund",
		"accounts": []map[string]any{{
			"kind": "cash", "name": "Tunai",
			"opening_balance": map[string]any{"amount": money.MaxAmount.Int64() + 1, "occurred_on": "2026-08-01"},
		}},
	})
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/setup one over the ceiling = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "amount_too_large" {
		t.Errorf("error code = %q, want %q", got.Code, "amount_too_large")
	}
}
