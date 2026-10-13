package http

import "net/http"

// accountBalanceResponse is one account's row within GET /api/balances: the
// identifying fields listAccounts returns (id, kind, name) plus the balance,
// so the home screen (PRD section 7.7) renders every location in one trip.
type accountBalanceResponse struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Balance int64  `json:"balance"`
}

// purposeBalanceResponse is one purpose's row within GET /api/balances,
// mirroring accountBalanceResponse: the fields listPurposes returns plus the
// balance.
type purposeBalanceResponse struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Balance int64  `json:"balance"`
}

// balancesResponse is GET /api/balances's body: the fund's pooled total, every
// account's share, and every purpose's running total (PRD section 7.7). A test
// asserts fund_total equals the sum of the account balances; that arithmetic
// lives in the test, never here (see getBalances).
type balancesResponse struct {
	FundTotal int64                    `json:"fund_total"`
	Accounts  []accountBalanceResponse `json:"accounts"`
	Purposes  []purposeBalanceResponse `json:"purposes"`

	// OwedToMembers is Ledger.OwedToMembers: unsettled, unwaived claims,
	// already inside fund_total and never subtracted from it (#406).
	OwedToMembers int64 `json:"owed_to_members"`

	// Month is the running month's money in and out (ADR-038), for Beranda's
	// "Bulan ini" card: Ledger.MonthInOut, the report's own figures.
	Month monthInOutResponse `json:"month"`
}

// monthInOutResponse is the running month's In (net of reversals, so it can
// be negative) and Out (a magnitude), in integer rupiah.
type monthInOutResponse struct {
	In  int64 `json:"in"`
	Out int64 `json:"out"`
}

// getBalances is GET /api/balances: the one composed view-model route in M4,
// feeding the home screen's balance hero and per-location/per-purpose
// breakdown (PRD section 7.7) in one trip.
//
// Every figure comes from a ledger call (FundBalance, AccountBalance,
// PurposeBalance) over the rows ListAccountsByFund and ListPurposesByFund
// name. The handler composes those reads and derives no number itself:
// fund_total comes from FundBalance, never from adding the account balances,
// though they agree (the test suite checks that). That is ADR-027's "thin
// mapping" for a read: composing calls is handler work, arithmetic on the
// results is not.
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

	owed, err := a.ledger.OwedToMembers(r.Context(), fund.ID)
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	monthIn, monthOut, err := a.ledger.MonthInOut(r.Context(), fund.ID, a.now())
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, balancesResponse{
		FundTotal:     fundTotal.Int64(),
		Accounts:      accountResp,
		Purposes:      purposeResp,
		OwedToMembers: owed.Int64(),
		Month:         monthInOutResponse{In: monthIn.Int64(), Out: monthOut.Int64()},
	})
}
