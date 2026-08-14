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
// by adding a row for a new period, never by repricing an existing one (PRD
// §6, "editable, effective over time"); #81's PATCH below is the narrower
// case of a mistyped amount on the row you already have, not a price change.
// A duplicate (tier_id, effective_from) hits dues_rate's own UNIQUE
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

// resolveDuesRate reads the {id} path segment PATCH and DELETE
// /api/dues-rates/{id} both need and looks the rate up, or answers the
// request itself and reports false - the same shape as resolveDuesTier
// above, over dues_rate instead of dues_tier. A pre-fetch rather than
// leaning on mapSQLiteError's sql.ErrNoRows case: DELETE affecting zero rows
// does not error at all, so both routes need the same explicit check ahead
// of the write.
func (a *api) resolveDuesRate(w http.ResponseWriter, r *http.Request) (store.DuesRate, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The dues rate id is not a valid number.")
		return store.DuesRate{}, false
	}

	rate, err := a.queries.GetDuesRate(r.Context(), id)
	if err != nil {
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.DuesRate{}, false
	}
	return rate, true
}

// updateDuesRateRequest is PATCH /api/dues-rates/{id}'s body: just the
// corrected amount (issue #81). No effective_from - the row's period is what
// UNIQUE (tier_id, effective_from) polices, and a rate entered against the
// wrong month is fixed by deleting it and posting a new one (below), not by
// mutating the period in place.
//
// Amount is a pointer so an absent key is distinguishable from a sent one.
// A plain int64 would decode a body with no amount - an empty {}, or one
// where the key is misspelt - to 0, and CHECK (amount >= 0) admits 0, so the
// rate would silently become free and every derived dues status for the
// periods it covers would read as paid. That is the precise failure this
// route exists to make fixable, so the one field it takes is required: nil
// is a 400, not a zero. member's nullable columns need the richer
// present-vs-null decoding in members.go; amount is NOT NULL and never
// cleared, so a pointer says everything there is to say here.
type updateDuesRateRequest struct {
	Amount *int64 `json:"amount"`
}

// updateDuesRate is PATCH /api/dues-rates/{id}: corrects a mistyped amount.
// This retroactively changes derived dues status for the periods the rate
// covers - a deliberate, accepted consequence (issue #81), the same shape as
// the mid-year-promotion limitation ADR-024 already accepts. No guard is
// added against it.
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

	updated, err := a.queries.UpdateDuesRate(r.Context(), store.UpdateDuesRateParams{
		ID:     rate.ID,
		Amount: *req.Amount,
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toDuesRateResponse(updated))
}

// deleteDuesRate is DELETE /api/dues-rates/{id}: what makes a rate entered
// against the wrong month correctable at all (issue #81), since UNIQUE
// (tier_id, effective_from) otherwise refuses the corrected row outright.
// Nothing in the ledger references a dues_rate - a dues payment stores the
// amount paid, not the rate (ADR-027) - so there is no foreign key here for
// SQLite to enforce and nothing to map to 409.
func (a *api) deleteDuesRate(w http.ResponseWriter, r *http.Request) {
	rate, ok := a.resolveDuesRate(w, r)
	if !ok {
		return
	}

	if err := a.queries.DeleteDuesRate(r.Context(), rate.ID); err != nil {
		mapSQLiteDeleteError(w, a.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
