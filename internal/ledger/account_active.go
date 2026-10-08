package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// refuseInactiveAccount returns ErrAccountInactive when accountID is one of
// this fund's locations and has been retired (inactive_on set), and nil
// otherwise. It reads inside the caller's transaction, so the check and the
// write that follows it see the same state (ADR-004's single connection).
//
// "Retired" is exactly PostPurposeMove's reading: inactive_on non-NULL,
// whatever date it holds and whatever the posting's own occurred_on is. The
// SPA's pickers filter the same way (inactive_on === null).
//
// An id that is not this fund's - unknown, or another fund's - returns nil
// here on purpose. It is not "retired", and the write that follows already
// refuses it through the composite foreign key, exactly as it did before this
// check existed (ADR-027: shape is the schema's to police). Do not turn that
// into a second, competing refusal.
func refuseInactiveAccount(ctx context.Context, q store.Querier, fundID, accountID int64) error {
	account, err := q.GetAccountForFund(ctx, store.GetAccountForFundParams{ID: accountID, FundID: fundID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("fetching account %d: %w", accountID, err)
	}
	if account.InactiveOn != nil {
		return ErrAccountInactive
	}
	return nil
}

// UpdateAccount corrects a location's labels (name, kind) and sets or clears
// its retirement date, in one transaction. Labels move no money (#134), so
// only retiring carries a rule: a location whose recorded balance is not
// zero is refused (#474). Retired, it would drop out of every Cek kas (PRD
// 7.8) and refuse every posting (#444) while FundBalance still sums it -
// money nothing proves and nothing can move. Above zero is
// ErrAccountHoldsMoney (move it out); below zero is ErrAccountBalanceNegative
// (a count has to find the gap - there is nothing to move). Reinstating (SetInactiveOn with a nil InactiveOn) is always
// allowed, and is the way out for a location retired before this rule.
//
// fundID scopes the balance; the caller has already resolved p.ID to this
// fund's account (resolveAccount), so an id from elsewhere never gets here.
func (l *Ledger) UpdateAccount(ctx context.Context, fundID int64, p store.UpdateAccountParams) (store.Account, error) {
	var updated store.Account
	err := l.withTx(ctx, func(q store.Querier) error {
		if p.SetInactiveOn == 1 && p.InactiveOn != nil {
			balance, err := q.AccountBalance(ctx, store.AccountBalanceParams{FundID: fundID, AccountID: p.ID})
			if err != nil {
				return fmt.Errorf("reading balance of account %d: %w", p.ID, err)
			}
			switch {
			case balance > 0:
				return ErrAccountHoldsMoney
			case balance < 0:
				return ErrAccountBalanceNegative
			}
		}
		var err error
		updated, err = q.UpdateAccount(ctx, p)
		return err
	})
	if err != nil {
		return store.Account{}, fmt.Errorf("updating account: %w", err)
	}
	return updated, nil
}
