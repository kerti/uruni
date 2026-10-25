package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// reimbursementRequest is POST /api/reimbursements's body: a member fronting
// their money for the fund (PRD section 7.4), recorded now and paid back
// later.
//
// waived_on is absent, as memberRequest omits inactive_on: accepting it at
// creation would be a waive route by the back door, a claim born unpayable.
// A claim is born owed; waiving (#103) is a later event with its own date, so
// CreateReimbursement always gets a nil WaivedOn here.
type reimbursementRequest struct {
	MemberID   int64   `json:"member_id"`
	PurposeID  int64   `json:"purpose_id"`
	Amount     int64   `json:"amount"`
	IncurredOn string  `json:"incurred_on"`
	Note       *string `json:"note"`
}

// reimbursementResponse is the wire shape of a claim. incurred_on is when the
// member spent their money, not when the fund pays them back; the settlement
// posts on its own date and lives on the transaction row (ADR-024).
//
// settled says whether a kind='reimbursement' transaction references the
// claim: a fact about the ledger, not this row. It rides the list queries (a
// computed column) so the client can render an "all" list without a second
// call. A claim neither settled nor waived is still owed.
//
// waived_on is on the wire since #103: a claim the member forgave is not owed
// and not paid, and nothing else on the row says so.
type reimbursementResponse struct {
	ID         int64   `json:"id"`
	MemberID   int64   `json:"member_id"`
	PurposeID  int64   `json:"purpose_id"`
	Amount     int64   `json:"amount"`
	IncurredOn string  `json:"incurred_on"`
	WaivedOn   *string `json:"waived_on"`
	Note       *string `json:"note"`
	Settled    bool    `json:"settled"`
	CreatedAt  int64   `json:"created_at"`

	// ReceiptIDs is every receipt attached to this claim, oldest first (#154):
	// always present, [] rather than null when none. toReimbursementResponse
	// defaults it to [] (right for createReimbursement, whose claim id is new);
	// listReimbursements and updateReimbursement overwrite it with a real
	// lookup, since a photo may already be attached.
	ReceiptIDs []int64 `json:"receipt_ids"`
}

// toReimbursementResponse maps a store row (and the settled fact only the
// list queries know) to the wire. createReimbursement and updateReimbursement
// pass 0: a fresh claim is born owed, and the ledger refuses every write to
// a settled claim, so 0 is a guarantee.
func toReimbursementResponse(r store.Reimbursement, settled int64) reimbursementResponse {
	return reimbursementResponse{
		ID:         r.ID,
		MemberID:   r.MemberID,
		PurposeID:  r.PurposeID,
		Amount:     r.Amount,
		IncurredOn: r.IncurredOn,
		WaivedOn:   r.WaivedOn,
		Note:       r.Note,
		Settled:    settled != 0,
		CreatedAt:  r.CreatedAt,

		ReceiptIDs: []int64{},
	}
}

// toReimbursementResponseRow maps one ListReimbursementsPage row (which
// carries the settled column) to the wire shape.
func toReimbursementResponseRow(r store.ListReimbursementsPageRow) reimbursementResponse {
	return toReimbursementResponse(store.Reimbursement{
		ID:         r.ID,
		FundID:     r.FundID,
		MemberID:   r.MemberID,
		PurposeID:  r.PurposeID,
		Amount:     r.Amount,
		IncurredOn: r.IncurredOn,
		WaivedOn:   r.WaivedOn,
		Note:       r.Note,
		CreatedAt:  r.CreatedAt,
	}, r.Settled)
}

// createReimbursement is POST /api/reimbursements. Recording a claim moves no
// money (no ledger row until settled), but it goes through
// Ledger.CreateReimbursement because one invariant applies: a claim against a
// closed envelope could only end in a refused payout, so it answers 409
// incidental_closed (ADR-031, #473).
//
// A malformed incurred_on and a member_id or purpose_id naming no row reach
// SQLite's CHECK and FOREIGN KEY constraints and come back through
// mapLedgerError's fallthrough to mapSQLiteError.
func (a *api) createReimbursement(w http.ResponseWriter, r *http.Request) {
	var req reimbursementRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	claim, err := a.ledger.CreateReimbursement(r.Context(), ledger.CreateReimbursementParams{
		FundID:     fund.ID,
		MemberID:   req.MemberID,
		PurposeID:  req.PurposeID,
		Amount:     money.Amount(req.Amount),
		IncurredOn: req.IncurredOn,
		Note:       req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toReimbursementResponse(claim, 0))
}

// reimbursementsPageSize is GET /api/reimbursements's fixed page size (#226,
// ADR-032): 25 a page, as GET /api/transactions.
const reimbursementsPageSize = 25

// reimbursementsPageResponse is GET /api/reimbursements's envelope (#226),
// the {rows, next_cursor} shape of transactionsPageResponse.
type reimbursementsPageResponse struct {
	Reimbursements []reimbursementResponse `json:"reimbursements"`
	NextCursor     *string                 `json:"next_cursor"`
}

// listReimbursements is GET /api/reimbursements (#226, ADR-032): newest-first,
// keyset-paged on (incurred_on DESC, id DESC), 25 a page, like
// GET /api/transactions (see ListReimbursementsPage for the keyset
// reasoning).
//
// ?outstanding=true narrows to claims still owed (neither settled nor
// waived), the list the treasurer looks at. An unparseable value is a 400,
// not a silent "all": ?outstanding=yes means the caller believes it is
// filtering, and the full list would show settled claims as owed.
//
// ?q= searches member name and note (case-insensitive substring), narrower
// than GET /api/transactions's search (ADR-032): a claim has no purpose-name
// column worth searching.
//
// ListReimbursementsPage is asked for one row more than the page size, as in
// listTransactions, to tell an empty next page from a next page.
func (a *api) listReimbursements(w http.ResponseWriter, r *http.Request) {
	outstandingOnly := false
	if raw := r.URL.Query().Get("outstanding"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "outstanding", "The outstanding filter is not a valid boolean.")
			return
		}
		outstandingOnly = parsed
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	params := store.ListReimbursementsPageParams{
		FundID:    fund.ID,
		PageLimit: reimbursementsPageSize + 1,
	}
	if outstandingOnly {
		params.OutstandingOnly = 1
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		incurredOn, id, ok := decodeDateCursor(cursor)
		if !ok {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "cursor", "The cursor is not valid.")
			return
		}
		params.CursorIncurredOn = incurredOn
		params.CursorID = &id
	}

	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params.Q = q
	}

	rows, err := a.queries.ListReimbursementsPage(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	var nextCursor *string
	if len(rows) > reimbursementsPageSize {
		rows = rows[:reimbursementsPageSize]
		last := rows[len(rows)-1]
		encoded := encodeCursor(last.IncurredOn, last.ID)
		nextCursor = &encoded
	}

	// One batched, fund-scoped receipt lookup for the page (#154), as in
	// listTransactions.
	ids := make([]int64, len(rows))
	for i, claim := range rows {
		ids[i] = claim.ID
	}
	receiptIDs, err := a.receiptIDsForReimbursements(r.Context(), fund.ID, ids)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]reimbursementResponse, 0, len(rows))
	for _, claim := range rows {
		row := toReimbursementResponseRow(claim)
		row.ReceiptIDs = orEmptyReceiptIDs(receiptIDs[claim.ID])
		resp = append(resp, row)
	}
	writeJSON(w, http.StatusOK, reimbursementsPageResponse{Reimbursements: resp, NextCursor: nextCursor})
}

// getReimbursement is GET /api/reimbursements/{id}: one claim, with the
// settled flag and receipt_ids the list rows carry, so the settle and correct
// screens can open on a claim by address. A non-numeric id is the same 404 as
// an unknown one.
func (a *api) getReimbursement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	row, err := a.queries.GetReimbursementWithSettled(r.Context(), store.GetReimbursementWithSettledParams{ID: id, FundID: fund.ID})
	if err != nil {
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return
	}

	receiptIDs, err := a.receiptIDsForReimbursements(r.Context(), fund.ID, []int64{row.ID})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := toReimbursementResponseRow(store.ListReimbursementsPageRow(row))
	resp.ReceiptIDs = orEmptyReceiptIDs(receiptIDs[row.ID])
	writeJSON(w, http.StatusOK, resp)
}

// settleReimbursementRequest is POST /api/reimbursements/{id}/settle's body:
// which account pays the claim out, and when.
//
// No amount and no purpose_id, as in SettleReimbursementParams: both come
// from the claim, since a settlement paying a different figure would not
// settle that claim. occurred_on is the settle date (when the fund handed the
// money over), never incurred_on, which stays on the claim.
type settleReimbursementRequest struct {
	AccountID  int64  `json:"account_id"`
	OccurredOn string `json:"occurred_on"`
}

// settleReimbursement is POST /api/reimbursements/{id}/settle: wraps
// Ledger.SettleReimbursement, the one call in this file that moves money. It
// posts a single kind='reimbursement', direction='out' row and returns the
// shared transactionResponse.
//
// Settling twice is ErrReimbursementAlreadySettled and settling a waived
// claim is ErrReimbursementWaived; the shared mapper gives each a named 409.
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
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransactionResponse(posted))
}

// updateReimbursementRequest is PATCH /api/reimbursements/{id}'s body. An
// absent key means "leave alone"; an explicit null on note or waived_on means
// "clear it" (clearing waived_on un-waives, which is why waiving is a field
// and not a /waive route). The four NOT NULL columns cannot be cleared, so
// they need no flag.
//
// Same *Set-flag shape as updateMemberRequest: no struct tag distinguishes a
// missing key from an explicit null.
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
	if !decodeJSON(w, r, &raw) {
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
	known := make([]string, 0, len(fields))
	for _, f := range fields {
		known = append(known, f.key)
	}
	if key, unknown := unknownKey(raw, known); unknown {
		writeUnknownField(w, key)
		return updateReimbursementRequest{}, false
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
// and waiving one the member forgave, one route because waiving sets a column
// (#103).
//
// It goes through the ledger, unlike the POST: "only until settled" is a fact
// in the transaction table, and a cross-table invariant is ADR-027's test for
// what belongs in internal/ledger.
//
// A settled claim is refused with its named 409. Nothing here re-checks that,
// or pre-checks that member_id names a real member; SQLite's foreign key does
// and the mapper reports it.
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
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := toReimbursementResponse(updated, 0)
	// The row already existed and may carry a receipt uploaded before this
	// PATCH, so receipt_ids gets a real single-row lookup, not the [] default
	// (#154).
	receiptIDs, err := a.receiptIDsForReimbursements(r.Context(), fund.ID, []int64{updated.ID})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}
	resp.ReceiptIDs = orEmptyReceiptIDs(receiptIDs[updated.ID])

	writeJSON(w, http.StatusOK, resp)
}

// deleteReimbursement is DELETE /api/reimbursements/{id}: for a claim that
// should never have existed (a typo). A claim the member forgave is waived
// through the PATCH, not deleted: it happened, and the record should say so.
//
// A claim with a receipt photo attached is refused by receipt's foreign key
// as a 409, like deleting a member with posted transactions. Receipts have no
// milestone owner yet (#73), so that path is latent rather than dead.
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
		mapLedgerError(w, a.log(r), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// reimbursementID parses {id}, or answers the request and reports false.
// Unlike resolveMember it does not pre-fetch the row: every caller hands the
// id to a ledger method that fetches it and produces the 404.
func reimbursementID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The reimbursement id is not a valid number.")
		return 0, false
	}
	return id, true
}
