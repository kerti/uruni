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
// Shape checks - a non-positive amount, an unrecognised direction, a
// calendar-invalid occurred_on - are TakeReconciliation's own job (ADR-027);
// this handler decodes and passes through, same as every other write route
// in this package.
type fixRequest struct {
	PurposeID  int64   `json:"purpose_id"`
	Direction  string  `json:"direction"`
	Amount     int64   `json:"amount"`
	OccurredOn string  `json:"occurred_on"`
	Note       *string `json:"note"`
}

// accountCountRequest is one counted location within POST
// /api/reconciliations's body: an account, what the treasurer found there,
// how the gap (if any) was resolved, and the fix that resolves it where the
// resolution calls for one.
type accountCountRequest struct {
	AccountID    int64       `json:"account_id"`
	ActualAmount int64       `json:"actual_amount"`
	Resolution   string      `json:"resolution"`
	Fix          *fixRequest `json:"fix"`
}

// takeReconciliationRequest is POST /api/reconciliations's body: taking one
// snapshot across every account the treasurer counted (PRD section 7.8).
// There is deliberately no performed_at field - TakeReconciliationParams's own
// comment explains why a reconciliation is always "now," never backdated.
type takeReconciliationRequest struct {
	Note   *string               `json:"note"`
	Counts []accountCountRequest `json:"counts"`
}

// reconciliationResponse is the wire shape of one snapshot on its own - no
// lines, no fund_id, same reasoning every other response type in this
// package gives for both omissions.
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
// a snapshot. No fund_id or reconciliation_id - both are implied by the
// detail response this is always nested in.
//
// resolution is returned as the plain schema string ("matched", "left_open",
// "adjusted", "entry_added"), never a color or an Indonesian label: those are
// the SPA's concern, not this slice's, per the issue.
type reconciliationLineResponse struct {
	ID                      int64  `json:"id"`
	AccountID               int64  `json:"account_id"`
	RecordedAmount          int64  `json:"recorded_amount"`
	ActualAmount            int64  `json:"actual_amount"`
	DifferenceAmount        int64  `json:"difference_amount"`
	Resolution              string `json:"resolution"`
	AdjustmentTransactionID *int64 `json:"adjustment_transaction_id"`
}

// openReconciliationLineResponse is GET /api/reconciliations/open-lines's
// element: the same line, plus the reconciliation_id the nested shape can
// leave implied and this one cannot. A flat list across every snapshot is
// unreadable without it - "there is a gap of 15000 somewhere" is not an
// answer the treasurer can act on, and which count it was found in is the
// one fact that makes it one.
type openReconciliationLineResponse struct {
	reconciliationLineResponse
	ReconciliationID int64 `json:"reconciliation_id"`
}

func toOpenReconciliationLineResponse(ln store.ReconciliationLine) openReconciliationLineResponse {
	return openReconciliationLineResponse{
		reconciliationLineResponse: toReconciliationLineResponse(ln),
		ReconciliationID:           ln.ReconciliationID,
	}
}

func toReconciliationLineResponse(ln store.ReconciliationLine) reconciliationLineResponse {
	return reconciliationLineResponse{
		ID:                      ln.ID,
		AccountID:               ln.AccountID,
		RecordedAmount:          ln.RecordedAmount,
		ActualAmount:            ln.ActualAmount,
		DifferenceAmount:        ln.DifferenceAmount,
		Resolution:              ln.Resolution,
		AdjustmentTransactionID: ln.AdjustmentTransactionID,
	}
}

// reconciliationDetailResponse is what POST /api/reconciliations and GET
// /api/reconciliations/{id} both return: the snapshot plus every line it
// froze. POST returns the same shape as the detail GET, not the bare
// snapshot - a caller taking a count wants to see how each line was resolved
// (the recorded/actual/difference numbers and, where posted, the fix's own
// transaction id) without a second round trip.
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
// what the treasurer counted per account, and posts whatever fix each line's
// resolution calls for - all inside one withTx (PRD section 7.8). See that
// method's own doc comment for the cutoff/resolution rules this handler
// re-asserts nothing about; it only decodes and passes through (ADR-027).
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
		mapLedgerError(w, a.logger, err)
		return
	}

	detail, err := a.ledger.GetReconciliationDetail(r.Context(), fund.ID, rec.ID)
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toReconciliationDetailResponse(detail))
}

// reconciliationsPageSize is GET /api/reconciliations's fixed page size
// (#227, ADR-032 "Lists: paging and search"): 25 a page, same as
// GET /api/transactions and GET /api/reimbursements.
const reconciliationsPageSize = 25

// reconciliationListItemResponse is one row of GET /api/reconciliations's
// page: the snapshot plus open_difference_amount, the sum of
// ABS(difference_amount) across that snapshot's still-open lines
// (ListReconciliationsPage's own comment has the reasoning). Cek kas's list
// needs this to show cocok versus selisih per row without a detail fetch.
type reconciliationListItemResponse struct {
	reconciliationResponse
	OpenDifferenceAmount int64 `json:"open_difference_amount"`
}

// reconciliationsPageResponse is GET /api/reconciliations's envelope (#227),
// the same {rows, next_cursor} shape transactionsPageResponse and
// reimbursementsPageResponse already use.
type reconciliationsPageResponse struct {
	Reconciliations []reconciliationListItemResponse `json:"reconciliations"`
	NextCursor      *string                          `json:"next_cursor"`
}

// encodeReconciliationsCursor/decodeReconciliationsCursor are
// encodeTransactionsCursor/decodeTransactionsCursor's own shape (base64 of
// "performed_at|id", the pair ListReconciliationsPage's keyset WHERE clause
// compares against), except performed_at is itself an integer (a Unix
// timestamp, not a calendar date - CreateReconciliation's own comment has
// why), so both halves are validated as positive integers rather than one
// being parsed as a date.
func encodeReconciliationsCursor(performedAt, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(performedAt, 10) + "|" + strconv.FormatInt(id, 10)))
}

// decodeReconciliationsCursor rejects anything that doesn't round-trip to
// two positive integers - the shape listReconciliations below answers 400
// invalid_argument for.
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

// listReconciliations is GET /api/reconciliations (#227, ADR-032 "Lists:
// paging and search"): newest-first, keyset-paged on
// (performed_at DESC, id DESC), 25 a page - the same shape
// GET /api/transactions and GET /api/reimbursements already answer, and for
// the same reason (ListReconciliationsPage's own comment has the
// row-value-keyset reasoning). No search - a handful of dated snapshots a
// year, per the issue.
//
// ListReconciliationsPage is asked for one row more than the page size so
// this handler can tell "the next page is empty" apart from "there is a
// next page" without a second round trip, the same trick listTransactions
// and listReimbursements use.
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
		mapSQLiteError(w, a.logger, err)
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

// latestReconciliation is GET /api/reconciliations/latest: the one snapshot
// PRD section 7.7's home banner reads to show "last counted on X."
//
// Before any snapshot has ever been taken this answers 404 "not_found," the
// same code every other "no such row yet" route in this package already
// uses (GET /api/fund before setup, GET /api/incidentals/{id} on an unknown
// id) - not an empty 200. An empty body here would make the caller
// distinguish "no snapshot yet" from "the snapshot has no lines" by shape
// alone, and 404 is also exactly what LatestReconciliation's own sql.ErrNoRows
// already gives for free through mapSQLiteError, so no special case is added
// here to produce anything else.
func (a *api) latestReconciliation(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	rec, err := a.queries.LatestReconciliation(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
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
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toReconciliationDetailResponse(detail))
}

// listOpenReconciliationLines is GET /api/reconciliations/open-lines: every
// line across every snapshot still sitting at resolution "left_open" - a gap
// the treasurer chose to sleep on rather than square immediately (ADR-024).
//
// Included on the maintainer's ruling that kept transfers in M4: left_open is
// a schema-committed state and the capability to read it back already exists,
// so it gets a surface now even though no PRD screen names one yet. No
// filter, no pagination - the same "read everything, let the SPA decide what
// to show" shape GET /api/reimbursements uses for its own unfiltered list.
func (a *api) listOpenReconciliationLines(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	lines, err := a.queries.ListOpenReconciliationLinesByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
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
// id to a ledger method that fetches it anyway, the same reasoning
// incidentalPurposeID's own comment gives.
func reconciliationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The reconciliation id is not a valid number.")
		return 0, false
	}
	return id, true
}
