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
