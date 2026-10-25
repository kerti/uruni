package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// memberRequest is POST /api/members's body. inactive_on is absent:
// deactivation is out of scope for #65 (no UpdateMember query), so nothing on
// the wire can set it at creation; CreateMember always gets a nil InactiveOn.
//
// tier_id is the member's tier from the month of joined_on (ADR-040); absent or
// null writes no tier row, and the member owes nothing.
//
// joined_on may be absent or null: the server owns PRD 7.1's "history starts
// at adoption" default (#471), so createMember dates such a member today in
// Asia/Jakarta. Backdating is the caller sending an earlier date (the
// live-arrears exception).
type memberRequest struct {
	Name     string  `json:"name"`
	TierID   *int64  `json:"tier_id"`
	JoinedOn *string `json:"joined_on"`
}

// memberResponse is the wire shape of a member row. No fund_id: v1 is one fund
// and no route here takes one.
//
// TierName, CurrentRate and ArrearsMonths (#233, ADR-032) are set only by
// toMemberListResponse (the paged roster), not toMemberResponse (the
// POST/PATCH reply), as for transactionResponse's post-CreatedAt fields: a
// page-wide derived figure has no honest answer on a single-row reply, and
// would cost every write an extra ledger call.
type memberResponse struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	TierID     *int64  `json:"tier_id"`
	JoinedOn   string  `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`

	// TierName is dues_tier.name for TierID; nil for a member with no tier, and
	// nil on POST/PATCH's reply. Period-independent, so it is cheap per row.
	TierName *string `json:"tier_name"`
	// CurrentRate is the dues_rate in force for the member's current tier; nil
	// when there is no tier or no rate effective yet (the "madya TBD" case, PRD
	// section 6), never an invented amount, and nil on POST/PATCH's reply.
	CurrentRate *int64 `json:"current_rate"`
	// ArrearsMonths is N whole months of periods strictly before the current one
	// that the member still owes something for, the Tunggakan badge's figure
	// (see Ledger.ArrearsMonthsForMember). 0 on POST/PATCH's reply: only the
	// roster listing reads a member as in arrears.
	ArrearsMonths int `json:"arrears_months"`
}

// toMemberResponse maps a ledger.Member: its TierID is the tier in force now
// (the later of the current month and the join month), except on a
// GET /api/dues-status row, where it is the tier at the requested period.
func toMemberResponse(m ledger.Member) memberResponse {
	return memberResponse{
		ID:         m.ID,
		Name:       m.Name,
		TierID:     m.TierID,
		JoinedOn:   m.JoinedOn,
		InactiveOn: m.InactiveOn,
		CreatedAt:  m.CreatedAt,
	}
}

// toMemberListResponse is GET /api/members' row mapper (#233, ADR-032):
// toMemberResponse plus the three roster fields. arrearsMonths is passed in
// because it takes a context and can fail, which a pure mapper should not
// carry; listMembers computes it once for the page.
func toMemberListResponse(m store.ListMembersPageRow, arrearsMonths int) memberResponse {
	resp := toMemberResponse(ledger.Member{
		Member: store.Member{
			ID: m.ID, FundID: m.FundID, Name: m.Name,
			JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt,
		},
		TierID: m.TierID,
	})
	resp.TierName = m.TierName
	resp.CurrentRate = m.CurrentRate
	resp.ArrearsMonths = arrearsMonths
	return resp
}

// createMember is POST /api/members: Ledger.CreateMember writes the member and,
// when a tier is given, its first member_tier row (ADR-040). Validation is
// JSON-shape only: an empty name or a tier_id naming no dues_tier reaches
// SQLite's CHECK/FOREIGN KEY and comes back through mapLedgerError (#65).
func (a *api) createMember(w http.ResponseWriter, r *http.Request) {
	var req memberRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	loc, ok := a.fundLocation(w, r, fund)
	if !ok {
		return
	}
	now := a.now()
	joinedOn := now.In(loc).Format(time.DateOnly)
	if req.JoinedOn != nil {
		joinedOn = *req.JoinedOn
	}

	member, err := a.ledger.CreateMember(r.Context(), ledger.CreateMemberParams{
		FundID:   fund.ID,
		Name:     req.Name,
		TierID:   req.TierID,
		JoinedOn: joinedOn,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toMemberResponse(member))
}

// membersPageSize is GET /api/members's fixed page size (#233, ADR-032): 25 a
// page, no client-supplied limit, like transactionsPageSize.
const membersPageSize = 25

// membersPageResponse is GET /api/members's envelope (#233): always this
// shape, no unpaginated form, the {"members": [...], "next_cursor": ...} idiom
// of transactionsPageResponse.
type membersPageResponse struct {
	Members    []memberResponse `json:"members"`
	NextCursor *string          `json:"next_cursor"`
}

// listMembers is GET /api/members (#233, ADR-032): the roster, alphabetical
// by name with id as tiebreak, keyset-paged and searchable by name; always
// this envelope, never the bare array #65 shipped. The paging and search are
// a direct-CRUD read (ADR-027) calling a.queries, as listTransactions does.
//
// Arrears is the one derived invariant on the row (CLAUDE.md rule 2), so it
// alone goes through a.ledger: ArrearsMonthsForMembers, once for the page
// returned (at most membersPageSize+1, trimmed first), a fixed number of reads
// however many members it holds or how long they have been members (#532), and
// never client-side.
//
// currentPeriod is computed once and handed to ListMembersPage's
// current_period (the current_rate column) and to ArrearsMonthsForMembers: one
// snapshot of "now", so a clock tick mid-page cannot make rows disagree about
// the month. This route has no ?through= to override it (ADR-032: a period
// selector does not belong in Anggota), so unlike
// GET /api/members/{id}/outstanding-dues there is nothing to defer to (see
// Ledger.ArrearsMonthsForMember).
func (a *api) listMembers(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	loc, ok := a.fundLocation(w, r, fund)
	if !ok {
		return
	}
	params := store.ListMembersPageParams{
		FundID:        fund.ID,
		CurrentPeriod: ledger.CurrentDuesPeriod(a.now(), loc),
		PageLimit:     membersPageSize + 1,
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		name, id, ok := decodeNameCursor(cursor)
		if !ok {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "cursor", "The cursor is not valid.")
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
		mapSQLiteError(w, a.log(r), err)
		return
	}

	var nextCursor *string
	if len(rows) > membersPageSize {
		rows = rows[:membersPageSize]
		last := rows[len(rows)-1]
		encoded := encodeCursor(last.Name, last.ID)
		nextCursor = &encoded
	}

	page := make([]store.Member, len(rows))
	for i, m := range rows {
		page[i] = store.Member{ID: m.ID, FundID: m.FundID, Name: m.Name, JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt}
	}
	arrears, err := a.ledger.ArrearsMonthsForMembers(r.Context(), fund.ID, page, params.CurrentPeriod)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := make([]memberResponse, 0, len(rows))
	for _, m := range rows {
		resp = append(resp, toMemberListResponse(m, arrears[m.ID]))
	}
	writeJSON(w, http.StatusOK, membersPageResponse{Members: resp, NextCursor: nextCursor})
}

// resolveMember looks up {id} within the fund, or answers the request and
// reports false. It pre-fetches rather than leaning on sql.ErrNoRows, which a
// DELETE affecting zero rows never raises.
//
// It resolves the fund itself: every caller is fund-scoped, and the scope
// belongs in the query (GetMemberForFund), not a check each handler must
// remember (this helper was unscoped through #188).
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
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return store.Member{}, false
	}
	return member, true
}

// updateMemberRequest is PATCH /api/members/{id}'s body. An absent key means
// "leave alone"; an explicit null on tier_id or inactive_on means "clear it"
// (clearing tier_id drops the dues obligation from the start month on, clearing
// inactive_on reinstates the member). An explicit null on joined_on is refused
// (#471): there is no "always was a member" to clear it back to.
//
// tier_effective_from ("YYYY-MM", ADR-040's "Mulai bulan") says from which month
// tier_id holds. Absent, it is the current month, or the join month if that is
// later. It needs tier_id in the same body.
//
// Hence the *Set flags and the map decode below: no struct tag carries this
// distinction. Both *T and **T leave the field nil for a missing key and an
// explicit null alike (checked against encoding/json).
type updateMemberRequest struct {
	Name          *string
	NameSet       bool
	TierID        *int64
	TierIDSet     bool
	TierFrom      *string
	TierFromSet   bool
	JoinedOn      *string
	JoinedOnSet   bool
	InactiveOn    *string
	InactiveOnSet bool
}

func decodeUpdateMemberRequest(w http.ResponseWriter, r *http.Request) (updateMemberRequest, bool) {
	var raw map[string]json.RawMessage
	if !decodeJSON(w, r, &raw) {
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
		{"tier_effective_from", &req.TierFromSet, &req.TierFrom},
		{"joined_on", &req.JoinedOnSet, &req.JoinedOn},
		{"inactive_on", &req.InactiveOnSet, &req.InactiveOn},
	}
	known := make([]string, 0, len(fields))
	for _, f := range fields {
		known = append(known, f.key)
	}
	if key, unknown := unknownKey(raw, known); unknown {
		writeUnknownField(w, key)
		return updateMemberRequest{}, false
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

// updateMember is PATCH /api/members/{id}: name, join date and leaving date are
// corrections to reference data, not ledger events (a transaction references a
// member by id, so a rename breaks nothing posted); the tier is stated "from a
// month" and the ledger decides what that writes (ADR-040).
// inactive_on only exposes the column; DuesStatusForPeriod owns what it means.
func (a *api) updateMember(w http.ResponseWriter, r *http.Request) {
	member, ok := a.resolveMember(w, r)
	if !ok {
		return
	}

	req, ok := decodeUpdateMemberRequest(w, r)
	if !ok {
		return
	}

	params := ledger.UpdateMemberParams{FundID: member.FundID, MemberID: member.ID}
	if req.NameSet {
		params.Name = req.Name
	}
	if req.TierIDSet {
		params.SetTier = true
		params.TierID = req.TierID
	}
	if req.TierFromSet {
		if req.TierFrom == nil {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "tier_effective_from", "tier_effective_from cannot be null; leave it out for the current month.")
			return
		}
		params.TierFrom = *req.TierFrom
	}
	if req.JoinedOnSet {
		if req.JoinedOn == nil {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "joined_on", "joined_on cannot be cleared; a member's history starts on a date.")
			return
		}
		params.JoinedOn = req.JoinedOn
	}
	if req.InactiveOnSet {
		params.SetInactiveOn = true
		params.InactiveOn = req.InactiveOn
	}

	updated, err := a.ledger.UpdateMember(r.Context(), params)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
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

	if err := a.ledger.DeleteMember(r.Context(), ledger.DeleteMemberParams{FundID: member.FundID, MemberID: member.ID}); err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
