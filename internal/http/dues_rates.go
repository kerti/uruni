package http

import (
	"net/http"
	"strconv"

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

// toDuesRateResponse is the wire shape of a rate that stands. The correction
// number and the deleted flag are not on the wire: a client sees the rates in
// force, and a correction shows as a new id (ADR-040).
func toDuesRateResponse(rate store.DuesRate) duesRateResponse {
	return duesRateResponse{
		ID:            rate.ID,
		TierID:        rate.TierID,
		Amount:        rate.Amount,
		EffectiveFrom: rate.EffectiveFrom,
		CreatedAt:     rate.CreatedAt,
	}
}

func standingToDuesRateResponse(rate store.DuesRateStanding) duesRateResponse {
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

// createDuesRate is POST /api/dues-tiers/{id}/rates. A rate is added for a new
// period; it never reprices an existing one (PATCH is the correction, ADR-040).
// A month that already has a rate answers 409 unique_violation; a month whose
// rate was deleted takes the new one. The write goes through the ledger because
// it reads the month's latest correction before it inserts (ADR-027).
func (a *api) createDuesRate(w http.ResponseWriter, r *http.Request) {
	var req duesRateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	rate, err := a.ledger.CreateDuesRate(r.Context(), ledger.CreateDuesRateParams{
		FundID:        tier.FundID,
		TierID:        tier.ID,
		Amount:        money.FromDB(req.Amount),
		EffectiveFrom: req.EffectiveFrom,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toDuesRateResponse(rate))
}

// parseDuesRateID reads the {id} path segment both rate routes share and the
// fund the request is scoped to, or answers the request and reports false. Whether
// the id names a rate in the fund, and whether that rate still stands, is the
// ledger's to say: it fetches the row before it writes (ADR-040).
func (a *api) parseDuesRateID(w http.ResponseWriter, r *http.Request) (fundID, rateID int64, ok bool) {
	rateID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The dues rate id is not a valid number.")
		return 0, 0, false
	}
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return 0, 0, false
	}
	return fund.ID, rateID, true
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

// updateDuesRate is PATCH /api/dues-rates/{id}: corrects a mistyped amount by
// appending the next correction of the rate's month (ADR-040), so the response
// carries a NEW id and the old row stays behind. It reprices every period the
// row covers, deliberately: the alternative is a wrong number nobody can fix.
// An amount equal to the standing one writes nothing and answers the standing
// row; an id that no longer stands is 409 dues_rate_superseded.
func (a *api) updateDuesRate(w http.ResponseWriter, r *http.Request) {
	fundID, rateID, ok := a.parseDuesRateID(w, r)
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

	updated, err := a.ledger.CorrectDuesRate(r.Context(), fundID, rateID, money.FromDB(*req.Amount))
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toDuesRateResponse(updated))
}

// deleteDuesRate is DELETE /api/dues-rates/{id}: what makes a rate filed against
// the wrong month (or an erroneous first rate) removable. It appends a deleting
// correction (ADR-040) rather than destroying the row, so the month then holds no
// rate and its periods fall back to the earlier month's. An id that no longer
// stands is 409 dues_rate_superseded.
func (a *api) deleteDuesRate(w http.ResponseWriter, r *http.Request) {
	fundID, rateID, ok := a.parseDuesRateID(w, r)
	if !ok {
		return
	}

	if err := a.ledger.DeleteDuesRate(r.Context(), fundID, rateID); err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listDuesRates is GET /api/dues-tiers/{id}/rates: the rates in force for one
// tier, oldest effective_from first (ListDuesRatesByTier reads the standing
// view): one per month that has a rate, the highest correction, a deleted month
// absent. The caller derives "the rate in force on date X" by the latest row at
// or before it.
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
		resp = append(resp, standingToDuesRateResponse(rate))
	}
	writeJSON(w, http.StatusOK, resp)
}
