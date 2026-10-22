package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// openIncidentalRequest is POST /api/incidentals's body: opening one envelope
// for an occasion (PRD section 7.5). No separate name field: Occasion doubles
// as the purpose's name (see OpenIncidentalParams).
type openIncidentalRequest struct {
	Occasion     string `json:"occasion"`
	TargetAmount *int64 `json:"target_amount"`
	OpenedOn     string `json:"opened_on"`

	// MinimumPerMember and RecipientMemberIDs (ADR-034, #211): the figure every
	// expected member is asked to give, and the members this envelope is for.
	// Both optional: nil/empty means no minimum and no recipients.
	MinimumPerMember   *int64  `json:"minimum_per_member"`
	RecipientMemberIDs []int64 `json:"recipient_member_ids"`
}

// incidentalResponse is the wire shape of one envelope alone, what POST
// /api/incidentals and GET /api/incidentals return. No fund_id, like the
// other response types.
type incidentalResponse struct {
	PurposeID    int64   `json:"purpose_id"`
	Occasion     string  `json:"occasion"`
	TargetAmount *int64  `json:"target_amount"`
	OpenedOn     string  `json:"opened_on"`
	ClosedOn     *string `json:"closed_on"`
	CreatedAt    int64   `json:"created_at"`
	// MinimumPerMember (ADR-034): nil means no minimum, like TargetAmount.
	MinimumPerMember *int64 `json:"minimum_per_member"`
}

func toIncidentalResponse(i store.Incidental) incidentalResponse {
	return incidentalResponse{
		PurposeID:        i.PurposeID,
		Occasion:         i.Occasion,
		TargetAmount:     i.TargetAmount,
		OpenedOn:         i.OpenedOn,
		ClosedOn:         i.ClosedOn,
		CreatedAt:        i.CreatedAt,
		MinimumPerMember: i.MinimumPerMember,
	}
}

// incidentalDetailResponse is GET /api/incidentals/{purposeID}'s body: the
// envelope plus the totals PRD section 7.5 shows. Not embedded in
// incidentalResponse: the totals exist only when a request asks for them, as
// reimbursementResponse keeps derived facts off the list shape.
type incidentalDetailResponse struct {
	PurposeID       int64   `json:"purpose_id"`
	Occasion        string  `json:"occasion"`
	TargetAmount    *int64  `json:"target_amount"`
	OpenedOn        string  `json:"opened_on"`
	ClosedOn        *string `json:"closed_on"`
	CreatedAt       int64   `json:"created_at"`
	CollectedAmount int64   `json:"collected_amount"`
	DisbursedAmount int64   `json:"disbursed_amount"`
	// BalanceAmount is what the envelope holds now (PurposeBalance). Collected
	// minus disbursed stops being that once a closed envelope is reopened, so the
	// screen reads this rather than subtracting.
	BalanceAmount int64 `json:"balance_amount"`
	// MinimumPerMember and Recipients (ADR-034). Recipients rides only on the
	// detail response, as TargetAmount/CollectedAmount do, avoiding an extra
	// query per row on GET /api/incidentals.
	MinimumPerMember *int64                        `json:"minimum_per_member"`
	Recipients       []incidentalRecipientResponse `json:"recipients"`
}

// incidentalRecipientResponse is one member an envelope is for (ADR-034): id
// and name together, joined in SQL (ListIncidentalRecipients) so the caller
// need not look each one up.
type incidentalRecipientResponse struct {
	MemberID   int64  `json:"member_id"`
	MemberName string `json:"member_name"`
}

func toIncidentalDetailResponse(d ledger.IncidentalDetail) incidentalDetailResponse {
	recipients := make([]incidentalRecipientResponse, 0, len(d.Recipients))
	for _, r := range d.Recipients {
		recipients = append(recipients, incidentalRecipientResponse{MemberID: r.MemberID, MemberName: r.MemberName})
	}
	return incidentalDetailResponse{
		PurposeID:        d.Incidental.PurposeID,
		Occasion:         d.Incidental.Occasion,
		TargetAmount:     d.Incidental.TargetAmount,
		OpenedOn:         d.Incidental.OpenedOn,
		ClosedOn:         d.Incidental.ClosedOn,
		CreatedAt:        d.Incidental.CreatedAt,
		CollectedAmount:  d.Collected.Int64(),
		DisbursedAmount:  d.Disbursed.Int64(),
		BalanceAmount:    d.Balance.Int64(),
		MinimumPerMember: d.Incidental.MinimumPerMember,
		Recipients:       recipients,
	}
}

// openIncidental is POST /api/incidentals: wraps Ledger.OpenIncidental, which
// creates the purpose and the incidental row in one transaction. Handlers
// decode and pass through; occasion, target_amount and opened_on shape checks
// are OpenIncidental's alone (ADR-027).
//
// Keyed (ADR-043): no route deletes an envelope, so a repeat would leave a twin
// for good.
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
	var minimum *money.Amount
	if req.MinimumPerMember != nil {
		v := money.Amount(*req.MinimumPerMember)
		minimum = &v
	}

	idem := idempotencyFrom(r)
	created, err := a.ledger.OpenIncidental(r.Context(), ledger.OpenIncidentalParams{
		Now:                a.now(),
		FundID:             fund.ID,
		Occasion:           req.Occasion,
		TargetAmount:       target,
		OpenedOn:           req.OpenedOn,
		MinimumPerMember:   minimum,
		RecipientMemberIDs: req.RecipientMemberIDs,
		Idempotency:        idem,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	markReplay(w, r, a.log(r), idem)
	writeJSON(w, http.StatusCreated, toIncidentalResponse(created))
}

// listIncidentals is GET /api/incidentals, optionally ?open=true: every
// envelope ever opened, or only those still collecting, like
// GET /api/reimbursements?outstanding: a closed envelope is still history.
//
// An unparseable value is a 400, not a silent "all", as in listReimbursements.
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
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]incidentalResponse, 0, len(incidentals))
	for _, i := range incidentals {
		resp = append(resp, toIncidentalResponse(i))
	}
	writeJSON(w, http.StatusOK, resp)
}

// getIncidental is GET /api/incidentals/{purposeID}: wraps
// Ledger.GetIncidentalDetail, the envelope row plus the collected/disbursed
// totals PRD section 7.5 shows.
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
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toIncidentalDetailResponse(detail))
}

// closeIncidentalRequest is POST /api/incidentals/{purposeID}/close's body:
// the account the roll posts through on both legs (immaterial to correctness,
// see CloseIncidentalAndRollParams) and the close date.
type closeIncidentalRequest struct {
	AccountID int64   `json:"account_id"`
	ClosedOn  string  `json:"closed_on"`
	Note      *string `json:"note"`
}

// closeIncidentalResponse is POST /api/incidentals/{purposeID}/close's body:
// the now-closed envelope and the amount rolled, signed (ADR-031): positive
// rolled out to Kas Utama, negative covered a shortfall from Kas Utama, zero
// landed square with nothing posted.
type closeIncidentalResponse struct {
	Incidental   incidentalResponse `json:"incidental"`
	RolledAmount int64              `json:"rolled_amount"`
}

// closeIncidental is POST /api/incidentals/{purposeID}/close: wraps
// Ledger.CloseIncidentalAndRoll. A second close is
// ErrIncidentalAlreadyClosed, a named 409.
//
// The response is built from a fresh read after the write, as
// settleReimbursement does: CloseIncidentalAndRoll returns only the rolled
// amount, so the closed row (closed_on set) is re-fetched through a.queries.
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
		Now:       a.now(),
		FundID:    fund.ID,
		PurposeID: purposeID,
		AccountID: req.AccountID,
		ClosedOn:  req.ClosedOn,
		Note:      req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	closed, err := a.queries.GetIncidental(r.Context(), store.GetIncidentalParams{
		PurposeID: purposeID, FundID: fund.ID,
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	// 200, not 201: the response addresses the envelope the caller named, now
	// closed, as PATCH /api/reimbursements does. A roll may post a transaction,
	// but a zero or negative leftover closes without creating anything.
	writeJSON(w, http.StatusOK, closeIncidentalResponse{
		Incidental:   toIncidentalResponse(closed),
		RolledAmount: rolled.Int64(),
	})
}

// reopenIncidental is POST /api/incidentals/{purposeID}/reopen: wraps
// Ledger.ReopenIncidental, the visible way back ADR-031 gives a closed
// envelope so a late entry has somewhere to post. No request body: there is
// nothing to say beyond which envelope.
//
// 200, not 201, for the reason closeIncidental gives.
func (a *api) reopenIncidental(w http.ResponseWriter, r *http.Request) {
	purposeID, ok := incidentalPurposeID(w, r)
	if !ok {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	reopened, err := a.ledger.ReopenIncidental(r.Context(), fund.ID, purposeID)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toIncidentalResponse(reopened))
}

// updateIncidentalParticipationRequest is PATCH /api/incidentals/{purposeID}'s
// body: the envelope's target, minimum and recipients (ADR-034, #381), the
// facets PATCH /api/purposes/{id} does not reach (that route corrects
// occasion alone, through RenameIncidental, rather than grow a second
// overlapping way to edit the envelope).
//
// Every field fully replaces its facet, as SetIncidentalParticipation does:
// nil TargetAmount or MinimumPerMember clears it, and RecipientMemberIDs (nil
// or empty alike) replaces the whole set, never a delta. A caller that omits
// target_amount therefore clears it; the SPA always sends all three.
type updateIncidentalParticipationRequest struct {
	TargetAmount       *int64  `json:"target_amount"`
	MinimumPerMember   *int64  `json:"minimum_per_member"`
	RecipientMemberIDs []int64 `json:"recipient_member_ids"`
}

// updateIncidentalParticipation is PATCH /api/incidentals/{purposeID}: wraps
// Ledger.SetIncidentalParticipation. Handlers decode and pass through;
// minimum_per_member's shape check is that method's (ADR-027).
func (a *api) updateIncidentalParticipation(w http.ResponseWriter, r *http.Request) {
	purposeID, ok := incidentalPurposeID(w, r)
	if !ok {
		return
	}

	var req updateIncidentalParticipationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	var target, minimum *money.Amount
	if req.TargetAmount != nil {
		v := money.Amount(*req.TargetAmount)
		target = &v
	}
	if req.MinimumPerMember != nil {
		v := money.Amount(*req.MinimumPerMember)
		minimum = &v
	}

	updated, err := a.ledger.SetIncidentalParticipation(r.Context(), ledger.SetIncidentalParticipationParams{
		FundID:             fund.ID,
		PurposeID:          purposeID,
		TargetAmount:       target,
		MinimumPerMember:   minimum,
		RecipientMemberIDs: req.RecipientMemberIDs,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toIncidentalResponse(updated))
}

// participationStateResponse is one expected member's row on GET
// /api/incidentals/{purposeID}/participation (ADR-034). member is a
// participant (no tier), not the roster shape.
type participationStateResponse struct {
	Member            participantResponse `json:"member"`
	ContributedAmount int64               `json:"contributed_amount"`
	State             string              `json:"state"`
}

// participantResponse is who a participation row is about. It carries no tier,
// rate or arrears: an envelope reads no tier (ADR-034, ADR-040), so those fields
// are left out of the shape rather than sent as a null that reads as "no tier".
type participantResponse struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	JoinedOn   string  `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

func participantMemberResponse(m store.Member) participantResponse {
	return participantResponse{ID: m.ID, Name: m.Name, JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt}
}

func toParticipationStateResponse(p ledger.MemberParticipation) participationStateResponse {
	return participationStateResponse{
		Member:            participantMemberResponse(p.Member),
		ContributedAmount: p.ContributedAmount.Int64(),
		State:             string(p.State),
	}
}

// unexpectedContributionResponse is one row of the "Sumbangan lain" list
// (ADR-034): a contribution from someone the envelope did not expect. Only an
// amount, no state.
type unexpectedContributionResponse struct {
	Member            participantResponse `json:"member"`
	ContributedAmount int64               `json:"contributed_amount"`
}

func toUnexpectedContributionResponse(u ledger.UnexpectedContribution) unexpectedContributionResponse {
	return unexpectedContributionResponse{
		Member:            participantMemberResponse(u.Member),
		ContributedAmount: u.ContributedAmount.Int64(),
	}
}

// incidentalParticipationResponse is GET /api/incidentals/{purposeID}/participation's
// body: the envelope's whole participation table in one round trip.
type incidentalParticipationResponse struct {
	Expected   []participationStateResponse     `json:"expected"`
	Unexpected []unexpectedContributionResponse `json:"unexpected"`
}

// getIncidentalParticipation is GET /api/incidentals/{purposeID}/participation:
// wraps Ledger.GetIncidentalParticipation, PRD section 7.5's "who has
// contributed and how much", derived from the ledger, never stored.
func (a *api) getIncidentalParticipation(w http.ResponseWriter, r *http.Request) {
	purposeID, ok := incidentalPurposeID(w, r)
	if !ok {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	participation, err := a.ledger.GetIncidentalParticipation(r.Context(), fund.ID, purposeID)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	expected := make([]participationStateResponse, 0, len(participation.Expected))
	for _, p := range participation.Expected {
		expected = append(expected, toParticipationStateResponse(p))
	}
	unexpected := make([]unexpectedContributionResponse, 0, len(participation.Unexpected))
	for _, u := range participation.Unexpected {
		unexpected = append(unexpected, toUnexpectedContributionResponse(u))
	}

	writeJSON(w, http.StatusOK, incidentalParticipationResponse{Expected: expected, Unexpected: unexpected})
}

// incidentalPurposeID parses {purposeID}, or answers the request and reports
// false. Unlike resolveMember it does not pre-fetch the row: every caller
// hands the id to a ledger method that fetches it, as reimbursementID does.
func incidentalPurposeID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "purposeID"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The incidental purpose id is not a valid number.")
		return 0, false
	}
	return id, true
}
