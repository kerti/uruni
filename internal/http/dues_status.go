package http

import (
	"net/http"

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
