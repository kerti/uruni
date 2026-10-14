package http

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// fixRequest is one AccountCount's fix, decoded straight into ledger.Fix.
// Shape checks (non-positive amount, unrecognised direction, invalid
// occurred_on) are TakeReconciliation's job (ADR-027); the handler passes
// through.
type fixRequest struct {
	PurposeID  int64   `json:"purpose_id"`
	Direction  string  `json:"direction"`
	Amount     int64   `json:"amount"`
	OccurredOn string  `json:"occurred_on"`
	Note       *string `json:"note"`
}

// accountCountRequest is one counted location within POST
// /api/reconciliations's body: an account, what the treasurer found, how the
// gap (if any) was resolved, and the fix where the resolution calls for one.
type accountCountRequest struct {
	AccountID    int64       `json:"account_id"`
	ActualAmount int64       `json:"actual_amount"`
	Resolution   string      `json:"resolution"`
	Fix          *fixRequest `json:"fix"`
}

// takeReconciliationRequest is POST /api/reconciliations's body: one snapshot
// across every account the treasurer counted (PRD section 7.8). There is no
// performed_at: a reconciliation is always "now" (see
// TakeReconciliationParams).
type takeReconciliationRequest struct {
	Note   *string               `json:"note"`
	Counts []accountCountRequest `json:"counts"`
}

// reconciliationResponse is the wire shape of one snapshot alone: no lines,
// no fund_id, like the other response types here.
type reconciliationResponse struct {
	ID                   int64   `json:"id"`
	PerformedAt          int64   `json:"performed_at"`
	ThroughTransactionID *int64  `json:"through_transaction_id"`
	Note                 *string `json:"note"`
	CreatedAt            int64   `json:"created_at"`
}

func toReconciliationResponse(rec store.Reconciliation) reconciliationResponse {
	return reconciliationResponse{
		ID:                   rec.ID,
		PerformedAt:          rec.PerformedAt,
		ThroughTransactionID: rec.ThroughTransactionID,
		Note:                 rec.Note,
		CreatedAt:            rec.CreatedAt,
	}
}

// reconciliationLineResponse is the wire shape of one counted account within
// a snapshot. No fund_id or reconciliation_id: the detail response it nests
// in implies both.
//
// resolution is the plain schema string ("matched", "left_open", "adjusted",
// "entry_added"), never a color or Indonesian label; those are the SPA's.
type reconciliationLineResponse struct {
	ID                      int64  `json:"id"`
	AccountID               int64  `json:"account_id"`
	RecordedAmount          int64  `json:"recorded_amount"`
	ActualAmount            int64  `json:"actual_amount"`
	DifferenceAmount        int64  `json:"difference_amount"`
	Resolution              string `json:"resolution"`
	AdjustmentTransactionID *int64 `json:"adjustment_transaction_id"`
	// RemainderAmount is what the line still has open (#498): the part of
	// DifferenceAmount its fix did not square, signed alike. Zero means closed.
	// Every "selisih" reads this, never DifferenceAmount (what was found at the
	// count, frozen).
	RemainderAmount int64 `json:"remainder_amount"`
}

// openReconciliationLineResponse is GET /api/reconciliations/open-lines's
// element: the same line plus the reconciliation_id the nested shape leaves
// implied. A flat list across snapshots is unactionable without which count
// found the gap.
type openReconciliationLineResponse struct {
	reconciliationLineResponse
	ReconciliationID int64 `json:"reconciliation_id"`
}

func toOpenReconciliationLineResponse(ln store.ListOpenReconciliationLinesByFundRow) openReconciliationLineResponse {
	return openReconciliationLineResponse{
		reconciliationLineResponse: reconciliationLineResponse{
			ID:                      ln.ID,
			AccountID:               ln.AccountID,
			RecordedAmount:          ln.RecordedAmount,
			ActualAmount:            ln.ActualAmount,
			DifferenceAmount:        ln.DifferenceAmount,
			Resolution:              ln.Resolution,
			AdjustmentTransactionID: ln.AdjustmentTransactionID,
			RemainderAmount:         ln.RemainderAmount,
		},
		ReconciliationID: ln.ReconciliationID,
	}
}

func toReconciliationLineResponse(ln store.ListReconciliationLinesRow) reconciliationLineResponse {
	return reconciliationLineResponse{
		ID:                      ln.ID,
		AccountID:               ln.AccountID,
		RecordedAmount:          ln.RecordedAmount,
		ActualAmount:            ln.ActualAmount,
		DifferenceAmount:        ln.DifferenceAmount,
		Resolution:              ln.Resolution,
		AdjustmentTransactionID: ln.AdjustmentTransactionID,
		RemainderAmount:         ln.RemainderAmount,
	}
}

// reconciliationDetailResponse is what POST /api/reconciliations and GET
// /api/reconciliations/{id} return: the snapshot plus every line it froze.
// POST returns the detail shape, not the bare snapshot, so a caller sees how
// each line resolved (numbers and the fix's transaction id) without a second
// round trip.
type reconciliationDetailResponse struct {
	reconciliationResponse
	Lines []reconciliationLineResponse `json:"lines"`
}

func toReconciliationDetailResponse(d ledger.ReconciliationDetail) reconciliationDetailResponse {
	lines := make([]reconciliationLineResponse, 0, len(d.Lines))
	for _, ln := range d.Lines {
		lines = append(lines, toReconciliationLineResponse(ln))
	}
	return reconciliationDetailResponse{
		reconciliationResponse: toReconciliationResponse(d.Reconciliation),
		Lines:                  lines,
	}
}

// takeReconciliation is POST /api/reconciliations: wraps
// Ledger.TakeReconciliation, which freezes a ledger cutoff, compares it to
// what the treasurer counted per account, and posts each line's fix, in one
// withTx (PRD section 7.8). See that method for the cutoff/resolution rules;
// the handler decodes and passes through (ADR-027).
func (a *api) takeReconciliation(w http.ResponseWriter, r *http.Request) {
	var req takeReconciliationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	counts := make([]ledger.AccountCount, 0, len(req.Counts))
	for _, c := range req.Counts {
		var fix *ledger.Fix
		if c.Fix != nil {
			fix = &ledger.Fix{
				PurposeID:  c.Fix.PurposeID,
				Direction:  c.Fix.Direction,
				Amount:     money.Amount(c.Fix.Amount),
				OccurredOn: c.Fix.OccurredOn,
				Note:       c.Fix.Note,
			}
		}
		counts = append(counts, ledger.AccountCount{
			AccountID:    c.AccountID,
			ActualAmount: money.Amount(c.ActualAmount),
			Resolution:   c.Resolution,
			Fix:          fix,
		})
	}

	rec, err := a.ledger.TakeReconciliation(r.Context(), ledger.TakeReconciliationParams{
		FundID: fund.ID,
		Note:   req.Note,
		Counts: counts,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	detail, err := a.ledger.GetReconciliationDetail(r.Context(), fund.ID, rec.ID)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toReconciliationDetailResponse(detail))
}

// reconciliationsPageSize is GET /api/reconciliations's fixed page size
// (#227, ADR-032): 25 a page, as the other lists.
const reconciliationsPageSize = 25

// reconciliationListItemResponse is one row of GET /api/reconciliations's
// page: the snapshot plus open_difference_amount, the sum of
// ABS(remainder_amount) across its lines (see ListReconciliationsPage). Cek
// kas's list needs it to show cocok versus selisih per row.
type reconciliationListItemResponse struct {
	reconciliationResponse
	OpenDifferenceAmount int64 `json:"open_difference_amount"`
}

// reconciliationsPageResponse is GET /api/reconciliations's envelope (#227),
// the {rows, next_cursor} shape of the other paged lists.
type reconciliationsPageResponse struct {
	Reconciliations []reconciliationListItemResponse `json:"reconciliations"`
	NextCursor      *string                          `json:"next_cursor"`
}

// encodeReconciliationsCursor/decodeReconciliationsCursor have the shape of
// the transactions cursor (base64 of "performed_at|id", the pair
// ListReconciliationsPage's keyset WHERE compares), except performed_at is a
// Unix timestamp integer (see CreateReconciliation), so both halves are
// validated as positive integers.
func encodeReconciliationsCursor(performedAt, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(performedAt, 10) + "|" + strconv.FormatInt(id, 10)))
}

// decodeReconciliationsCursor rejects anything that does not round-trip to
// two positive integers; listReconciliations answers 400 invalid_argument.
func decodeReconciliationsCursor(raw string) (performedAt, id int64, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, 0, false
	}
	performedAtPart, idPart, found := strings.Cut(string(decoded), "|")
	if !found {
		return 0, 0, false
	}
	performedAt, err = strconv.ParseInt(performedAtPart, 10, 64)
	if err != nil || performedAt <= 0 {
		return 0, 0, false
	}
	id, err = strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, false
	}
	return performedAt, id, true
}

// listReconciliations is GET /api/reconciliations (#227, ADR-032):
// newest-first, keyset-paged on (performed_at DESC, id DESC), 25 a page, like
// GET /api/transactions and GET /api/reimbursements (see
// ListReconciliationsPage for the keyset reasoning). No search: a handful of
// dated snapshots a year.
//
// ListReconciliationsPage is asked for one row more than the page size, as in
// listTransactions, to tell an empty next page from a next page.
func (a *api) listReconciliations(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	params := store.ListReconciliationsPageParams{
		FundID:    fund.ID,
		PageLimit: reconciliationsPageSize + 1,
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		performedAt, id, ok := decodeReconciliationsCursor(cursor)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The cursor is not valid.")
			return
		}
		params.CursorPerformedAt = performedAt
		params.CursorID = &id
	}

	rows, err := a.queries.ListReconciliationsPage(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	var nextCursor *string
	if len(rows) > reconciliationsPageSize {
		rows = rows[:reconciliationsPageSize]
		last := rows[len(rows)-1]
		encoded := encodeReconciliationsCursor(last.PerformedAt, last.ID)
		nextCursor = &encoded
	}

	resp := make([]reconciliationListItemResponse, 0, len(rows))
	for _, rec := range rows {
		resp = append(resp, reconciliationListItemResponse{
			reconciliationResponse: reconciliationResponse{
				ID:                   rec.ID,
				PerformedAt:          rec.PerformedAt,
				ThroughTransactionID: rec.ThroughTransactionID,
				Note:                 rec.Note,
				CreatedAt:            rec.CreatedAt,
			},
			OpenDifferenceAmount: rec.OpenDifferenceAmount,
		})
	}
	writeJSON(w, http.StatusOK, reconciliationsPageResponse{Reconciliations: resp, NextCursor: nextCursor})
}

// latestReconciliation is GET /api/reconciliations/latest: the snapshot PRD
// section 7.7's home banner reads for "last counted on X."
//
// Before any snapshot exists this answers 404 "not_found", the code every "no
// such row yet" route uses (GET /api/fund before setup, GET
// /api/incidentals/{id} on an unknown id), not an empty 200, which would make
// the caller tell "no snapshot yet" from "no lines" by shape. LatestReconciliation's
// sql.ErrNoRows already gives 404 through mapSQLiteError.
func (a *api) latestReconciliation(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	rec, err := a.queries.LatestReconciliation(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toReconciliationResponse(rec))
}

// getReconciliation is GET /api/reconciliations/{id}: wraps
// Ledger.GetReconciliationDetail, the snapshot plus the lines it froze.
func (a *api) getReconciliation(w http.ResponseWriter, r *http.Request) {
	id, ok := reconciliationID(w, r)
	if !ok {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	detail, err := a.ledger.GetReconciliationDetail(r.Context(), fund.ID, id)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toReconciliationDetailResponse(detail))
}

// listOpenReconciliationLines is GET /api/reconciliations/open-lines: every
// location whose most recent count still has a non-zero remainder_amount
// (#498): a left_open line (a gap the treasurer chose to sleep on,
// ADR-024), or an entry_added line whose entry closed only part of the gap.
// "Most recent" is per account_id, not per snapshot: once any later snapshot
// has a line for that account, whatever its resolution, the earlier line
// stops being open, but a later snapshot that skips an account leaves its gap
// open. Later means the owning reconciliation's (performed_at, id).
//
// Kept on the maintainer's ruling that kept transfers in M4: left_open is a
// schema-committed state, so reading it back gets a surface though no PRD
// screen names one. No filter or pagination: read everything, let the SPA
// decide, as GET /api/reimbursements does.
func (a *api) listOpenReconciliationLines(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	lines, err := a.queries.ListOpenReconciliationLinesByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]openReconciliationLineResponse, 0, len(lines))
	for _, ln := range lines {
		resp = append(resp, toOpenReconciliationLineResponse(ln))
	}
	writeJSON(w, http.StatusOK, resp)
}

// reconciliationID parses {id}, or answers the request and reports false.
// Unlike resolveMember it does not pre-fetch the row: every caller hands the
// id to a ledger method that fetches it, as incidentalPurposeID does.
func reconciliationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The reconciliation id is not a valid number.")
		return 0, false
	}
	return id, true
}
