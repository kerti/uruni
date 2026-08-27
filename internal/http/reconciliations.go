package http

import (
	"net/http"
	"strconv"

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

// listReconciliations is GET /api/reconciliations: every snapshot ever taken,
// newest first (ListReconciliationsByFund's own ordering) - the history PRD
// section 7.8 shows, not filtered to anything.
func (a *api) listReconciliations(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	recs, err := a.queries.ListReconciliationsByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]reconciliationResponse, 0, len(recs))
	for _, rec := range recs {
		resp = append(resp, toReconciliationResponse(rec))
	}
	writeJSON(w, http.StatusOK, resp)
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
