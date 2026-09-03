package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

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
type accountRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// accountResponse is the wire shape of an account row. No fund_id, same
// reasoning as memberResponse. kind is exposed because it is the whole point
// of the row - cash and bank reconcile differently (PRD §7.7). inactive_on
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

// createAccount is POST /api/accounts: a direct-CRUD write (ADR-027), the
// same shape createMember and createDuesTier already use. #78 overturned the
// assumption that only SetUpFund ever creates an account - a treasurer who
// opens a second bank account, or realizes setup under-counted, needs this
// afterward too.
func (a *api) createAccount(w http.ResponseWriter, r *http.Request) {
	var req accountRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	account, err := a.queries.CreateAccount(r.Context(), store.CreateAccountParams{
		FundID:    fund.ID,
		Kind:      req.Kind,
		Name:      req.Name,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toAccountResponse(account))
}

// listAccounts is GET /api/accounts: every location the fund has - however
// many the treasurer named at setup, plus anything added afterward through
// POST /api/accounts above, retired ones (inactive_on set) included, since
// history still needs to render them (PRD §7.8's reconcile flow and PRD
// §7.9's report both read past entries against a location that no longer
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

// postOpeningBalanceRequest is POST /api/accounts/{id}/opening-balance's
// body: an account's starting figure (PRD §7.1). No purpose_id - an opening
// balance is always tagged to the fund's one kind='main' purpose, the same
// way OpenIncidental fixes its own purpose's kind server-side rather than
// taking it on the wire.
type postOpeningBalanceRequest struct {
	Amount     int64   `json:"amount"`
	OccurredOn string  `json:"occurred_on"`
	Note       *string `json:"note"`
}

// postOpeningBalanceResponse is POST /api/accounts/{id}/opening-balance's
// body. PostedAmount distinguishes "posted" from "nothing to post" the same
// way closeIncidentalResponse.RolledAmount already does for a zero roll: 0
// on the zero-amount path PostOpeningBalance's own doc comment describes,
// since a zero amount posts no row. Transaction is the posted row itself,
// nil on that same path - there is nothing yet to describe. The status code
// says the same thing a second way: 201 when a row was created, 200 when
// none was.
type postOpeningBalanceResponse struct {
	Transaction  *transactionResponse `json:"transaction"`
	PostedAmount int64                `json:"posted_amount"`
}

// postAccountOpeningBalance is POST /api/accounts/{id}/opening-balance:
// wraps Ledger.PostOpeningBalance, built and tested since M3 but never
// routed until now. A second call for the same account is
// ErrOpeningBalanceExists, already mapped to 409 (errors.go).
func (a *api) postAccountOpeningBalance(w http.ResponseWriter, r *http.Request) {
	account, ok := a.resolveAccount(w, r)
	if !ok {
		return
	}

	var req postOpeningBalanceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	mainPurposeID, ok := a.resolveMainPurposeID(w, r, fund.ID)
	if !ok {
		return
	}

	posted, err := a.ledger.PostOpeningBalance(r.Context(), ledger.PostOpeningBalanceParams{
		FundID:     fund.ID,
		AccountID:  account.ID,
		PurposeID:  mainPurposeID,
		Amount:     money.Amount(req.Amount),
		OccurredOn: req.OccurredOn,
		Note:       req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	// 201 only when a row was actually created; the zero-amount path creates
	// nothing and has no Location to name, so it answers 200. The body's
	// posted_amount is still the signal a client reads - the status just
	// stops claiming a creation that did not happen.
	if posted.ID == 0 {
		writeJSON(w, http.StatusOK, postOpeningBalanceResponse{})
		return
	}

	tr := toTransactionResponse(posted)
	writeJSON(w, http.StatusCreated, postOpeningBalanceResponse{
		Transaction:  &tr,
		PostedAmount: posted.Amount,
	})
}

// resolveMainPurposeID finds the fund's one kind='main' purpose, or answers
// the request itself (500 - this should be unreachable once SetUpFund has
// run, since purpose_single_main guarantees exactly one) and reports false.
// Mirrors internal/ledger's own unexported mainPurposeID, duplicated rather
// than exported across the package boundary for one small lookup no other
// http handler needs yet (openIncidental and createPassThroughPurpose both
// write purposes, never read "the" main one).
func (a *api) resolveMainPurposeID(w http.ResponseWriter, r *http.Request, fundID int64) (int64, bool) {
	purposes, err := a.queries.ListPurposesByFund(r.Context(), fundID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return 0, false
	}
	for _, p := range purposes {
		if p.Kind == "main" {
			return p.ID, true
		}
	}
	a.logger.Error("fund has no main purpose", "fund_id", fundID)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	return 0, false
}
