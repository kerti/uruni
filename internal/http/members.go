package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
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
//
// TierName, CurrentRate and ArrearsMonths (#233, ADR-032) are set only by
// toMemberListResponse (GET /api/members' paged roster), not by
// toMemberResponse (POST/PATCH's own single-row reply) - the same split
// transactionResponse's own comment documents for its post-CreatedAt
// fields, and for the same reason: a derived, page-wide figure computed for
// the roster read has no honest answer on the single row a create or an
// update replies with, so it is left at its zero value there rather than
// costing every write an extra ledger call for a field nothing asked for.
type memberResponse struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	TierID     *int64  `json:"tier_id"`
	JoinedOn   *string `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`

	// TierName is dues_tier.name for TierID, nil for a member with no tier
	// and nil (never invented) on POST/PATCH's reply - period-independent,
	// per the issue, so it costs nothing to compute alongside every row.
	TierName *string `json:"tier_name"`
	// CurrentRate is the dues_rate in force for the current month for the
	// member's tier, nil when the member has no tier or the tier has no
	// rate effective yet (the "madya TBD" case, PRD section 6) - never an
	// invented amount, and nil on POST/PATCH's reply.
	CurrentRate *int64 `json:"current_rate"`
	// ArrearsMonths is N whole months of periods strictly before the
	// current one that the member still owes something for - the Tunggakan
	// badge's own figure (Ledger.ArrearsMonthsForMember has the full
	// derivation), 0 on POST/PATCH's reply rather than a signal of any
	// kind: a freshly created or corrected member is never read as "in
	// arrears" by this field alone, only by the roster listing.
	ArrearsMonths int `json:"arrears_months"`
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

// toMemberListResponse is GET /api/members' own row mapper (#233,
// ADR-032): everything toMemberResponse answers for a bare store.Member,
// plus the three fields the roster carries and a create/update reply does
// not. arrearsMonths is passed in rather than computed here - it takes a
// context and can fail, which a pure mapper function should not have to
// carry, so listMembers computes it per row and hands the count in.
func toMemberListResponse(m store.ListMembersPageRow, arrearsMonths int) memberResponse {
	resp := toMemberResponse(store.Member{
		ID: m.ID, FundID: m.FundID, Name: m.Name, TierID: m.TierID,
		JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt,
	})
	resp.TierName = m.TierName
	resp.CurrentRate = m.CurrentRate
	resp.ArrearsMonths = arrearsMonths
	return resp
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

// membersPageSize is GET /api/members's fixed page size (#233, ADR-032
// "Lists: paging and search"): 25 a page, no client-supplied limit - the
// same constant transactionsPageSize sets for its own list.
const membersPageSize = 25

// membersPageResponse is GET /api/members's envelope (#233): always this
// shape, on the one route - no second, unpaginated form. Same
// {"members": [...], "next_cursor": ...} idiom transactionsPageResponse
// already established for GET /api/transactions.
type membersPageResponse struct {
	Members    []memberResponse `json:"members"`
	NextCursor *string          `json:"next_cursor"`
}

// encodeMembersCursor/decodeMembersCursor mirror
// encodeTransactionsCursor/decodeTransactionsCursor exactly - base64 of
// "name|id", opaque to the client, round-tripped rather than parsed. The
// keyset pair is (name, id) here rather than (occurred_on, id), because
// this list orders ascending by name with id only as the tiebreak
// (ADR-032: a roster read alphabetically, not a newest-first feed) - the
// shape of the trick is identical, only the pair's first half changes.
func encodeMembersCursor(name string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name + "|" + strconv.FormatInt(id, 10)))
}

// decodeMembersCursor rejects anything that doesn't round-trip to a
// non-empty name plus a positive id, answering 400 invalid_argument for a
// malformed cursor exactly as decodeTransactionsCursor does. Unlike a date,
// a name has no calendar to validate against - "non-empty" is the whole
// shape check, since strings.Cut already proves there was a "|" separator
// and ParseInt already proves the id half was a real integer.
func decodeMembersCursor(raw string) (name string, id int64, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", 0, false
	}
	name, idPart, found := strings.Cut(string(decoded), "|")
	if !found || name == "" {
		return "", 0, false
	}
	id, err = strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return name, id, true
}

// listMembers is GET /api/members (#233, ADR-032 "Lists: paging and
// search"): the roster, alphabetically by name with id as the tiebreak,
// keyset-paged and searchable by name - always this envelope, never the
// bare array #65 originally shipped. A direct-CRUD read (ADR-027) for the
// paging and search half - no derived invariant beyond what
// ListMembersPage's own join computes - so it calls a.queries directly for
// the page, the same split listTransactions uses for its own list.
//
// Arrears is the one field on this row that IS a derived invariant
// (CLAUDE.md rule 2), so it alone goes through a.ledger -
// ArrearsMonthsForMember, once per row on the page actually returned
// (at most membersPageSize+1, trimmed to membersPageSize before this loop
// runs) - never per member in the fund and never client-side, per the
// issue's own "no N+1" requirement.
//
// currentPeriod is computed once, here, and handed to both
// ListMembersPage's own current_period parameter (this route's
// current_rate column) and every row's ArrearsMonthsForMember call - one
// snapshot of "now" for the whole response, so a clock tick partway through
// building a page can never make one row's current_rate and another row's
// arrears disagree about what month it is. See
// Ledger.ArrearsMonthsForMember's doc comment for why this route computes
// its own current period at all rather than deferring to an omitted
// ?through=, the way GET /api/members/{id}/outstanding-dues does: this
// route carries no such query parameter for a caller to override, by
// ADR-032's own design (a period selector does not belong in Anggota), so
// there is nothing to defer to.
func (a *api) listMembers(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	params := store.ListMembersPageParams{
		FundID:        fund.ID,
		CurrentPeriod: time.Now().Format(duesPeriodLayout),
		PageLimit:     membersPageSize + 1,
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		name, id, ok := decodeMembersCursor(cursor)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The cursor is not valid.")
			return
		}
		params.CursorName = name
		params.CursorID = &id
	}

	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params.Q = q
	}

	rows, err := a.queries.ListMembersPage(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	var nextCursor *string
	if len(rows) > membersPageSize {
		rows = rows[:membersPageSize]
		last := rows[len(rows)-1]
		encoded := encodeMembersCursor(last.Name, last.ID)
		nextCursor = &encoded
	}

	resp := make([]memberResponse, 0, len(rows))
	for _, m := range rows {
		arrears, err := a.ledger.ArrearsMonthsForMember(r.Context(), fund.ID, m.ID, params.CurrentPeriod)
		if err != nil {
			mapLedgerError(w, a.logger, err)
			return
		}
		resp = append(resp, toMemberListResponse(m, arrears))
	}
	writeJSON(w, http.StatusOK, membersPageResponse{Members: resp, NextCursor: nextCursor})
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
