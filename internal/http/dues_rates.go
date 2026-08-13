package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/store"
)

// duesRateRequest is POST /api/dues-tiers/{id}/rates's body. amount is
// integer rupiah, never a float or a string (CLAUDE.md's money rule).
// effective_from is the schema's own "YYYY-MM" - unchanged on the wire.
type duesRateRequest struct {
	Amount        int64  `json:"amount"`
	EffectiveFrom string `json:"effective_from"`
}

// duesRateResponse is the wire shape of a dues_rate row. tier_id is kept
// here (unlike fund_id elsewhere) because it is the one parent reference a
// client can still act on: GET .../rates takes it as a path segment, so
// echoing it back is confirming the request, not leaking an id with nowhere
// to go.
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

// resolveDuesTier reads the {id} path segment both dues-rate routes share
// and looks the tier up, or answers the request itself and reports false.
// A non-numeric id is a shape problem this layer alone can see (like
// decodeJSON's malformed-JSON case), so it is 400 invalid_argument. A
// well-formed id naming no row is not a shape problem - it is 404, the same
// code getFund uses for "this resource is not there." Both routes need this
// same lookup: GET is a plain SELECT that would otherwise answer an unknown
// tier with a silent empty list indistinguishable from "no rate decided yet"
// (dues_rate.sql's own comment), and doing the identical check ahead of
// POST keeps the two routes consistent rather than leaning on the
// FOREIGNKEY-to-400 mapping in errors.go for one of them and a 404 read for
// the other.
func (a *api) resolveDuesTier(w http.ResponseWriter, r *http.Request) (store.DuesTier, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The tier id is not a valid number.")
		return store.DuesTier{}, false
	}

	tier, err := a.queries.GetDuesTier(r.Context(), id)
	if err != nil {
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.DuesTier{}, false
	}
	return tier, true
}

// createDuesRate is POST /api/dues-tiers/{id}/rates. A dues rate is edited
// by adding a row, never by updating one (PRD §6, "editable, effective over
// time") - there is no update or delete route, only this one and the list
// below. A duplicate (tier_id, effective_from) hits dues_rate's own UNIQUE
// constraint and comes back as 409 through mapSQLiteError.
func (a *api) createDuesRate(w http.ResponseWriter, r *http.Request) {
	var req duesRateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	rate, err := a.queries.CreateDuesRate(r.Context(), store.CreateDuesRateParams{
		TierID:        tier.ID,
		Amount:        req.Amount,
		EffectiveFrom: req.EffectiveFrom,
		CreatedAt:     time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toDuesRateResponse(rate))
}

// listDuesRates is GET /api/dues-tiers/{id}/rates: every rate ever set for
// one tier, oldest effective_from first (ListDuesRatesByTier's own ORDER
// BY) - the caller derives "the rate in force on date X" itself, or uses
// GetEffectiveDuesRate directly (internal/ledger's dues status work, #67).
func (a *api) listDuesRates(w http.ResponseWriter, r *http.Request) {
	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	rates, err := a.queries.ListDuesRatesByTier(r.Context(), tier.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]duesRateResponse, 0, len(rates))
	for _, rate := range rates {
		resp = append(resp, toDuesRateResponse(rate))
	}
	writeJSON(w, http.StatusOK, resp)
}
