package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
)

// duesStatusResponse is one row of GET /api/dues-status's roster: a member who
// owes dues for the requested period, classified by DuesStatusForPeriod (PRD
// section 7.3). member is the full roster shape of members.go, not a trimmed
// id+name: the reconcile flow wants the fields the roster screen shows.
type duesStatusResponse struct {
	// Member.tier_id is the tier at the requested period - the one that priced
	// owed_amount (ADR-040) - not the member's current tier.
	Member     memberResponse `json:"member"`
	OwedAmount int64          `json:"owed_amount"`
	PaidAmount int64          `json:"paid_amount"`
	Status     string         `json:"status"`
	// PaidThrough is "YYYY-MM" for a paid-in-advance member whose unbroken
	// run of paid periods reaches past this one (#357), null otherwise.
	PaidThrough *string `json:"paid_through"`
}

func toDuesStatusResponse(s ledger.MemberDuesStatus) duesStatusResponse {
	resp := duesStatusResponse{
		Member:     toMemberResponse(s.Member),
		OwedAmount: s.OwedAmount.Int64(),
		PaidAmount: s.PaidAmount.Int64(),
		Status:     string(s.Status),
	}
	if s.PaidThrough != "" {
		resp.PaidThrough = &s.PaidThrough
	}
	return resp
}

// getDuesStatus is GET /api/dues-status?period=YYYY-MM: wraps
// DuesStatusForPeriod. period is passed through unvalidated: a missing or
// malformed value reaches validateDuesPeriod and returns as
// ErrInvalidArgument through mapLedgerError, the single source of truth
// (ADR-027).
func (a *api) getDuesStatus(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	statuses, err := a.ledger.DuesStatusForPeriod(r.Context(), fund.ID, r.URL.Query().Get("period"))
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := make([]duesStatusResponse, 0, len(statuses))
	for _, s := range statuses {
		resp = append(resp, toDuesStatusResponse(s))
	}
	writeJSON(w, http.StatusOK, resp)
}

// outstandingDuesResponse is one row of GET /api/members/{id}/outstanding-dues:
// a period the path's member still owes something for, oldest first. No nested
// member (the id is the path segment); status is only "unpaid" or "partial"
// (see OutstandingDuesForMember).
type outstandingDuesResponse struct {
	Period     string `json:"period"`
	OwedAmount int64  `json:"owed_amount"`
	PaidAmount int64  `json:"paid_amount"`
	Status     string `json:"status"`
}

func toOutstandingDuesResponse(p ledger.OutstandingDuesPeriod) outstandingDuesResponse {
	return outstandingDuesResponse{
		Period:     p.Period,
		OwedAmount: p.OwedAmount.Int64(),
		PaidAmount: p.PaidAmount.Int64(),
		Status:     string(p.Status),
	}
}

// getOutstandingDues is GET /api/members/{id}/outstanding-dues: wraps
// OutstandingDuesForMember. through is the optional ?through=YYYY-MM, passed
// through unvalidated as getDuesStatus does period: missing means the
// server's current month (OutstandingDuesForMember decides), malformed
// returns ErrInvalidArgument through mapLedgerError.
//
// {id} is parsed here, not through resolveMember: that GetMember lookup takes
// no fund_id, so it cannot guard fund scope. GetMemberForFund inside
// OutstandingDuesForMember does: another fund's member id reaches
// sql.ErrNoRows, which mapLedgerError turns into 404 (ADR-029's
// GetTransactionForFund shape, for members).
func (a *api) getOutstandingDues(w http.ResponseWriter, r *http.Request) {
	memberID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The member id is not a valid number.")
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	periods, err := a.ledger.OutstandingDuesForMember(r.Context(), fund.ID, memberID, r.URL.Query().Get("through"), a.now())
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := make([]outstandingDuesResponse, 0, len(periods))
	for _, p := range periods {
		resp = append(resp, toOutstandingDuesResponse(p))
	}
	writeJSON(w, http.StatusOK, resp)
}
