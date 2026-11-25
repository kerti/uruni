package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The two single-path reads below have no production caller: the roster and the
// home screen read through the batched ArrearsMonthsForMembers and Balances.
// They stay as test helpers because they are the independent oracle the #532
// parity tests (batch_reads_test.go) compare the batched reads against, so they
// must never route through the batch.

// ArrearsMonthsForMember is the one-member form of ArrearsMonthsForMembers:
// periods strictly before currentPeriod that the member still owes something
// for, counted over OutstandingDuesForMember, a part-paid period as one month
// like an unpaid one (ADR-032).
func (l *Ledger) ArrearsMonthsForMember(ctx context.Context, fundID, memberID int64, currentPeriod string) (int, error) {
	if err := checkPeriodShape("current period", currentPeriod); err != nil {
		return 0, err
	}

	t, err := time.Parse(duesPeriodLayout, currentPeriod)
	if err != nil {
		return 0, fmt.Errorf("parsing current period %q: %w", currentPeriod, err)
	}
	through := t.AddDate(0, -1, 0).Format(duesPeriodLayout)

	outstanding, err := l.OutstandingDuesForMember(ctx, fundID, memberID, through) // through is non-empty: the clock is never read
	if err != nil {
		return 0, err
	}
	return len(outstanding), nil
}

// MonthInOut is monthInOut in a transaction of its own, read at the ledger
// clock: the running month's In and Out as Balances carries them.
func (l *Ledger) MonthInOut(ctx context.Context, fundID int64) (in, out money.Amount, err error) {
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		in, out, err = l.onQuerier(q).monthInOut(ctx, fundID, l.now())
		return err
	})
	return in, out, err
}
