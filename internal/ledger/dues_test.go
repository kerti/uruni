package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// PostDuesPayments writes kind='dues', direction='in', with member_id and
// dues_period set - exactly the schema's CHECK - and moves the balance.
func TestPostDuesPaymentsWritesTheDuesShapeAndMovesTheBalance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	if len(posted) != 1 {
		t.Fatalf("PostDuesPayments() returned %d rows, want 1", len(posted))
	}
	row := posted[0]
	if row.ID == 0 {
		t.Fatal("PostDuesPayments() returned a zero id")
	}
	if row.Kind != "dues" {
		t.Errorf("Kind = %q, want %q", row.Kind, "dues")
	}
	if row.Direction != "in" {
		t.Errorf("Direction = %q, want %q", row.Direction, "in")
	}
	if row.MemberID == nil || *row.MemberID != f.memberID {
		t.Errorf("MemberID = %v, want %d", row.MemberID, f.memberID)
	}
	if row.DuesPeriod == nil || *row.DuesPeriod != "2026-08" {
		t.Errorf("DuesPeriod = %v, want %q", row.DuesPeriod, "2026-08")
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 25_000 {
		t.Errorf("FundBalance() = %d, want 25000", fundBal)
	}
}

// Several months paid at once is one call, one row per period - the
// schema's shape and the treasurer's real workflow - and every row stays
// visible afterwards.
func TestPostDuesPaymentsSeveralMonthsAtOnceIsOneRowPerPeriod(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	periods := []string{"2026-06", "2026-07", "2026-08"}
	periodAmounts := make([]PeriodAmount, 0, len(periods))
	for _, period := range periods {
		periodAmounts = append(periodAmounts, PeriodAmount{DuesPeriod: period, Amount: 25_000})
	}

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: periodAmounts,
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	if len(posted) != len(periods) {
		t.Fatalf("PostDuesPayments() returned %d rows, want %d - one per period", len(posted), len(periods))
	}

	rows, err := store.New(l.db).ListDuesPaymentsByMember(ctx, &f.memberID)
	if err != nil {
		t.Fatalf("ListDuesPaymentsByMember() = %v, want no error", err)
	}
	if len(rows) != len(periods) {
		t.Fatalf("ListDuesPaymentsByMember() returned %d rows, want %d - one per period", len(rows), len(periods))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.DuesPeriod == nil {
			t.Fatal("row.DuesPeriod = nil, want a period")
		}
		seen[*row.DuesPeriod] = true
	}
	for _, period := range periods {
		if !seen[period] {
			t.Errorf("period %q missing from ListDuesPaymentsByMember(), got %v", period, seen)
		}
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 75_000 {
		t.Errorf("FundBalance() = %d, want 75000 - three months at 25000 each", fundBal)
	}
}

// TestPostDuesPaymentsAFailureOnALaterPeriodLeavesZeroRows is the direct
// regression test for #96: a multi-period payment where a later period is
// malformed must post NOTHING, not the earlier periods that would have
// succeeded on their own. PostDuesPayments validates every period before
// writing any of them, and writes every row inside one withTx, so a
// mid-batch failure rolls back rows that were already inserted in this same
// transaction rather than leaving them standing.
func TestPostDuesPaymentsAFailureOnALaterPeriodLeavesZeroRows(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	before, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(before) != 0 {
		t.Fatalf("ledger holds %d rows before the call, want 0", len(before))
	}

	_, err = l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{
			{DuesPeriod: "2026-06", Amount: 25_000},
			{DuesPeriod: "2026-07", Amount: 25_000},
			{DuesPeriod: "not-a-period", Amount: 25_000}, // fails validation
		},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostDuesPayments() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	after, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(after) != 0 {
		t.Fatalf("ledger holds %d rows after a batch that failed on its third period, want 0 - "+
			"the first two periods must not have been left standing", len(after))
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 0 {
		t.Errorf("FundBalance() = %d, want 0 - nothing from the failed batch should have moved the balance", fundBal)
	}
}

func TestPostDuesPaymentsRejectsEmptyPeriods(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: nil,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostDuesPayments() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a rejected empty-periods post, want 0", len(rows))
	}
}

func TestPostDuesPaymentsRejectsNonPositiveAmountBeforeTheWrite(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 0}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostDuesPayments() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a rejected post, want 0", len(rows))
	}
}

func TestPostDuesPaymentsRejectsInvalidOccurredOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-02-30",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostDuesPayments() = %v, want an error wrapping ErrInvalidArgument", err)
	}
}

// A malformed dues_period is rejected with ErrInvalidArgument before the
// write ever reaches the schema's GLOB/date() CHECK.
func TestPostDuesPaymentsRejectsInvalidDuesPeriod(t *testing.T) {
	tests := []struct {
		name       string
		duesPeriod string
	}{
		{"invalid month", "2026-13"},
		{"unpadded month", "2026-1"},
		{"not a period", "not-a-month"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				MemberID: f.memberID, OccurredOn: "2026-08-12",
				Periods: []PeriodAmount{{DuesPeriod: tt.duesPeriod, Amount: 25_000}},
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PostDuesPayments() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
			}
			if len(rows) != 0 {
				t.Errorf("ledger holds %d rows after a rejected dues_period, want 0 - the CHECK should never have been reached", len(rows))
			}
		})
	}
}
