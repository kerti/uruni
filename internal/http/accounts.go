package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// accountRequest is POST /api/accounts's body: one more location for an
// existing fund (#78's other half - setup asks for the first batch,
// POST /api/accounts is what adds to them afterward). Shape only - a
// malformed kind and a blank name are the schema's own CHECKs to refuse, not
// re-checked here (ADR-027).
//
// OpeningBalance is optional and, per #230's uniform rule, the only way left
// to give a location added after setup a starting figure: a location and its
// opening balance are born together, in the same database transaction, or
// not at all. Absent is the same as an amount of 0 - neither posts a row.
type accountRequest struct {
	Kind           string                 `json:"kind"`
	Name           string                 `json:"name"`
	OpeningBalance *openingBalanceRequest `json:"opening_balance"`
}

// openingBalanceRequest is accountRequest's and setupRequest's shared
// opening-balance shape: an amount, the calendar date it is dated, and an
// optional note. No purpose_id - an opening balance is always tagged to the
// fund's one kind='main' purpose, resolved server-side.
type openingBalanceRequest struct {
	Amount     int64   `json:"amount"`
	OccurredOn string  `json:"occurred_on"`
	Note       *string `json:"note"`
}

// toOpeningBalance converts the wire shape to the ledger's own value type, or
// nil when req itself is nil - the same "absent means no opening balance"
// rule accountRequest's own doc comment states.
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

// accountResponse is the wire shape of an account row. No fund_id, same
// reasoning as memberResponse. kind is exposed because it is the whole point
// of the row - cash and bank reconcile differently (PRD section 7.7). inactive_on
// is #134's account-lifecycle half: null for a location still in use, a date
// for one retired (mirrors memberResponse.InactiveOn exactly).
type accountResponse struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

func toAccountResponse(a store.Account) accountResponse {
	return accountResponse{ID: a.ID, Kind: a.Kind, Name: a.Name, InactiveOn: a.InactiveOn, CreatedAt: a.CreatedAt}
}

// createAccount is POST /api/accounts. #78 overturned the assumption that
// only SetUpFund ever creates an account - a treasurer who opens a second
// bank account, or realizes setup under-counted, needs this afterward too.
//
// #230 moved this off the direct-CRUD path createMember and createDuesTier
// still use: an optional opening balance now rides the same request, and
// Ledger.CreateAccount is what posts it inside the same transaction as the
// account itself - a location and its opening balance are born together, or
// not at all, so mapLedgerError (not mapSQLiteError) is what answers a
// refusal here, the same as every other ledger-backed write.
func (a *api) createAccount(w http.ResponseWriter, r *http.Request) {
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	account, err := a.ledger.CreateAccount(r.Context(), ledger.CreateAccountParams{
		FundID:         fund.ID,
		Kind:           req.Kind,
		Name:           req.Name,
		OpeningBalance: req.OpeningBalance.toOpeningBalance(),
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toAccountResponse(account))
}

// listAccounts is GET /api/accounts: every location the fund has - however
// many the treasurer named at setup, plus anything added afterward through
// POST /api/accounts above, retired ones (inactive_on set) included, since
// history still needs to render them (PRD section 7.8's reconcile flow and PRD
// section 7.9's report both read past entries against a location that no longer
// takes new counts).
func (a *api) listAccounts(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	accounts, err := a.queries.ListAccountsByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]accountResponse, 0, len(accounts))
	for _, acc := range accounts {
		resp = append(resp, toAccountResponse(acc))
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveAccount looks up {id} within the fund, or answers the request and
// reports false. Mirrors resolveMember exactly - fund-scoped in the query,
// and a pre-fetch rather than leaning on sql.ErrNoRows, which a DELETE
// affecting zero rows never raises.
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
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.Account{}, false
	}
	return account, true
}

// updateAccountRequest is PATCH /api/accounts/{id}'s body. An absent key
// means "leave alone"; an explicit null on inactive_on means "reinstate" -
// the same *Set-flag decode shape updateMemberRequest already uses for
// exactly this ambiguity (absent vs. explicit null), rather than a second
// convention for it in this package.
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
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
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
// own label, not a ledger event - account.name is a label on a location, not
// a posted fact, so rule 3's immutability does not reach it (renaming breaks
// nothing already posted, which still references the account by id).
// kind is correctable the same way and for the same reason: nothing in
// internal/ledger branches on it, so 'cash' vs 'bank' is a label on the
// location, not a rule about the money in it. A location entered as the
// wrong one is a typo, and the schema's CHECK still refuses anything but
// the two (ADR-027 - shape is the schema's to police, not this handler's).
// inactive_on is the other half of #134's account-lifecycle ruling: the
// retirement date for a used-then-retired location, or an explicit null to
// reinstate it.
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

	updated, err := a.queries.UpdateAccount(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toAccountResponse(updated))
}

// deleteAccount is DELETE /api/accounts/{id}: for a never-used duplicate
// added at setup or by mistake, never for a location that was actually used
// and then retired - that is inactive_on (updateAccount above). No
// pre-check for referencing rows; the composite foreign keys already refuse
// it, and a COUNT(*) first would only race them. Mirrors deleteMember
// verbatim.
func (a *api) deleteAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := a.resolveAccount(w, r)
	if !ok {
		return
	}

	if err := a.queries.DeleteAccount(r.Context(), account.ID); err != nil {
		mapSQLiteDeleteError(w, a.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
