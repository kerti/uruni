package http

import (
	"net/http"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// memberRequest is POST /api/members's body. inactive_on is deliberately
// absent: marking a member inactive is a deactivation route #65 puts out of
// scope (no UpdateMember query exists), so nothing on the wire can set it at
// creation either - CreateMember always gets a nil InactiveOn here.
type memberRequest struct {
	Name     string  `json:"name"`
	TierID   *int64  `json:"tier_id"`
	JoinedOn *string `json:"joined_on"`
}

// memberResponse is the wire shape of a member row. No fund_id: v1 is
// exactly one fund and no route in this package takes one, so echoing it
// back would name a value the client already implicitly knows and can do
// nothing with.
type memberResponse struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	TierID     *int64  `json:"tier_id"`
	JoinedOn   *string `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

func toMemberResponse(m store.Member) memberResponse {
	return memberResponse{
		ID:         m.ID,
		Name:       m.Name,
		TierID:     m.TierID,
		JoinedOn:   m.JoinedOn,
		InactiveOn: m.InactiveOn,
		CreatedAt:  m.CreatedAt,
	}
}

// createMember is POST /api/members: a direct-CRUD write (ADR-027), so it
// calls a.queries itself. Validation is JSON-shape only - an empty name or a
// tier_id naming no dues_tier reaches SQLite's own CHECK/FOREIGN KEY and
// comes back through mapSQLiteError, the single source of truth for those
// rules (#65).
func (a *api) createMember(w http.ResponseWriter, r *http.Request) {
	var req memberRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	member, err := a.queries.CreateMember(r.Context(), store.CreateMemberParams{
		FundID:     fund.ID,
		Name:       req.Name,
		TierID:     req.TierID,
		JoinedOn:   req.JoinedOn,
		InactiveOn: nil,
		CreatedAt:  time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toMemberResponse(member))
}

// listMembers is GET /api/members: the whole roster for the one fund, in
// insertion order (ListMembersByFund's own ORDER BY id) - #65 doesn't ask
// for filtering or pagination, and the PRD's roster is small enough that
// none is needed yet.
func (a *api) listMembers(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	members, err := a.queries.ListMembersByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]memberResponse, 0, len(members))
	for _, m := range members {
		resp = append(resp, toMemberResponse(m))
	}
	writeJSON(w, http.StatusOK, resp)
}
