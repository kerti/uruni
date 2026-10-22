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

// monthInOutResponse is the running month's In (net of the reversals of income)
// and Out (net of the reversals of spending), in integer rupiah. Either can be
// negative (ADR-042), but only after a cek kas counted the original.
type monthInOutResponse struct {
	In  int64 `json:"in"`
	Out int64 `json:"out"`
}

// getBalances is GET /api/balances: the one composed view-model route in M4,
// feeding the home screen's balance hero and per-location/per-purpose
// breakdown (PRD section 7.7) in one trip.
//
// Every figure comes from one ledger call, Balances, which reads them all in a
// single transaction: the hero total and its parts are one moment of the ledger,
// and the cost does not grow with the number of accounts and purposes (#532).
// The handler derives no number itself: fund_total is the ledger's own sum,
// never the account balances added up, though they agree (the test suite checks
// that). That is ADR-027's "thin mapping" for a read.
func (a *api) getBalances(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	b, err := a.ledger.Balances(r.Context(), fund.ID, a.now())
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	accountResp := make([]accountBalanceResponse, 0, len(b.Accounts))
	for _, row := range b.Accounts {
		accountResp = append(accountResp, accountBalanceResponse{
			ID: row.Account.ID, Kind: row.Account.Kind, Name: row.Account.Name, Balance: row.Balance.Int64(),
		})
	}
	purposeResp := make([]purposeBalanceResponse, 0, len(b.Purposes))
	for _, row := range b.Purposes {
		purposeResp = append(purposeResp, purposeBalanceResponse{
			ID: row.Purpose.ID, Kind: row.Purpose.Kind, Name: row.Purpose.Name, Balance: row.Balance.Int64(),
		})
	}

	writeJSON(w, http.StatusOK, balancesResponse{
		FundTotal:     b.FundTotal.Int64(),
		Accounts:      accountResp,
		Purposes:      purposeResp,
		OwedToMembers: b.OwedToMembers.Int64(),
		Month:         monthInOutResponse{In: b.MonthIn.Int64(), Out: b.MonthOut.Int64()},
	})
}
