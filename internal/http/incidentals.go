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
// envelope for an occasion (PRD section 7.5). There is no separate name field -
// Occasion doubles as the purpose's name, the same choice
// OpenIncidentalParams's own doc comment explains.
type openIncidentalRequest struct {
	Occasion     string `json:"occasion"`
	TargetAmount *int64 `json:"target_amount"`
	OpenedOn     string `json:"opened_on"`

	// MinimumPerMember and RecipientMemberIDs are ADR-034's addition
	// (#211): the one figure every expected member is asked to give, and
	// the members this envelope is for. Both are optional - nil/empty
	// means no minimum and no recipients, same as leaving TargetAmount
	// unset.
	MinimumPerMember   *int64  `json:"minimum_per_member"`
	RecipientMemberIDs []int64 `json:"recipient_member_ids"`
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
	// MinimumPerMember is ADR-034's addition - nil means no minimum, the
	// same nullability TargetAmount carries.
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
// envelope plus the totals PRD section 7.5 shows for it. Not embedded on
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
	// MinimumPerMember and Recipients are ADR-034's addition. Recipients
	// rides only on the detail response, not the plain list - the same
	// "a derived figure costs nothing extra only where it is actually
	// asked for" split TargetAmount/CollectedAmount already draws, here
	// avoiding an extra query per row on GET /api/incidentals.
	MinimumPerMember *int64                        `json:"minimum_per_member"`
	Recipients       []incidentalRecipientResponse `json:"recipients"`
}

// incidentalRecipientResponse is one member an envelope is for (ADR-034) -
// id and name together, the same reasoning ListIncidentalRecipients' own
// comment gives for joining member in SQL rather than asking the caller to
// look each one up.
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
		MinimumPerMember: d.Incidental.MinimumPerMember,
		Recipients:       recipients,
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
	var minimum *money.Amount
	if req.MinimumPerMember != nil {
		v := money.Amount(*req.MinimumPerMember)
		minimum = &v
	}

	created, err := a.ledger.OpenIncidental(r.Context(), ledger.OpenIncidentalParams{
		FundID:             fund.ID,
		Occasion:           req.Occasion,
		TargetAmount:       target,
		OpenedOn:           req.OpenedOn,
		MinimumPerMember:   minimum,
		RecipientMemberIDs: req.RecipientMemberIDs,
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
// collected/disbursed totals PRD section 7.5 shows for it.
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
	AccountID int64   `json:"account_id"`
	ClosedOn  string  `json:"closed_on"`
	Note      *string `json:"note"`
}

// closeIncidentalResponse is POST /api/incidentals/{purposeID}/close's body:
// the now-closed envelope, and the amount rolled - signed (ADR-031):
// positive rolled out to Kas Utama, negative covered a shortfall from Kas
// Utama, zero means the envelope landed square and nothing posted.
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
		Note:      req.Note,
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

// reopenIncidental is POST /api/incidentals/{purposeID}/reopen: wraps
// Ledger.ReopenIncidental, the deliberate, visible way back ADR-031 gives a
// closed envelope so a late entry has somewhere to post. No request body -
// there is nothing to say about a reopen beyond which envelope, matching
// GET /api/incidentals/{purposeID}'s own no-body idiom rather than close's
// account/date/note.
//
// 200, not 201: the response addresses the envelope the caller already
// named, now reopened, the same reasoning closeIncidental's own comment
// gives for its status code.
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
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toIncidentalResponse(reopened))
}

// updateIncidentalParticipationRequest is PATCH /api/incidentals/{purposeID}'s
// body: the envelope's minimum and its recipients (ADR-034), the two facets
// PATCH /api/purposes/{id} does not reach - that route corrects occasion
// alone, through RenameIncidental, and stays that way rather than growing a
// second, overlapping way to edit the same envelope.
//
// Both fields fully replace their own facet, the same "editable like
// occasion" shape SetIncidentalParticipation itself carries: nil
// MinimumPerMember clears it, and RecipientMemberIDs (nil or empty alike)
// replaces the whole recipient set, never an add/remove delta.
type updateIncidentalParticipationRequest struct {
	MinimumPerMember   *int64  `json:"minimum_per_member"`
	RecipientMemberIDs []int64 `json:"recipient_member_ids"`
}

// updateIncidentalParticipation is PATCH /api/incidentals/{purposeID}: wraps
// Ledger.SetIncidentalParticipation. Handlers decode and pass through -
// minimum_per_member's shape check is that method's job alone (ADR-027).
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

	var minimum *money.Amount
	if req.MinimumPerMember != nil {
		v := money.Amount(*req.MinimumPerMember)
		minimum = &v
	}

	updated, err := a.ledger.SetIncidentalParticipation(r.Context(), ledger.SetIncidentalParticipationParams{
		FundID:             fund.ID,
		PurposeID:          purposeID,
		MinimumPerMember:   minimum,
		RecipientMemberIDs: req.RecipientMemberIDs,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toIncidentalResponse(updated))
}

// participationStateResponse is one expected member's row on GET
// /api/incidentals/{purposeID}/participation (ADR-034). member is the full
// roster shape members.go already exposes, the same choice
// duesStatusResponse's own comment makes for the reconcile flow's roster.
type participationStateResponse struct {
	Member            memberResponse `json:"member"`
	ContributedAmount int64          `json:"contributed_amount"`
	State             string         `json:"state"`
}

func toParticipationStateResponse(p ledger.MemberParticipation) participationStateResponse {
	return participationStateResponse{
		Member:            toMemberResponse(p.Member),
		ContributedAmount: p.ContributedAmount.Int64(),
		State:             string(p.State),
	}
}

// unexpectedContributionResponse is one row of the "Sumbangan lain" list
// (ADR-034): a contribution from someone the envelope did not expect. No
// state - unexpected is not itself a status, only an amount.
type unexpectedContributionResponse struct {
	Member            memberResponse `json:"member"`
	ContributedAmount int64          `json:"contributed_amount"`
}

func toUnexpectedContributionResponse(u ledger.UnexpectedContribution) unexpectedContributionResponse {
	return unexpectedContributionResponse{
		Member:            toMemberResponse(u.Member),
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
// wraps Ledger.GetIncidentalParticipation - PRD section 7.5's "who has
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
		mapLedgerError(w, a.logger, err)
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
