package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
)

// duesStatusResponse is one row of GET /api/dues-status's roster: one member
// who owes dues for the requested period, classified against
// DuesStatusForPeriod (PRD §7.3). member is the full roster shape members.go
// already exposes, not a trimmed id+name pair - the reconcile flow reading
// this list wants the same member fields the roster screen shows.
type duesStatusResponse struct {
	Member     memberResponse `json:"member"`
	OwedAmount int64          `json:"owed_amount"`
	PaidAmount int64          `json:"paid_amount"`
	Status     string         `json:"status"`
}

func toDuesStatusResponse(s ledger.MemberDuesStatus) duesStatusResponse {
	return duesStatusResponse{
		Member:     toMemberResponse(s.Member),
		OwedAmount: s.OwedAmount.Int64(),
		PaidAmount: s.PaidAmount.Int64(),
		Status:     string(s.Status),
	}
}

// getDuesStatus is GET /api/dues-status?period=YYYY-MM: wraps
// DuesStatusForPeriod. The period query parameter is passed through
// unvalidated - a missing or malformed period reaches
// DuesStatusForPeriod's own validateDuesPeriod check and comes back as
// ErrInvalidArgument through mapLedgerError, the same single source of
// truth every other route in this slice defers to (ADR-027).
func (a *api) getDuesStatus(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	statuses, err := a.ledger.DuesStatusForPeriod(r.Context(), fund.ID, r.URL.Query().Get("period"))
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	resp := make([]duesStatusResponse, 0, len(statuses))
	for _, s := range statuses {
		resp = append(resp, toDuesStatusResponse(s))
	}
	writeJSON(w, http.StatusOK, resp)
}

// outstandingDuesResponse is one row of GET
// /api/members/{id}/outstanding-dues's result: one period the path's member
// still owes something for, oldest first. No nested member object - the id
// is already the path segment - and status is only ever "unpaid" or
// "partial", per OutstandingDuesForMember's own doc comment.
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
// OutstandingDuesForMember. through is the optional ?through=YYYY-MM query
// parameter, passed through unvalidated exactly as getDuesStatus passes
// period through - a missing value means "default to the server's current
// month" (OutstandingDuesForMember's own job to decide), and a malformed one
// reaches its validateDuesPeriod check and comes back as ErrInvalidArgument
// through mapLedgerError, same as every other route in this slice.
//
// {id} is parsed here rather than through resolveMember: that helper's own
// GetMember lookup takes no fund_id, so it cannot be this route's fund-scope
// guard. Fund scoping here is GetMemberForFund's job inside
// OutstandingDuesForMember itself - a member id belonging to another fund
// never reaches a lookup that would find it, it reaches sql.ErrNoRows, which
// mapLedgerError already turns into 404 (ADR-029's GetTransactionForFund
// shape, applied here to members).
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

	periods, err := a.ledger.OutstandingDuesForMember(r.Context(), fund.ID, memberID, r.URL.Query().Get("through"))
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	resp := make([]outstandingDuesResponse, 0, len(periods))
	for _, p := range periods {
		resp = append(resp, toOutstandingDuesResponse(p))
	}
	writeJSON(w, http.StatusOK, resp)
}
