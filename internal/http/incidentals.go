package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// openIncidentalRequest is POST /api/incidentals's body: opening one
// envelope for an occasion (PRD §7.5). There is no separate name field -
// Occasion doubles as the purpose's name, the same choice
// OpenIncidentalParams's own doc comment explains.
type openIncidentalRequest struct {
	Occasion     string `json:"occasion"`
	TargetAmount *int64 `json:"target_amount"`
	OpenedOn     string `json:"opened_on"`
}

// incidentalResponse is the wire shape of one envelope on its own - what
// POST /api/incidentals and GET /api/incidentals return. No fund_id, same
// reasoning as every other response type in this package.
type incidentalResponse struct {
	PurposeID    int64   `json:"purpose_id"`
	Occasion     string  `json:"occasion"`
	TargetAmount *int64  `json:"target_amount"`
	OpenedOn     string  `json:"opened_on"`
	ClosedOn     *string `json:"closed_on"`
	CreatedAt    int64   `json:"created_at"`
}

func toIncidentalResponse(i store.Incidental) incidentalResponse {
	return incidentalResponse{
		PurposeID:    i.PurposeID,
		Occasion:     i.Occasion,
		TargetAmount: i.TargetAmount,
		OpenedOn:     i.OpenedOn,
		ClosedOn:     i.ClosedOn,
		CreatedAt:    i.CreatedAt,
	}
}

// incidentalDetailResponse is GET /api/incidentals/{purposeID}'s body: the
// envelope plus the totals PRD §7.5 shows for it. Not embedded on
// incidentalResponse - the two totals only exist once a request asks for
// them specifically, the same reasoning reimbursementResponse's own comment
// gives for keeping a derived fact off the plain list shape.
type incidentalDetailResponse struct {
	PurposeID       int64   `json:"purpose_id"`
	Occasion        string  `json:"occasion"`
	TargetAmount    *int64  `json:"target_amount"`
	OpenedOn        string  `json:"opened_on"`
	ClosedOn        *string `json:"closed_on"`
	CreatedAt       int64   `json:"created_at"`
	CollectedAmount int64   `json:"collected_amount"`
	DisbursedAmount int64   `json:"disbursed_amount"`
}

func toIncidentalDetailResponse(d ledger.IncidentalDetail) incidentalDetailResponse {
	return incidentalDetailResponse{
		PurposeID:       d.Incidental.PurposeID,
		Occasion:        d.Incidental.Occasion,
		TargetAmount:    d.Incidental.TargetAmount,
		OpenedOn:        d.Incidental.OpenedOn,
		ClosedOn:        d.Incidental.ClosedOn,
		CreatedAt:       d.Incidental.CreatedAt,
		CollectedAmount: d.Collected.Int64(),
		DisbursedAmount: d.Disbursed.Int64(),
	}
}

// openIncidental is POST /api/incidentals: wraps Ledger.OpenIncidental, which
// creates the purpose and the incidental row in one transaction (M3's
// atomicity; this handler only exposes it). Handlers decode and pass
// through - occasion, target_amount and opened_on's own shape checks are
// OpenIncidental's job alone (ADR-027).
func (a *api) openIncidental(w http.ResponseWriter, r *http.Request) {
	var req openIncidentalRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	var target *money.Amount
	if req.TargetAmount != nil {
		v := money.Amount(*req.TargetAmount)
		target = &v
	}

	created, err := a.ledger.OpenIncidental(r.Context(), ledger.OpenIncidentalParams{
		FundID:       fund.ID,
		Occasion:     req.Occasion,
		TargetAmount: target,
		OpenedOn:     req.OpenedOn,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toIncidentalResponse(created))
}

// listIncidentals is GET /api/incidentals, optionally ?open=true: every
// envelope ever opened, or only the ones still collecting - the same
// unfiltered-vs-filtered split GET /api/reimbursements?outstanding uses, and
// for the same reason: a closed envelope is still history.
//
// An unparseable value is a 400 rather than a silent "all", matching
// listReimbursements's identical guard.
func (a *api) listIncidentals(w http.ResponseWriter, r *http.Request) {
	openOnly := false
	if raw := r.URL.Query().Get("open"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The open filter is not a valid boolean.")
			return
		}
		openOnly = parsed
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	var incidentals []store.Incidental
	var err error
	if openOnly {
		incidentals, err = a.queries.ListOpenIncidentalsByFund(r.Context(), fund.ID)
	} else {
		incidentals, err = a.queries.ListIncidentalsByFund(r.Context(), fund.ID)
	}
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]incidentalResponse, 0, len(incidentals))
	for _, i := range incidentals {
		resp = append(resp, toIncidentalResponse(i))
	}
	writeJSON(w, http.StatusOK, resp)
}

// getIncidental is GET /api/incidentals/{purposeID}: wraps
// Ledger.GetIncidentalDetail, which is the envelope's own row plus the
// collected/disbursed totals PRD §7.5 shows for it.
func (a *api) getIncidental(w http.ResponseWriter, r *http.Request) {
	purposeID, ok := incidentalPurposeID(w, r)
	if !ok {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	detail, err := a.ledger.GetIncidentalDetail(r.Context(), fund.ID, purposeID)
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toIncidentalDetailResponse(detail))
}

// closeIncidentalRequest is POST /api/incidentals/{purposeID}/close's body:
// which account the roll posts through on both legs (immaterial to
// correctness, see CloseIncidentalAndRollParams's own comment) and the
// close date.
type closeIncidentalRequest struct {
	AccountID int64  `json:"account_id"`
	ClosedOn  string `json:"closed_on"`
}

// closeIncidentalResponse is POST /api/incidentals/{purposeID}/close's body:
// the now-closed envelope, and the amount rolled into the main purpose - 0
// when the leftover was zero or negative, since neither posts anything.
type closeIncidentalResponse struct {
	Incidental   incidentalResponse `json:"incidental"`
	RolledAmount int64              `json:"rolled_amount"`
}

// closeIncidental is POST /api/incidentals/{purposeID}/close: wraps
// Ledger.CloseIncidentalAndRoll. A second close on an already-closed
// envelope is ErrIncidentalAlreadyClosed, mapped to its own named 409.
//
// The response is built from a fresh read after the write, the same shape
// settleReimbursement uses for its own posted row: CloseIncidentalAndRoll
// returns only the rolled amount, not the updated incidental, so the closed
// row (closed_on now set) is re-fetched through a.queries rather than
// reconstructed by hand here.
func (a *api) closeIncidental(w http.ResponseWriter, r *http.Request) {
	purposeID, ok := incidentalPurposeID(w, r)
	if !ok {
		return
	}

	var req closeIncidentalRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	rolled, err := a.ledger.CloseIncidentalAndRoll(r.Context(), ledger.CloseIncidentalAndRollParams{
		FundID:    fund.ID,
		PurposeID: purposeID,
		AccountID: req.AccountID,
		ClosedOn:  req.ClosedOn,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	closed, err := a.queries.GetIncidental(r.Context(), store.GetIncidentalParams{
		PurposeID: purposeID, FundID: fund.ID,
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	// 200, not 201: what the response addresses is the envelope the caller
	// already named, now closed - the same shape PATCH /api/reimbursements
	// answers with. The roll may post a transaction, but a zero or negative
	// leftover closes without creating anything at all.
	writeJSON(w, http.StatusOK, closeIncidentalResponse{
		Incidental:   toIncidentalResponse(closed),
		RolledAmount: rolled.Int64(),
	})
}

// incidentalPurposeID parses {purposeID}, or answers the request and reports
// false. Unlike resolveMember it does not pre-fetch the row: every caller
// hands the id to a ledger method that fetches it anyway, the same reasoning
// reimbursementID's own comment gives.
func incidentalPurposeID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "purposeID"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The incidental purpose id is not a valid number.")
		return 0, false
	}
	return id, true
}
