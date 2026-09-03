package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

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

// resolveMember looks up {id} within the fund, or answers the request and
// reports false. The lookup is a pre-fetch rather than leaning on
// sql.ErrNoRows, which a DELETE affecting zero rows never raises.
//
// It resolves the fund itself rather than taking one: every caller is a
// fund-scoped route, and the scope belongs in the query (GetMemberForFund)
// rather than in a check each handler has to remember - which is how this
// helper came to be unscoped through #188.
func (a *api) resolveMember(w http.ResponseWriter, r *http.Request) (store.Member, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The member id is not a valid number.")
		return store.Member{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.Member{}, false
	}

	member, err := a.queries.GetMemberForFund(r.Context(), store.GetMemberForFundParams{
		ID:     id,
		FundID: fund.ID,
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.Member{}, false
	}
	return member, true
}

// updateMemberRequest is PATCH /api/members/{id}'s body. An absent key means
// "leave alone"; an explicit null on tier_id, joined_on or inactive_on means
// "clear it" - clearing tier_id drops the dues obligation, clearing
// inactive_on reinstates the member.
//
// Hence the *Set flags and the map decode below: no struct tag can carry this
// distinction. Both *T and **T leave the field nil for a missing key and for
// an explicit null alike (checked against encoding/json, not assumed).
type updateMemberRequest struct {
	Name          *string
	NameSet       bool
	TierID        *int64
	TierIDSet     bool
	JoinedOn      *string
	JoinedOnSet   bool
	InactiveOn    *string
	InactiveOnSet bool
}

func decodeUpdateMemberRequest(w http.ResponseWriter, r *http.Request) (updateMemberRequest, bool) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return updateMemberRequest{}, false
	}

	var req updateMemberRequest
	fields := []struct {
		key string
		set *bool
		dst any
	}{
		{"name", &req.NameSet, &req.Name},
		{"tier_id", &req.TierIDSet, &req.TierID},
		{"joined_on", &req.JoinedOnSet, &req.JoinedOn},
		{"inactive_on", &req.InactiveOnSet, &req.InactiveOn},
	}
	for _, f := range fields {
		v, ok := raw[f.key]
		if !ok {
			continue
		}
		*f.set = true
		if err := json.Unmarshal(v, f.dst); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
			return updateMemberRequest{}, false
		}
	}
	return req, true
}

// updateMember is PATCH /api/members/{id}: a correction to reference data,
// not a ledger event - a transaction references a member by id, so a rename
// or a tier change breaks nothing already posted. inactive_on only exposes
// the column; DuesStatusForPeriod owns what it means.
func (a *api) updateMember(w http.ResponseWriter, r *http.Request) {
	member, ok := a.resolveMember(w, r)
	if !ok {
		return
	}

	req, ok := decodeUpdateMemberRequest(w, r)
	if !ok {
		return
	}

	params := store.UpdateMemberParams{ID: member.ID}
	if req.NameSet {
		params.Name = req.Name
	}
	if req.TierIDSet {
		params.SetTierID = 1
		params.TierID = req.TierID
	}
	if req.JoinedOnSet {
		params.SetJoinedOn = 1
		params.JoinedOn = req.JoinedOn
	}
	if req.InactiveOnSet {
		params.SetInactiveOn = 1
		params.InactiveOn = req.InactiveOn
	}

	updated, err := a.queries.UpdateMember(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toMemberResponse(updated))
}

// deleteMember is DELETE /api/members/{id}: for a duplicate added at setup,
// never for a member who left - that is inactive_on. No pre-check for
// referencing rows; the composite foreign keys already refuse it, and a
// COUNT(*) first would only race them.
func (a *api) deleteMember(w http.ResponseWriter, r *http.Request) {
	member, ok := a.resolveMember(w, r)
	if !ok {
		return
	}

	if err := a.queries.DeleteMember(r.Context(), member.ID); err != nil {
		mapSQLiteDeleteError(w, a.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
