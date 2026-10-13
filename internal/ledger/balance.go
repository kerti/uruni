package ledger

import (
	"context"
	"fmt"

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
