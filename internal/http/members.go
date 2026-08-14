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

// resolveMember reads the {id} path segment PATCH and DELETE /api/members/{id}
// both need and looks the member up, or answers the request itself and
// reports false - the same shape as dues_rates.go's resolveDuesTier, for the
// same reason: a non-numeric id is a 400 this layer alone can see, and a
// well-formed id naming no row is a 404 read ahead of the write rather than
// leaning on mapSQLiteError's sql.ErrNoRows case, which a DELETE affecting
// zero rows would never even reach.
func (a *api) resolveMember(w http.ResponseWriter, r *http.Request) (store.Member, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The member id is not a valid number.")
		return store.Member{}, false
	}

	member, err := a.queries.GetMember(r.Context(), id)
	if err != nil {
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.Member{}, false
	}
	return member, true
}

// updateMemberRequest is PATCH /api/members/{id}'s body. Every field is
// optional - an absent key means "leave alone" - and tier_id, joined_on and
// inactive_on can each also be sent explicitly as JSON null, which means
// "clear it": clearing tier_id removes the member's dues obligation
// entirely, and clearing inactive_on reinstates a member who had been marked
// inactive. Decoding straight into a struct of *T (or even **T - verified
// against encoding/json directly, not assumed: a missing key and an explicit
// null both leave a **T field nil) cannot tell "absent" from "present and
// null" apart, so this type is filled in from a map[string]json.RawMessage
// instead: each field's own *Set bool records whether its key appeared in
// the body at all, independent of what it decoded to.
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

// updateMember is PATCH /api/members/{id}: a correction to reference data
// (issue #81), never a new ledger event - a transaction references a member
// by id, so renaming one or reassigning its tier breaks nothing already
// posted. Setting inactive_on is exactly what internal/ledger's
// DuesStatusForPeriod (M3) already interprets: the member owes their final
// month in full and is excluded from every period after it. This route only
// exposes the column; the semantics live there and stay there.
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

// deleteMember is DELETE /api/members/{id}: for a duplicate added twice at
// setup, never for a member who actually left (issue #81) - that is what
// inactive_on is for. No pre-check for referencing rows: the composite
// foreign keys from "transaction" and reimbursement already refuse this
// once real rows point at the member, and mapSQLiteError turns that refusal
// into a clean 409. A hand-rolled COUNT(*) first would only race the
// constraint the schema already enforces.
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
