package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// duesWaiverRequest is POST /api/members/{id}/dues-waivers's body (ADR-050):
// the months whose dues stop being owed, with one optional note shared by all of
// them. A waiver has no sum, so periods are plain "YYYY-MM" strings.
type duesWaiverRequest struct {
	Periods []string `json:"periods"`
	Note    *string  `json:"note"`
}

// duesWaiverResponse is one created waiver. The note is the treasurer's: it
// rides the session-gated dues views only, never the public report.
type duesWaiverResponse struct {
	ID         int64   `json:"id"`
	MemberID   int64   `json:"member_id"`
	DuesPeriod string  `json:"dues_period"`
	Note       *string `json:"note"`
	CreatedAt  int64   `json:"created_at"`
}

// createDuesWaivers is POST /api/members/{id}/dues-waivers: one call to
// Ledger.WaiveDues, which writes a waiver per period in one transaction, all or
// none. It moves no money and posts no row, so balances never change.
//
// Not keyed (ADR-043): a waiver is undoable, not an immutable row, and a repeat
// is answered by the ledger itself with dues_already_waived.
//
// {id} is read through resolveMember, which is fund-scoped: another fund's member
// is a 404.
func (a *api) createDuesWaivers(w http.ResponseWriter, r *http.Request) {
	var req duesWaiverRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Periods) == 0 {
		writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "periods", "At least one period must be given.")
		return
	}

	member, ok := a.resolveMember(w, r)
	if !ok {
		return
	}

	created, err := a.ledger.WaiveDues(r.Context(), ledger.WaiveDuesParams{
		FundID: member.FundID, MemberID: member.ID, Periods: req.Periods, Note: req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := make([]duesWaiverResponse, 0, len(created))
	for _, row := range created {
		resp = append(resp, toDuesWaiverResponse(row))
	}
	writeJSON(w, http.StatusCreated, resp)
}

func toDuesWaiverResponse(row store.DuesWaiver) duesWaiverResponse {
	return duesWaiverResponse{ID: row.ID, MemberID: row.MemberID, DuesPeriod: row.DuesPeriod, Note: row.Note, CreatedAt: row.CreatedAt}
}

// deleteDuesWaiver is DELETE /api/members/{id}/dues-waivers/{period}: Ledger.
// UnwaiveDues, 204 on success. The month is owed again at its rate, less anything
// already paid toward it. A month with no waiver is dues_not_waived.
func (a *api) deleteDuesWaiver(w http.ResponseWriter, r *http.Request) {
	member, ok := a.resolveMember(w, r)
	if !ok {
		return
	}

	err := a.ledger.UnwaiveDues(r.Context(), ledger.UnwaiveDuesParams{
		FundID: member.FundID, MemberID: member.ID, Period: chi.URLParam(r, "period"),
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
