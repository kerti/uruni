package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// reimbursementRequest is POST /api/reimbursements's body: a member fronting
// their own money for the fund (PRD §7.4), recorded now and paid back later.
//
// waived_on is deliberately absent, for the same reason memberRequest omits
// inactive_on: PRD §7.4 never asks to waive a claim, so #69 adds no waive
// route - and accepting waived_on at creation would be that route by the back
// door, a claim born unpayable with no way to undo it. It stays off the
// create body now that PATCH can set it (#103) - a claim is born owed, and
// waiving it is a later event with its own date; CreateReimbursement always
// gets a nil WaivedOn here.
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
// field.
//
// waived_on is on the wire as of #103, when it became settable: a claim the
// member forgave is not owed and not paid, and nothing else on this row says
// so. It was withheld while #69 left it unsettable, since a field that is
// always null tells a client nothing.
type reimbursementResponse struct {
	ID         int64   `json:"id"`
	MemberID   int64   `json:"member_id"`
	PurposeID  int64   `json:"purpose_id"`
	Amount     int64   `json:"amount"`
	IncurredOn string  `json:"incurred_on"`
	WaivedOn   *string `json:"waived_on"`
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
		WaivedOn:   r.WaivedOn,
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
	id, ok := reimbursementID(w, r)
	if !ok {
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

// updateReimbursementRequest is PATCH /api/reimbursements/{id}'s body. An
// absent key means "leave alone"; an explicit null on note or waived_on
// means "clear it" - clearing waived_on un-waives a claim, which is why
// waiving is a field here rather than a /waive route of its own. The four
// NOT NULL columns cannot be cleared, so they need no flag.
//
// Same *Set-flag shape as updateMemberRequest, and for the same reason: no
// struct tag distinguishes a missing key from an explicit null.
type updateReimbursementRequest struct {
	MemberID      *int64
	MemberIDSet   bool
	PurposeID     *int64
	PurposeIDSet  bool
	Amount        *int64
	AmountSet     bool
	IncurredOn    *string
	IncurredOnSet bool
	Note          *string
	NoteSet       bool
	WaivedOn      *string
	WaivedOnSet   bool
}

func decodeUpdateReimbursementRequest(w http.ResponseWriter, r *http.Request) (updateReimbursementRequest, bool) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return updateReimbursementRequest{}, false
	}

	var req updateReimbursementRequest
	fields := []struct {
		key string
		set *bool
		dst any
	}{
		{"member_id", &req.MemberIDSet, &req.MemberID},
		{"purpose_id", &req.PurposeIDSet, &req.PurposeID},
		{"amount", &req.AmountSet, &req.Amount},
		{"incurred_on", &req.IncurredOnSet, &req.IncurredOn},
		{"note", &req.NoteSet, &req.Note},
		{"waived_on", &req.WaivedOnSet, &req.WaivedOn},
	}
	for _, f := range fields {
		v, ok := raw[f.key]
		if !ok {
			continue
		}
		*f.set = true
		if err := json.Unmarshal(v, f.dst); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
			return updateReimbursementRequest{}, false
		}
	}
	return req, true
}

// updateReimbursement is PATCH /api/reimbursements/{id}: correcting a claim
// the treasurer got wrong, and waiving one the member has forgiven - the
// same route, because waiving sets a column (#103).
//
// It goes through the ledger rather than a.queries, unlike the POST above:
// "only until settled" is a fact in the transaction table, and a cross-table
// invariant is ADR-027's own test for what belongs in internal/ledger.
//
// A settled claim is refused with its named 409. Nothing here re-checks it,
// and nothing here pre-checks that member_id names a real member either -
// that reaches SQLite's foreign key and comes back through the mapper.
func (a *api) updateReimbursement(w http.ResponseWriter, r *http.Request) {
	id, ok := reimbursementID(w, r)
	if !ok {
		return
	}

	req, ok := decodeUpdateReimbursementRequest(w, r)
	if !ok {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	params := ledger.UpdateReimbursementParams{FundID: fund.ID, ReimbursementID: id}
	if req.MemberIDSet {
		params.MemberID = req.MemberID
	}
	if req.PurposeIDSet {
		params.PurposeID = req.PurposeID
	}
	if req.AmountSet && req.Amount != nil {
		amount := money.Amount(*req.Amount)
		params.Amount = &amount
	}
	if req.IncurredOnSet {
		params.IncurredOn = req.IncurredOn
	}
	if req.NoteSet {
		params.SetNote = true
		params.Note = req.Note
	}
	if req.WaivedOnSet {
		params.SetWaivedOn = true
		params.WaivedOn = req.WaivedOn
	}

	updated, err := a.ledger.UpdateReimbursement(r.Context(), params)
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toReimbursementResponse(updated))
}

// deleteReimbursement is DELETE /api/reimbursements/{id}: for a claim that
// should never have existed - the treasurer's own typo. A claim the member
// actually forgave is waived through the PATCH above, not deleted: that one
// happened, and the record should keep saying so.
//
// A claim that still has a receipt photo attached is refused by receipt's
// foreign key as a 409, the same answer deleting a member with posted
// transactions gives. Receipts have no milestone owner yet (#73), so that
// path is latent rather than dead.
func (a *api) deleteReimbursement(w http.ResponseWriter, r *http.Request) {
	id, ok := reimbursementID(w, r)
	if !ok {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	if err := a.ledger.DeleteReimbursement(r.Context(), fund.ID, id); err != nil {
		mapLedgerDeleteError(w, a.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// reimbursementID parses {id}, or answers the request and reports false.
// Unlike resolveMember it does not pre-fetch the row: every caller hands the
// id to a ledger method that fetches it anyway, and a second read would only
// duplicate the 404 that method already produces.
func reimbursementID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The reimbursement id is not a valid number.")
		return 0, false
	}
	return id, true
}
