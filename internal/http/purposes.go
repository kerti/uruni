package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// passThroughPurposeRequest is POST /api/pass-through-purposes's body. Name
// only, no kind: a caller that could name the kind could ask for a second
// 'main' and trip purpose_single_main, which the schema refuses and the API
// should not invite (ADR-027, as for exposing IsAdjustment, not a raw Kind).
type passThroughPurposeRequest struct {
	Name string `json:"name"`
}

// purposeResponse is the wire shape of a purpose row. kind is read-only but
// tells the caller which purposes are the fund's own money and which only pass
// through (PRD section 7.6).
type purposeResponse struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func toPurposeResponse(p store.Purpose) purposeResponse {
	return purposeResponse{ID: p.ID, Kind: p.Kind, Name: p.Name, CreatedAt: p.CreatedAt}
}

// listPurposes is GET /api/purposes: every tag a transaction can carry: main,
// pass-throughs, and any incidental already opened.
//
// ?selectable=true switches to ListSelectablePurposesByFund, excluding a
// closed incidental's purpose (ADR-031): the record-transaction picker asks
// for it so it stops offering what PostTransaction's guard refuses, while
// unfiltered GET /api/purposes answers with everything (a closed envelope is
// history), the split listIncidentals' ?open uses. An unparseable value is a
// 400, as there.
func (a *api) listPurposes(w http.ResponseWriter, r *http.Request) {
	selectable := false
	if raw := r.URL.Query().Get("selectable"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "selectable", "The selectable filter is not a valid boolean.")
			return
		}
		selectable = parsed
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	var purposes []store.Purpose
	var err error
	if selectable {
		purposes, err = a.queries.ListSelectablePurposesByFund(r.Context(), fund.ID)
	} else {
		purposes, err = a.queries.ListPurposesByFund(r.Context(), fund.ID)
	}
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]purposeResponse, 0, len(purposes))
	for _, p := range purposes {
		resp = append(resp, toPurposeResponse(p))
	}
	writeJSON(w, http.StatusOK, resp)
}

// createPassThroughPurpose is POST /api/pass-through-purposes: money the fund
// holds but does not own, collected and paid straight out (PRD section 7.6).
// The kind is pinned server-side; see the request type.
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
		Kind:      string(ledger.PurposeKindPassThrough),
		Name:      req.Name,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toPurposeResponse(purpose))
}

// resolveRenameablePurpose looks {id} up within the fund and refuses only the
// fund's 'main' row, or answers the request and reports false.
//
// The kind check is policy, not shape, so it lives here, not in the query
// (see purpose.sql). 'main' is the only refusal: the fund's system row, which
// purpose_single_main guarantees exactly one of, with no treasurer-typed name
// to have mistyped. A pass-through and an incidental alike are renameable,
// because renaming moves no money and touches no transaction row: a posted
// transaction references a purpose by id and nothing in the ledger reads the
// text, so a mistyped occasion is correctable like a location's name. The app
// is strict about money so everything around it can stay forgiving.
//
// The refusal is 409, not 404: the row is there and she may be looking right
// at it; "not found" about something visible is wrong twice over.
func (a *api) resolveRenameablePurpose(w http.ResponseWriter, r *http.Request) (store.Purpose, bool) {
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
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return store.Purpose{}, false
	}
	if purpose.Kind == string(ledger.PurposeKindMain) {
		writeAPIError(w, http.StatusConflict, "purpose_not_renameable",
			"The fund's Kas Utama purpose cannot be renamed.")
		return store.Purpose{}, false
	}
	return purpose, true
}

// updatePurposeNameRequest is PATCH /api/purposes/{id}'s body: the corrected
// name only. No kind: it is pinned server-side on creation (see
// passThroughPurposeRequest), and a route that could change it would reopen
// that hole.
//
// Name is a pointer so a body with no name is a 400, not a silent rename to
// the empty string, as in updateFundRequest.
type updatePurposeNameRequest struct {
	Name *string `json:"name"`
}

// updatePurposeName is PATCH /api/purposes/{id}: fixes a mistyped name. A
// posted transaction references a purpose by id and nothing in the ledger
// reads the text, so no history is rewritten, as updateAccount for a
// location's name.
//
// An incidental's name lives on two rows (purpose.name and
// incidental.occasion, see OpenIncidental), so that branch goes through
// ledger.RenameIncidental to move both in one transaction; a pass-through is
// a plain UPDATE on purpose alone.
func (a *api) updatePurposeName(w http.ResponseWriter, r *http.Request) {
	purpose, ok := a.resolveRenameablePurpose(w, r)
	if !ok {
		return
	}

	var req updatePurposeNameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "name", "A name is required.")
		return
	}

	if purpose.Kind == string(ledger.PurposeKindIncidental) {
		fund, ok := a.resolveFund(w, r)
		if !ok {
			return
		}
		renamed, err := a.ledger.RenameIncidental(r.Context(), ledger.RenameIncidentalParams{
			FundID: fund.ID, PurposeID: purpose.ID, Occasion: *req.Name,
		})
		if err != nil {
			mapLedgerError(w, a.log(r), err)
			return
		}
		writeJSON(w, http.StatusOK, toPurposeResponse(store.Purpose{
			ID: purpose.ID, FundID: purpose.FundID, Kind: purpose.Kind,
			Name: renamed.Occasion, CreatedAt: purpose.CreatedAt,
		}))
		return
	}

	updated, err := a.queries.UpdatePurposeName(r.Context(), store.UpdatePurposeNameParams{ID: purpose.ID, Name: *req.Name})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toPurposeResponse(updated))
}
