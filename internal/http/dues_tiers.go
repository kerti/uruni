package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// duesTierRequest is POST /api/dues-tiers's body: the name the treasurer
// gives the tier (PRD section 6: "a table, not an enum").
type duesTierRequest struct {
	Name string `json:"name"`
}

// duesTierResponse is the wire shape of a dues_tier row. No fund_id, as
// memberResponse.
type duesTierResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func toDuesTierResponse(t store.DuesTier) duesTierResponse {
	return duesTierResponse{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt}
}

// duesTierListItem is one row of GET /api/dues-tiers: the tier and its rates
// in force, oldest month first. rates is [] for a tier whose price is not
// decided yet, never null.
type duesTierListItem struct {
	duesTierResponse
	Rates []duesRateResponse `json:"rates"`
}

// createDuesTier is POST /api/dues-tiers. A duplicate name for this fund hits
// UNIQUE (fund_id, name) and the ledger answers it as 409 unique_violation; not
// re-checked here.
func (a *api) createDuesTier(w http.ResponseWriter, r *http.Request) {
	var req duesTierRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	tier, err := a.ledger.CreateDuesTier(r.Context(), ledger.CreateDuesTierParams{FundID: fund.ID, Name: req.Name})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toDuesTierResponse(tier))
}

// updateDuesTier is PATCH /api/dues-tiers/{id}: a rename. name is the only
// mutable field and is NOT NULL, so there is no "leave alone vs. clear" as for
// member: a PATCH names the new value, and the column's CHECK refuses empty.
func (a *api) updateDuesTier(w http.ResponseWriter, r *http.Request) {
	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	var req duesTierRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	updated, err := a.ledger.RenameDuesTier(r.Context(), ledger.RenameDuesTierParams{FundID: tier.FundID, TierID: tier.ID, Name: req.Name})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toDuesTierResponse(updated))
}

// listDuesTiers is GET /api/dues-tiers: every tier for the one fund, each
// with its rate history (#672). Two queries however many tiers, so Pengaturan
// reads its Golongan cards in one round trip rather than one per tier after the
// list. A client derives "the rate in force on date X" by the latest row at or
// before it.
func (a *api) listDuesTiers(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	tiers, err := a.queries.ListDuesTiersByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}
	rates, err := a.queries.ListDuesRatesForPricing(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	byTier := make(map[int64][]duesRateResponse, len(tiers))
	for _, rate := range rates {
		byTier[rate.TierID] = append(byTier[rate.TierID], standingToDuesRateResponse(rate))
	}
	resp := make([]duesTierListItem, 0, len(tiers))
	for _, t := range tiers {
		tierRates := byTier[t.ID]
		if tierRates == nil {
			tierRates = []duesRateResponse{}
		}
		resp = append(resp, duesTierListItem{duesTierResponse: toDuesTierResponse(t), Rates: tierRates})
	}
	writeJSON(w, http.StatusOK, resp)
}

// deleteDuesTier is DELETE /api/dues-tiers/{id} (#232): for a tier the fund
// never put anyone in (a setup typo, a golongan named twice), never one with
// members.
//
// The tier and its rates must go in one transaction, which the ledger owns (see
// Ledger.DeleteDuesTier's ordering note).
//
// A tier a member still references is refused by the foreign key and leaves as
// 409 referenced_by_other_records, like deleting a used location or member.
func (a *api) deleteDuesTier(w http.ResponseWriter, r *http.Request) {
	tier, ok := a.resolveDuesTier(w, r)
	if !ok {
		return
	}

	if err := a.ledger.DeleteDuesTier(r.Context(), tier.FundID, tier.ID); err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
