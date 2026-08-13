package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// PostDuesPayment writes kind='dues', direction='in', with member_id and
// dues_period set - exactly the schema's CHECK - and moves the balance.
func TestPostDuesPaymentWritesTheDuesShapeAndMovesTheBalance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostDuesPayment(ctx, PostDuesPaymentParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, DuesPeriod: "2026-08",
		Amount: 25_000, OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("PostDuesPayment() = %v, want no error", err)
	}
	if posted.ID == 0 {
		t.Fatal("PostDuesPayment() returned a zero id")
	}
	if posted.Kind != "dues" {
		t.Errorf("Kind = %q, want %q", posted.Kind, "dues")
	}
	if posted.Direction != "in" {
		t.Errorf("Direction = %q, want %q", posted.Direction, "in")
	}
	if posted.MemberID == nil || *posted.MemberID != f.memberID {
		t.Errorf("MemberID = %v, want %d", posted.MemberID, f.memberID)
	}
	if posted.DuesPeriod == nil || *posted.DuesPeriod != "2026-08" {
		t.Errorf("DuesPeriod = %v, want %q", posted.DuesPeriod, "2026-08")
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 25_000 {
		t.Errorf("FundBalance() = %d, want 25000", fundBal)
	}
}

// Several months paid at once is several calls, one per period - the
// schema's shape and the treasurer's real workflow - and every row stays
// visible afterwards.
func TestPostDuesPaymentSeveralMonthsAtOnceIsOneRowPerPeriod(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	periods := []string{"2026-06", "2026-07", "2026-08"}
	for _, period := range periods {
		if _, err := l.PostDuesPayment(ctx, PostDuesPaymentParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			MemberID: f.memberID, DuesPeriod: period,
			Amount: 25_000, OccurredOn: "2026-08-12",
		}); err != nil {
			t.Fatalf("PostDuesPayment(%q) = %v, want no error", period, err)
		}
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

func TestPostDuesPaymentRejectsNonPositiveAmountBeforeTheWrite(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostDuesPayment(ctx, PostDuesPaymentParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, DuesPeriod: "2026-08",
		Amount: 0, OccurredOn: "2026-08-12",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostDuesPayment() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a rejected post, want 0", len(rows))
	}
}

func TestPostDuesPaymentRejectsInvalidOccurredOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostDuesPayment(ctx, PostDuesPaymentParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, DuesPeriod: "2026-08",
		Amount: 25_000, OccurredOn: "2026-02-30",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostDuesPayment() = %v, want an error wrapping ErrInvalidArgument", err)
	}
}

// A malformed dues_period is rejected with ErrInvalidArgument before the
// write ever reaches the schema's GLOB/date() CHECK.
func TestPostDuesPaymentRejectsInvalidDuesPeriod(t *testing.T) {
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

			_, err := l.PostDuesPayment(ctx, PostDuesPaymentParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				MemberID: f.memberID, DuesPeriod: tt.duesPeriod,
				Amount: 25_000, OccurredOn: "2026-08-12",
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PostDuesPayment() = %v, want an error wrapping ErrInvalidArgument", err)
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
