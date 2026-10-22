package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// FundBalance is the fund's one pooled balance: every posted transaction,
// summed, direction as sign. Pass-through and incidental purposes sum in like
// main; there is no second "available" figure anywhere (PRD 7.6, ADR-024:
// "either both leave the headline or neither does"). A fund with no
// transactions returns Rp 0, not an error.
//
// A read: it uses l.q directly, as one SELECT is already consistent (ADR-027).
func (l *Ledger) FundBalance(ctx context.Context, fundID int64) (money.Amount, error) {
	v, err := l.q.FundBalance(ctx, fundID)
	if err != nil {
		return 0, fmt.Errorf("fund balance: %w", err)
	}
	return money.FromDB(v), nil
}

// AccountBalance is one physical location's share of the fund's balance: what
// should be in the cash box or bank account now, if the ledger is right.
func (l *Ledger) AccountBalance(ctx context.Context, fundID, accountID int64) (money.Amount, error) {
	v, err := l.q.AccountBalance(ctx, store.AccountBalanceParams{FundID: fundID, AccountID: accountID})
	if err != nil {
		return 0, fmt.Errorf("account balance: %w", err)
	}
	return money.FromDB(v), nil
}

// PurposeBalance is one purpose's running total (main, an incidental
// collection, a pass-through envelope), where AccountBalance and FundBalance
// stay pooled across purposes.
func (l *Ledger) PurposeBalance(ctx context.Context, fundID, purposeID int64) (money.Amount, error) {
	v, err := l.q.PurposeBalance(ctx, store.PurposeBalanceParams{FundID: fundID, PurposeID: purposeID})
	if err != nil {
		return 0, fmt.Errorf("purpose balance: %w", err)
	}
	return money.FromDB(v), nil
}

// OwedToMembers is what the fund still owes members who fronted an expense:
// every claim neither settled by a payout nor waived, summed (#406). It is not
// subtracted from FundBalance (the money is in the kas until the payout), so
// Beranda and the report show it beside the balance, never inside it. It
// equals the Talangan "Belum dibayar" list's sum by construction: the query
// applies ListOutstandingReimbursementsByFund's two conditions.
func (l *Ledger) OwedToMembers(ctx context.Context, fundID int64) (money.Amount, error) {
	v, err := l.q.OutstandingReimbursementTotal(ctx, fundID)
	if err != nil {
		return 0, fmt.Errorf("owed to members: %w", err)
	}
	return money.FromDB(v), nil
}

// AccountBalanceRow is one account with its AccountBalance.
type AccountBalanceRow struct {
	Account store.Account
	Balance money.Amount
}

// PurposeBalanceRow is one purpose with its PurposeBalance.
type PurposeBalanceRow struct {
	Purpose store.Purpose
	Balance money.Amount
}

// HomeBalances is everything GET /api/balances answers with, read at one
// moment: the pooled total, each account's share, each purpose's running total,
// what is owed to members and the running month's money in and out.
type HomeBalances struct {
	FundTotal     money.Amount
	Accounts      []AccountBalanceRow // ListAccountsByFund's order
	Purposes      []PurposeBalanceRow // ListPurposesByFund's order
	OwedToMembers money.Amount
	MonthIn       money.Amount
	MonthOut      money.Amount
}

// Balances reads the home screen's figures in one transaction, so the hero total
// and the parts under it come from the same ledger and cannot disagree for a
// refresh, and in a fixed number of reads however many accounts and purposes
// the fund has (#532): one grouped sum over the ledger for the accounts and one
// for the purposes, each merged onto its ordered list. An account or purpose
// with no entry reads Rp 0.
//
// Every figure is what FundBalance, AccountBalance, PurposeBalance,
// OwedToMembers and MonthInOut return for it. The total is its own sum, not the
// accounts added up, though the two agree (a test holds them to it). now is
// MonthInOut's clock.
func (l *Ledger) Balances(ctx context.Context, fundID int64, now time.Time) (HomeBalances, error) {
	var b HomeBalances
	err := l.withTx(ctx, func(q store.Querier) error {
		tl := &Ledger{db: l.db, q: q}

		var err error
		if b.FundTotal, err = tl.FundBalance(ctx, fundID); err != nil {
			return err
		}

		accounts, err := q.ListAccountsByFund(ctx, fundID)
		if err != nil {
			return fmt.Errorf("listing accounts: %w", err)
		}
		accountSums, err := q.AccountBalancesByFund(ctx, fundID)
		if err != nil {
			return fmt.Errorf("account balances: %w", err)
		}
		byAccount := make(map[int64]int64, len(accountSums))
		for _, row := range accountSums {
			byAccount[row.AccountID] = row.BalanceAmount
		}
		b.Accounts = make([]AccountBalanceRow, len(accounts))
		for i, a := range accounts {
			b.Accounts[i] = AccountBalanceRow{Account: a, Balance: money.FromDB(byAccount[a.ID])}
		}

		purposes, err := q.ListPurposesByFund(ctx, fundID)
		if err != nil {
			return fmt.Errorf("listing purposes: %w", err)
		}
		purposeSums, err := q.PurposeBalancesByFund(ctx, fundID)
		if err != nil {
			return fmt.Errorf("purpose balances: %w", err)
		}
		byPurpose := make(map[int64]int64, len(purposeSums))
		for _, row := range purposeSums {
			byPurpose[row.PurposeID] = row.BalanceAmount
		}
		b.Purposes = make([]PurposeBalanceRow, len(purposes))
		for i, p := range purposes {
			b.Purposes[i] = PurposeBalanceRow{Purpose: p, Balance: money.FromDB(byPurpose[p.ID])}
		}

		if b.OwedToMembers, err = tl.OwedToMembers(ctx, fundID); err != nil {
			return err
		}
		b.MonthIn, b.MonthOut, err = tl.monthInOut(ctx, fundID, now)
		return err
	})
	return b, err
}
