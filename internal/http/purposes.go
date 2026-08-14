package http

import (
	"net/http"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// purposeKindPassThrough is the only kind this package writes. The other two
// are set elsewhere: 'main' by SetUpFund, 'incidental' by OpenIncidental.
const purposeKindPassThrough = "pass_through"

// passThroughPurposeRequest is POST /api/pass-through-purposes's body. Name
// only, no kind: a caller that can name the kind can ask for a second 'main'
// and trip purpose_single_main, which is the schema's job to refuse and not
// the API's to invite (ADR-027, the same reasoning behind exposing
// IsAdjustment rather than a raw Kind).
type passThroughPurposeRequest struct {
	Name string `json:"name"`
}

// purposeResponse is the wire shape of a purpose row. kind is read-only
// here, but it is what tells the caller which purposes are the fund's own
// money and which are only passing through (PRD §7.6).
type purposeResponse struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func toPurposeResponse(p store.Purpose) purposeResponse {
	return purposeResponse{ID: p.ID, Kind: p.Kind, Name: p.Name, CreatedAt: p.CreatedAt}
}

// listPurposes is GET /api/purposes: every tag a transaction can carry -
// main, whatever pass-throughs exist, and any incidental already opened.
func (a *api) listPurposes(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	purposes, err := a.queries.ListPurposesByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]purposeResponse, 0, len(purposes))
	for _, p := range purposes {
		resp = append(resp, toPurposeResponse(p))
	}
	writeJSON(w, http.StatusOK, resp)
}

// createPassThroughPurpose is POST /api/pass-through-purposes: money the
// fund holds but does not own, collected for something and paid straight out
// (PRD §7.6). The kind is pinned server-side; see the request type.
func (a *api) createPassThroughPurpose(w http.ResponseWriter, r *http.Request) {
	var req passThroughPurposeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	purpose, err := a.queries.CreatePurpose(r.Context(), store.CreatePurposeParams{
		FundID:    fund.ID,
		Kind:      purposeKindPassThrough,
		Name:      req.Name,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toPurposeResponse(purpose))
}
