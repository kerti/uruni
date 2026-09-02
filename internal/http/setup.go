package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// fundResponse is the wire shape of a fund row. sqlc's generated store.Fund
// carries no json tags (sqlc.yaml sets emit_json_tags: false, deliberately -
// nullability needs a type change more than the wire needs a shortcut), so
// this package owns the wire shape explicitly, the same way errorEnvelope and
// health already do.
type fundResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	ReportSlug string `json:"report_slug"`
	CreatedAt  int64  `json:"created_at"`
}

func toFundResponse(f store.Fund) fundResponse {
	return fundResponse{
		ID:         f.ID,
		Name:       f.Name,
		Currency:   f.Currency,
		ReportSlug: f.ReportSlug,
		CreatedAt:  f.CreatedAt,
	}
}

// setupAccountRequest is one entry of setupRequest.Accounts: the kind and
// name the treasurer gives one location at setup (#78). Shape only - whether
// kind names a real kind and name is non-blank after trimming are the
// schema's own CHECKs to refuse, not re-checked here (ADR-027).
type setupAccountRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// setupRequest is POST /api/setup's body: the fund's name (PRD §7.1, "name
// the fund") and every account the treasurer wants it to start with (#78) -
// at least one, SetUpFund's own job to refuse if empty. The main purpose is
// still not on the wire: "Kas Utama" is the domain's own fixed name
// (CONTEXT.md, ADR-014), never user input.
type setupRequest struct {
	Name     string                `json:"name"`
	Accounts []setupAccountRequest `json:"accounts"`
}

// setupResponse is pinned by #64 for fund and main_purpose_id; accounts
// became a list under #78 - the fixed two-field CashAccountID/BankAccountID
// shape assumed exactly the pair SetUpFund no longer creates.
type setupResponse struct {
	Fund          fundResponse      `json:"fund"`
	MainPurposeID int64             `json:"main_purpose_id"`
	Accounts      []accountResponse `json:"accounts"`
}

// setupFund is POST /api/setup. No fund_id anywhere: v1 is exactly one fund,
// and SetUpFund itself is what refuses a second one - this handler decodes
// and passes the name and accounts through, it does not pre-check anything
// the ledger already checks (ADR-027).
func (a *api) setupFund(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	accounts := make([]ledger.AccountInput, 0, len(req.Accounts))
	for _, acc := range req.Accounts {
		accounts = append(accounts, ledger.AccountInput{Kind: acc.Kind, Name: acc.Name})
	}

	result, err := a.ledger.SetUpFund(r.Context(), ledger.SetUpFundParams{
		FundName: req.Name,
		Accounts: accounts,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	accountsResp := make([]accountResponse, 0, len(result.Accounts))
	for _, acc := range result.Accounts {
		accountsResp = append(accountsResp, toAccountResponse(acc))
	}

	writeJSON(w, http.StatusCreated, setupResponse{
		Fund:          toFundResponse(result.Fund),
		MainPurposeID: result.MainPurposeID,
		Accounts:      accountsResp,
	})
}

// getFund is GET /api/fund: a direct-CRUD read (no derived invariant), so it
// calls a.queries itself rather than going through the ledger, the same
// split api's own doc comment describes. No fund_id in the route - v1 is
// exactly one fund, so the handler resolves it server-side via resolveFund:
// the first (and only) row ListFunds returns, or 404 meaning "run setup."
func (a *api) getFund(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toFundResponse(fund))
}

// resolveFund returns the single fund every fund-scoped route in this
// package operates against, or answers the request itself (404, "run
// setup") and reports false. v1 is exactly one fund and no route takes a
// fund_id, so "the fund" always means the first (and only) row ListFunds
// returns - the resolution getFund used alone until #65 gave it siblings
// (members, dues tiers, dues rates) that need the identical check, at
// which point it moved here to stay the one place that check is made.
func (a *api) resolveFund(w http.ResponseWriter, r *http.Request) (store.Fund, bool) {
	funds, err := a.queries.ListFunds(r.Context())
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return store.Fund{}, false
	}
	if len(funds) == 0 {
		writeAPIError(w, http.StatusNotFound, "not_found", "No fund has been set up yet.")
		return store.Fund{}, false
	}
	return funds[0], true
}
