package http

import (
	"net/http"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// duesTierRequest is POST /api/dues-tiers's body: just the name the
// treasurer gives the tier (PRD §6 - "a table, not an enum").
type duesTierRequest struct {
	Name string `json:"name"`
}

// duesTierResponse is the wire shape of a dues_tier row. No fund_id, same
// reasoning as memberResponse.
type duesTierResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func toDuesTierResponse(t store.DuesTier) duesTierResponse {
	return duesTierResponse{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt}
}

// createDuesTier is POST /api/dues-tiers: a direct-CRUD write (ADR-027). A
// duplicate name for this fund hits dues_tier's own UNIQUE (fund_id, name)
// and comes back as 409 through mapSQLiteError - not re-checked here.
func (a *api) createDuesTier(w http.ResponseWriter, r *http.Request) {
	var req duesTierRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	tier, err := a.queries.CreateDuesTier(r.Context(), store.CreateDuesTierParams{
		FundID:    fund.ID,
		Name:      req.Name,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toDuesTierResponse(tier))
}

// listDuesTiers is GET /api/dues-tiers: every tier for the one fund.
func (a *api) listDuesTiers(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	tiers, err := a.queries.ListDuesTiersByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]duesTierResponse, 0, len(tiers))
	for _, t := range tiers {
		resp = append(resp, toDuesTierResponse(t))
	}
	writeJSON(w, http.StatusOK, resp)
}
