package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

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

// resolvePassThroughPurpose looks {id} up within the fund and insists it is
// a pass-through row, or answers the request itself and reports false.
//
// The kind check is policy, not shape, so it lives here rather than in the
// query (purpose.sql's own note): 'main' is the fund's own system row - the
// one purpose_single_main guarantees - and an incidental carries its own
// lifecycle (PRD §7.5), where the occasion is what the envelope IS rather
// than a label on it. Only a pass-through is a plain name the treasurer
// typed and may have mistyped.
//
// The refusal is 409, not 404: the row is there and she may be looking
// right at it. Saying "not found" about something visible on screen is the
// wrong answer twice over.
func (a *api) resolvePassThroughPurpose(w http.ResponseWriter, r *http.Request) (store.Purpose, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The purpose id is not a valid number.")
		return store.Purpose{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.Purpose{}, false
	}

	purpose, err := a.queries.GetPurposeForFund(r.Context(), store.GetPurposeForFundParams{ID: id, FundID: fund.ID})
	if err != nil {
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.Purpose{}, false
	}
	if purpose.Kind != purposeKindPassThrough {
		writeAPIError(w, http.StatusConflict, "purpose_not_renameable",
			"Only a pass-through purpose can be renamed.")
		return store.Purpose{}, false
	}
	return purpose, true
}

// updatePassThroughPurposeRequest is PATCH /api/purposes/{id}'s body: the
// corrected name, and nothing else. No kind - that is pinned server-side on
// creation for the reason passThroughPurposeRequest describes, and a route
// that could change it afterward would reopen exactly that hole.
//
// Name is a pointer so a body with no name is a 400 rather than a silent
// rename to the empty string, the same reasoning updateFundRequest rests on.
type updatePassThroughPurposeRequest struct {
	Name *string `json:"name"`
}

// updatePassThroughPurpose is PATCH /api/purposes/{id}: fixes the name of a
// pass-through purpose. A posted transaction references a purpose by id and
// nothing in the ledger reads the text, so this rewrites no history - the
// same correction updateAccount makes for a location's name.
func (a *api) updatePassThroughPurpose(w http.ResponseWriter, r *http.Request) {
	purpose, ok := a.resolvePassThroughPurpose(w, r)
	if !ok {
		return
	}

	var req updatePassThroughPurposeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "A name is required.")
		return
	}

	updated, err := a.queries.UpdatePurposeName(r.Context(), store.UpdatePurposeNameParams{ID: purpose.ID, Name: *req.Name})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toPurposeResponse(updated))
}
