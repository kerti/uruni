package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// duesStatusResponse is one row of GET /api/dues-status's roster: a member who
// owes dues for the requested period, classified by DuesStatusForPeriod (PRD
// section 7.3). member is the full roster shape of members.go, not a trimmed
// id+name: the reconcile flow wants the fields the roster screen shows.
type duesStatusResponse struct {
	// Member.tier_id is the tier at the requested period (ADR-040), not the
	// member's current tier.
	Member     memberResponse `json:"member"`
	OwedAmount int64          `json:"owed_amount"`
	PaidAmount int64          `json:"paid_amount"`
	Status     string         `json:"status"`
	// PaidThrough is "YYYY-MM" for a paid-in-advance member whose unbroken
	// run of paid periods reaches past this one (#357), null otherwise.
	PaidThrough *string `json:"paid_through"`
}

func toDuesStatusResponse(s ledger.MemberDuesStatus, arrearsMonths int) duesStatusResponse {
	member := toMemberResponse(s.Member)
	member.ArrearsMonths = arrearsMonths
	resp := duesStatusResponse{
		Member:     member,
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
//
// Each row's member.arrears_months is the Anggota roster's number, from the
// same derivation (ArrearsMonthsForMembers) against the real current month,
// whatever period was asked for: the Status card shows the same Tunggakan the
// roster does (#740).
func (a *api) getDuesStatus(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	loc, ok := a.fundLocation(w, r, fund)
	if !ok {
		return
	}

	statuses, err := a.ledger.DuesStatusForPeriod(r.Context(), fund.ID, r.URL.Query().Get("period"))
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	members := make([]store.Member, len(statuses))
	for i, s := range statuses {
		members[i] = s.Member.Member
	}
	arrears, err := a.ledger.ArrearsMonthsForMembers(r.Context(), fund.ID, members, ledger.CurrentDuesPeriod(a.now(), loc))
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := make([]duesStatusResponse, 0, len(statuses))
	for _, s := range statuses {
		resp = append(resp, toDuesStatusResponse(s, arrears[s.Member.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// duesGridResponse is GET /api/dues-status/grid (ADR-044): the twelve-month
// dues status table. periods are "YYYY-MM", oldest first, and every member's
// cells run parallel to them. A cell is "paid", "partial", "unpaid" or "none"
// (no obligation that month); paying ahead is "paid". Status only: no amounts,
// counts or totals, by design.
type duesGridResponse struct {
	Periods []string                 `json:"periods"`
	Members []duesGridMemberResponse `json:"members"`
}

type duesGridMemberResponse struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Cells []string `json:"cells"`
}

// getDuesStatusGrid is GET /api/dues-status/grid: wraps DuesGrid for the
// fund's current month, read on the server in the fund's zone (ADR-041). It
// takes no parameters: the client names no month, so the window cannot reach
// past the present or be asked for anything the report does not already show.
func (a *api) getDuesStatusGrid(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	grid, err := a.ledger.DuesGrid(r.Context(), fund.ID, "")
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := duesGridResponse{Periods: grid.Periods, Members: make([]duesGridMemberResponse, 0, len(grid.Rows))}
	for _, row := range grid.Rows {
		cells := make([]string, len(row.Cells))
		for i, c := range row.Cells {
			cells[i] = string(c)
		}
		resp.Members = append(resp.Members, duesGridMemberResponse{ID: row.MemberID, Name: row.Name, Cells: cells})
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

	periods, err := a.ledger.OutstandingDuesForMember(r.Context(), fund.ID, memberID, r.URL.Query().Get("through"))
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
