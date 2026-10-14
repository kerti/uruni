package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// duesRateRequest is POST /api/dues-tiers/{id}/rates's body. amount is
// integer rupiah, never a float or string (CLAUDE.md's money rule);
// effective_from is the schema's "YYYY-MM".
type duesRateRequest struct {
	Amount        int64  `json:"amount"`
	EffectiveFrom string `json:"effective_from"`
}

// duesRateResponse is the wire shape of a dues_rate row. tier_id is kept
// (unlike fund_id elsewhere) as the one parent reference a client can act on:
// GET .../rates takes it as a path segment, so echoing it confirms the
// request.
type duesRateResponse struct {
	ID            int64  `json:"id"`
	TierID        int64  `json:"tier_id"`
	Amount        int64  `json:"amount"`
	EffectiveFrom string `json:"effective_from"`
	CreatedAt     int64  `json:"created_at"`
}

func toDuesRateResponse(rate store.DuesRate) duesRateResponse {
	return duesRateResponse{
		ID:            rate.ID,
		TierID:        rate.TierID,
		Amount:        rate.Amount,
		EffectiveFrom: rate.EffectiveFrom,
		CreatedAt:     rate.CreatedAt,
	}
}

// resolveDuesTier reads the {id} path segment both dues-rate routes share and
// looks the tier up within the fund, or answers the request and reports
// false. The scope lives in the query (GetDuesTierForFund, #188), so another
// fund's tier is a 404. A non-numeric id is a shape problem only this layer
// sees (like decodeJSON's malformed JSON): 400 invalid_argument. A well-formed
// id naming no row is 404, as getFund. Both routes need it: GET would
// otherwise answer an unknown tier with a silent empty list, indistinguishable
// from "no rate decided yet" (see dues_rate.sql), and the same check ahead of
// POST keeps them consistent rather than leaning on the FOREIGNKEY-to-400
// mapping in errors.go for one.
func (a *api) resolveDuesTier(w http.ResponseWriter, r *http.Request) (store.DuesTier, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The tier id is not a valid number.")
		return store.DuesTier{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.DuesTier{}, false
	}

	tier, err := a.queries.GetDuesTierForFund(r.Context(), store.GetDuesTierForFundParams{
		ID:     id,
		FundID: fund.ID,
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return store.DuesTier{}, false
	}
	return tier, true
}

// createDuesRate is POST /api/dues-tiers/{id}/rates. A rate is edited by
// adding a row for a new period, never repricing an existing one (PRD section
// 6, "editable, effective over time"); #81's PATCH is the narrower case of a
// mistyped amount on the row you have. A duplicate (tier_id, effective_from)
// hits dues_rate's UNIQUE and returns 409 through mapSQLiteError.
func (a *api) createDuesRate(w http.ResponseWriter, r *http.Request) {
	var req duesRateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	// Rates write through queries, not a Ledger method, so the ceiling (#499) is
	// applied here with the ledger paths' check.
	if err := ledger.CheckAmountCeiling("amount", money.Amount(req.Amount)); err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	rate, err := a.queries.CreateDuesRate(r.Context(), store.CreateDuesRateParams{
		TierID:        tier.ID,
		Amount:        req.Amount,
		EffectiveFrom: req.EffectiveFrom,
		CreatedAt:     time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toDuesRateResponse(rate))
}

// resolveDuesRate is resolveDuesTier for dues_rate, and a pre-fetch for the
// same reason: a DELETE affecting zero rows never raises sql.ErrNoRows.
// dues_rate has no fund_id, so GetDuesRateForFund reaches the fund through
// the rate's tier (#188).
func (a *api) resolveDuesRate(w http.ResponseWriter, r *http.Request) (store.DuesRate, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The dues rate id is not a valid number.")
		return store.DuesRate{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.DuesRate{}, false
	}

	rate, err := a.queries.GetDuesRateForFund(r.Context(), store.GetDuesRateForFundParams{
		ID:     id,
		FundID: fund.ID,
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return store.DuesRate{}, false
	}
	return rate, true
}

// updateDuesRateRequest is PATCH /api/dues-rates/{id}'s body: the corrected
// amount only. No effective_from: a rate filed against the wrong month is
// deleted and re-posted, not moved.
//
// Amount must stay a pointer. CHECK (amount >= 0) admits zero, so a plain
// int64 would turn a body with no amount ({}, a misspelt key) into a free
// tier, reading every derived dues status for those periods as paid. nil is a
// 400.
type updateDuesRateRequest struct {
	Amount *int64 `json:"amount"`
}

// updateDuesRate is PATCH /api/dues-rates/{id}: corrects a mistyped amount. It
// retroactively changes derived dues status for the periods the rate covers,
// deliberately: the alternative is a wrong number nobody can fix.
func (a *api) updateDuesRate(w http.ResponseWriter, r *http.Request) {
	rate, ok := a.resolveDuesRate(w, r)
	if !ok {
		return
	}

	var req updateDuesRateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Amount == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The corrected amount is required.")
		return
	}
	if err := ledger.CheckAmountCeiling("amount", money.Amount(*req.Amount)); err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	updated, err := a.queries.UpdateDuesRate(r.Context(), store.UpdateDuesRateParams{
		ID:     rate.ID,
		Amount: *req.Amount,
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toDuesRateResponse(updated))
}

// deleteDuesRate is DELETE /api/dues-rates/{id}: what makes a rate filed
// against the wrong month correctable, since UNIQUE (tier_id, effective_from)
// otherwise refuses the corrected row. Nothing references a dues_rate (a
// payment stores the amount paid, not the rate), so no foreign key fires.
func (a *api) deleteDuesRate(w http.ResponseWriter, r *http.Request) {
	rate, ok := a.resolveDuesRate(w, r)
	if !ok {
		return
	}

	if err := a.queries.DeleteDuesRate(r.Context(), rate.ID); err != nil {
		mapSQLiteDeleteError(w, a.log(r), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listDuesRates is GET /api/dues-tiers/{id}/rates: every rate ever set for one
// tier, oldest effective_from first (ListDuesRatesByTier); the caller derives
// "the rate in force on date X", or uses GetEffectiveDuesRate (#67).
func (a *api) listDuesRates(w http.ResponseWriter, r *http.Request) {
	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	rates, err := a.queries.ListDuesRatesByTier(r.Context(), tier.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]duesRateResponse, 0, len(rates))
	for _, rate := range rates {
		resp = append(resp, toDuesRateResponse(rate))
	}
	writeJSON(w, http.StatusOK, resp)
}
