package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// reimbursementRequest is POST /api/reimbursements's body: a member fronting
// their own money for the fund (PRD §7.4), recorded now and paid back later.
//
// waived_on is deliberately absent, for the same reason memberRequest omits
// inactive_on: PRD §7.4 never asks to waive a claim, so #69 adds no waive
// route - and accepting waived_on at creation would be that route by the back
// door, a claim born unpayable with no way to undo it. The column stays in
// the schema for the waive that has not been asked for; CreateReimbursement
// always gets a nil WaivedOn here.
type reimbursementRequest struct {
	MemberID   int64   `json:"member_id"`
	PurposeID  int64   `json:"purpose_id"`
	Amount     int64   `json:"amount"`
	IncurredOn string  `json:"incurred_on"`
	Note       *string `json:"note"`
}

// reimbursementResponse is the wire shape of a claim. incurred_on is when the
// member actually spent their own money, which is not when the fund pays them
// back - the settlement posts on its own date and lives on the transaction
// row, not here (ADR-024).
//
// No settled flag: whether a claim is settled is a fact about the ledger, not
// about this row, and the one query that knows it is what
// GET /api/reimbursements?outstanding=true runs. A client asking "who is
// still owed money" asks that question directly rather than filtering a
// field. waived_on is off the wire because nothing in this API can set it.
type reimbursementResponse struct {
	ID         int64   `json:"id"`
	MemberID   int64   `json:"member_id"`
	PurposeID  int64   `json:"purpose_id"`
	Amount     int64   `json:"amount"`
	IncurredOn string  `json:"incurred_on"`
	Note       *string `json:"note"`
	CreatedAt  int64   `json:"created_at"`
}

func toReimbursementResponse(r store.Reimbursement) reimbursementResponse {
	return reimbursementResponse{
		ID:         r.ID,
		MemberID:   r.MemberID,
		PurposeID:  r.PurposeID,
		Amount:     r.Amount,
		IncurredOn: r.IncurredOn,
		Note:       r.Note,
		CreatedAt:  r.CreatedAt,
	}
}

// createReimbursement is POST /api/reimbursements: a direct-CRUD write
// (ADR-027), so it calls a.queries itself. Recording a claim moves no money -
// there is no ledger row until it is settled, which is exactly why the
// recorded balance still matches the wallet while a claim is outstanding -
// so there is no invariant here for the ledger to hold.
//
// A non-positive amount, a malformed incurred_on and a member_id or
// purpose_id naming no row all reach SQLite's own CHECK and FOREIGN KEY
// constraints and come back through mapSQLiteError, the single source of
// truth for those rules.
func (a *api) createReimbursement(w http.ResponseWriter, r *http.Request) {
	var req reimbursementRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	claim, err := a.queries.CreateReimbursement(r.Context(), store.CreateReimbursementParams{
		FundID:     fund.ID,
		MemberID:   req.MemberID,
		PurposeID:  req.PurposeID,
		Amount:     req.Amount,
		IncurredOn: req.IncurredOn,
		WaivedOn:   nil,
		Note:       req.Note,
		CreatedAt:  time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toReimbursementResponse(claim))
}

// listReimbursements is GET /api/reimbursements, optionally
// ?outstanding=true: every claim, or only those still owed - the list the
// treasurer actually looks at, since a settled claim is history.
//
// The two are separate store queries rather than one query filtered in Go:
// "outstanding" means no kind='reimbursement' transaction references the
// claim, which is a fact in the transaction table, not a column here.
//
// An unparseable value is a 400 rather than a silent "all": ?outstanding=yes
// most likely means the caller believes it is filtering, and answering with
// the full list would quietly show settled claims as though they were owed.
func (a *api) listReimbursements(w http.ResponseWriter, r *http.Request) {
	outstandingOnly := false
	if raw := r.URL.Query().Get("outstanding"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The outstanding filter is not a valid boolean.")
			return
		}
		outstandingOnly = parsed
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	var claims []store.Reimbursement
	var err error
	if outstandingOnly {
		claims, err = a.queries.ListOutstandingReimbursementsByFund(r.Context(), fund.ID)
	} else {
		claims, err = a.queries.ListReimbursementsByFund(r.Context(), fund.ID)
	}
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]reimbursementResponse, 0, len(claims))
	for _, claim := range claims {
		resp = append(resp, toReimbursementResponse(claim))
	}
	writeJSON(w, http.StatusOK, resp)
}

// settleReimbursementRequest is POST /api/reimbursements/{id}/settle's body:
// which account pays the claim out, and when.
//
// No amount and no purpose_id, matching SettleReimbursementParams itself:
// both are read from the claim being settled, because a settlement that paid
// a different figure than the claim would not be a settlement of that claim.
// occurred_on is the settle date - the day the fund actually handed the money
// over - never incurred_on, which stays on the claim as the truth about when
// the member spent it.
type settleReimbursementRequest struct {
	AccountID  int64  `json:"account_id"`
	OccurredOn string `json:"occurred_on"`
}

// settleReimbursement is POST /api/reimbursements/{id}/settle: wraps
// Ledger.SettleReimbursement, the one call in this file that moves money. It
// posts a single kind='reimbursement', direction='out' row and returns it as
// the transactionResponse every other transaction-shaped route already uses.
//
// Settling twice is ErrReimbursementAlreadySettled and settling a waived
// claim is ErrReimbursementWaived; both reach the client as their own named
// 409 through the shared mapper, which already knows them.
func (a *api) settleReimbursement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The reimbursement id is not a valid number.")
		return
	}

	var req settleReimbursementRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	posted, err := a.ledger.SettleReimbursement(r.Context(), ledger.SettleReimbursementParams{
		FundID:          fund.ID,
		ReimbursementID: id,
		AccountID:       req.AccountID,
		OccurredOn:      req.OccurredOn,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransactionResponse(posted))
}
