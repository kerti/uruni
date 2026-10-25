package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// accountRequest is POST /api/accounts's body: one more location for an
// existing fund (setup asks for the first batch, #78). Shape only: a
// malformed kind and a blank name are the schema's CHECKs to refuse (ADR-027).
//
// OpeningBalance is optional and, per #230, the only way to give a location
// added after setup a starting figure: a location and its opening balance are
// born together in one transaction, or not at all. Absent is the same as 0;
// neither posts a row.
type accountRequest struct {
	Kind           ledger.AccountKind     `json:"kind"`
	Name           string                 `json:"name"`
	OpeningBalance *openingBalanceRequest `json:"opening_balance"`
}

// openingBalanceRequest is accountRequest's and setupRequest's shared
// opening-balance shape: an amount, its calendar date, and an optional note.
// No purpose_id: an opening balance is always tagged to the fund's
// kind='main' purpose, resolved server-side.
type openingBalanceRequest struct {
	Amount     int64   `json:"amount"`
	OccurredOn string  `json:"occurred_on"`
	Note       *string `json:"note"`
}

// toOpeningBalance converts the wire shape to the ledger's value type, or nil
// when req is nil ("absent means no opening balance", see accountRequest).
func (req *openingBalanceRequest) toOpeningBalance() *ledger.OpeningBalance {
	if req == nil {
		return nil
	}
	return &ledger.OpeningBalance{
		Amount:     money.Amount(req.Amount),
		OccurredOn: req.OccurredOn,
		Note:       req.Note,
	}
}

// accountResponse is the wire shape of an account row. No fund_id, as for
// memberResponse. kind is exposed because cash and bank reconcile differently
// (PRD section 7.7). inactive_on (#134) is null for a location in use, a date
// for a retired one.
type accountResponse struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

// accountListItem is an account as GET /api/accounts lists it: the row plus
// opening_on (#637), the date of the location's opening entry, null when it has
// none. Only the list carries it (the one read the entry forms load), so no
// other route answers a null that would read as "no opening".
type accountListItem struct {
	accountResponse
	OpeningOn *string `json:"opening_on"`
}

func toAccountResponse(a store.Account) accountResponse {
	return accountResponse{ID: a.ID, Kind: a.Kind, Name: a.Name, InactiveOn: a.InactiveOn, CreatedAt: a.CreatedAt}
}

// createAccount is POST /api/accounts. #78 overturned the assumption that only
// SetUpFund creates accounts: a treasurer who opens a second bank account, or
// finds setup under-counted, needs this afterward.
//
// An optional opening balance rides the request, and Ledger.CreateAccount
// posts it in the account's transaction.
//
// Keyed (ADR-043).
func (a *api) createAccount(w http.ResponseWriter, r *http.Request) {
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	idem := idempotencyFrom(r)
	account, err := a.ledger.CreateAccount(r.Context(), ledger.CreateAccountParams{
		FundID:         fund.ID,
		Kind:           req.Kind,
		Name:           req.Name,
		OpeningBalance: req.OpeningBalance.toOpeningBalance(),
		Idempotency:    idem,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	markReplay(w, r, a.log(r), idem)
	writeJSON(w, http.StatusCreated, toAccountResponse(account))
}

// listAccounts is GET /api/accounts: every location the fund has, retired
// ones (inactive_on set) included, since history still renders them (PRD
// sections 7.8, 7.9: past entries read against a location that no longer
// takes new counts).
func (a *api) listAccounts(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	accounts, err := a.queries.ListAccountsByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	openings, err := a.queries.ListAccountOpeningDates(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}
	openingOn := make(map[int64]string, len(openings))
	for _, o := range openings {
		openingOn[o.AccountID] = o.OpeningOn
	}

	resp := make([]accountListItem, 0, len(accounts))
	for _, acc := range accounts {
		item := accountListItem{accountResponse: toAccountResponse(acc)}
		if on, ok := openingOn[acc.ID]; ok {
			item.OpeningOn = &on
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveAccount looks up {id} within the fund, or answers the request and
// reports false. Mirrors resolveMember: fund-scoped in the query, and a
// pre-fetch because a DELETE affecting zero rows never raises sql.ErrNoRows.
func (a *api) resolveAccount(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The account id is not a valid number.")
		return store.Account{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.Account{}, false
	}

	account, err := a.queries.GetAccountForFund(r.Context(), store.GetAccountForFundParams{
		ID:     id,
		FundID: fund.ID,
	})
	if err != nil {
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return store.Account{}, false
	}
	return account, true
}

// updateAccountRequest is PATCH /api/accounts/{id}'s body. An absent key means
// "leave alone"; an explicit null on inactive_on means "reinstate", with the
// *Set-flag decode of updateMemberRequest for that ambiguity.
type updateAccountRequest struct {
	Name          *string
	NameSet       bool
	Kind          *string
	KindSet       bool
	InactiveOn    *string
	InactiveOnSet bool
}

func decodeUpdateAccountRequest(w http.ResponseWriter, r *http.Request) (updateAccountRequest, bool) {
	var raw map[string]json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return updateAccountRequest{}, false
	}

	var req updateAccountRequest
	fields := []struct {
		key string
		set *bool
		dst any
	}{
		{"name", &req.NameSet, &req.Name},
		{"kind", &req.KindSet, &req.Kind},
		{"inactive_on", &req.InactiveOnSet, &req.InactiveOn},
	}
	known := make([]string, 0, len(fields))
	for _, f := range fields {
		known = append(known, f.key)
	}
	if key, unknown := unknownKey(raw, known); unknown {
		writeUnknownField(w, key)
		return updateAccountRequest{}, false
	}
	for _, f := range fields {
		v, ok := raw[f.key]
		if !ok {
			continue
		}
		*f.set = true
		if err := json.Unmarshal(v, f.dst); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
			return updateAccountRequest{}, false
		}
	}
	return req, true
}

// updateAccount is PATCH /api/accounts/{id}: a correction to a location's
// label, not a ledger event. account.name is a label, not a posted fact, so
// rule 3's immutability does not reach it (posted rows reference the account
// by id). kind is correctable alike: nothing in internal/ledger branches on
// it, so 'cash' vs 'bank' is a label, and the wrong one is a typo (the
// schema's CHECK still allows only the two, ADR-027). inactive_on is #134's
// retirement date, or an explicit null to reinstate. Retiring a location that
// holds money is 409 account_holds_money (#474), so the write goes through
// Ledger.UpdateAccount.
func (a *api) updateAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := a.resolveAccount(w, r)
	if !ok {
		return
	}

	req, ok := decodeUpdateAccountRequest(w, r)
	if !ok {
		return
	}

	params := store.UpdateAccountParams{ID: account.ID}
	if req.NameSet {
		params.Name = req.Name
	}
	if req.KindSet {
		params.Kind = req.Kind
	}
	if req.InactiveOnSet {
		params.SetInactiveOn = 1
		params.InactiveOn = req.InactiveOn
	}

	updated, err := a.ledger.UpdateAccount(r.Context(), account.FundID, params)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toAccountResponse(updated))
}

// deleteAccount is DELETE /api/accounts/{id}: for a never-used duplicate added
// at setup or by mistake, not a location used then retired (inactive_on, see
// updateAccount). No pre-check for referencing rows: the composite foreign
// keys refuse it (409 referenced_by_other_records), and a COUNT(*) first would
// only race them. Mirrors deleteMember.
func (a *api) deleteAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := a.resolveAccount(w, r)
	if !ok {
		return
	}

	if err := a.ledger.DeleteAccount(r.Context(), ledger.DeleteAccountParams{FundID: account.FundID, AccountID: account.ID}); err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
