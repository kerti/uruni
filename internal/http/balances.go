package http

import "net/http"

// accountBalanceResponse is one account's row within GET /api/balances: the
// same identifying fields listAccounts already returns (id, kind, name), plus
// the balance this route exists to add - so the home screen (PRD section 7.7)
// can render every location without a second round trip to GET /api/accounts.
type accountBalanceResponse struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Balance int64  `json:"balance"`
}

// purposeBalanceResponse is one purpose's row within GET /api/balances,
// mirroring accountBalanceResponse exactly - the same identifying fields
// listPurposes already returns, plus the balance.
type purposeBalanceResponse struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Balance int64  `json:"balance"`
}

// balancesResponse is GET /api/balances's body: the fund's one pooled total,
// every account's share of it, and every purpose's running total (PRD
// section 7.7). fund_total is asserted by a test to equal the sum of the
// account balances, but that arithmetic lives in the test, never here - see
// getBalances's own comment.
type balancesResponse struct {
	FundTotal int64                    `json:"fund_total"`
	Accounts  []accountBalanceResponse `json:"accounts"`
	Purposes  []purposeBalanceResponse `json:"purposes"`
}

// getBalances is GET /api/balances: the one deliberately composed view-model
// route in M4, feeding the home screen's balance hero and per-location/
// per-purpose breakdown (PRD section 7.7) in a single round trip.
//
// Every figure returned here comes back from a ledger call -
// Ledger.FundBalance, Ledger.AccountBalance, Ledger.PurposeBalance - over the
// rows ListAccountsByFund and ListPurposesByFund name. This handler composes
// those reads; it does not sum, subtract, reconcile or otherwise re-derive a
// single number of its own. In particular fund_total comes from FundBalance
// directly, never from adding up the account balances gathered below, even
// though they must and do agree (a fact the test suite checks, not this
// handler). That is ADR-027's "thin mapping" applied to a read: composing
// calls is legitimate handler work; doing arithmetic on the results is not.
func (a *api) getBalances(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	fundTotal, err := a.ledger.FundBalance(r.Context(), fund.ID)
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	accounts, err := a.queries.ListAccountsByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}
	accountResp := make([]accountBalanceResponse, 0, len(accounts))
	for _, acc := range accounts {
		balance, err := a.ledger.AccountBalance(r.Context(), fund.ID, acc.ID)
		if err != nil {
			mapLedgerError(w, a.logger, err)
			return
		}
		accountResp = append(accountResp, accountBalanceResponse{
			ID: acc.ID, Kind: acc.Kind, Name: acc.Name, Balance: balance.Int64(),
		})
	}

	purposes, err := a.queries.ListPurposesByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}
	purposeResp := make([]purposeBalanceResponse, 0, len(purposes))
	for _, p := range purposes {
		balance, err := a.ledger.PurposeBalance(r.Context(), fund.ID, p.ID)
		if err != nil {
			mapLedgerError(w, a.logger, err)
			return
		}
		purposeResp = append(purposeResp, purposeBalanceResponse{
			ID: p.ID, Kind: p.Kind, Name: p.Name, Balance: balance.Int64(),
		})
	}

	writeJSON(w, http.StatusOK, balancesResponse{
		FundTotal: fundTotal.Int64(),
		Accounts:  accountResp,
		Purposes:  purposeResp,
	})
}
