package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// Posting moves FundBalance and AccountBalance by exactly the amount, in both
// directions.
func TestPostTransactionMovesFundAndAccountBalanceByExactlyTheAmount(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		amount    money.Amount
		want      money.Amount
	}{
		{"in moves the balance up", "in", 50_000, 50_000},
		{"out moves the balance down", "out", 50_000, -50_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			posted, err := l.PostTransaction(ctx, PostTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				Direction: tt.direction, Amount: tt.amount, OccurredOn: "2026-08-12",
			})
			if err != nil {
				t.Fatalf("PostTransaction() = %v, want no error", err)
			}
			if posted.ID == 0 {
				t.Error("PostTransaction() returned a zero id")
			}
			if posted.Kind != "normal" {
				t.Errorf("Kind = %q, want %q", posted.Kind, "normal")
			}
			if posted.Direction != tt.direction || posted.Amount != tt.amount.Int64() {
				t.Errorf("posted = (%q, %d), want (%q, %d)", posted.Direction, posted.Amount, tt.direction, tt.amount.Int64())
			}

			fundBal, err := l.FundBalance(ctx, f.fundID)
			if err != nil {
				t.Fatalf("FundBalance() = %v, want no error", err)
			}
			if fundBal != tt.want {
				t.Errorf("FundBalance() = %d, want %d", fundBal, tt.want)
			}

			acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
			if err != nil {
				t.Fatalf("AccountBalance() = %v, want no error", err)
			}
			if acctBal != tt.want {
				t.Errorf("AccountBalance() = %d, want %d", acctBal, tt.want)
			}
		})
	}
}

// A correction is kind='adjustment', selected by the bool - never a string a
// caller could set to 'dues', 'reimbursement' or 'transfer'.
func TestPostTransactionPostsAnAdjustment(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 5_000, OccurredOn: "2026-08-12", IsAdjustment: true,
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}
	if posted.Kind != "adjustment" {
		t.Errorf("Kind = %q, want %q", posted.Kind, "adjustment")
	}
}

// amount <= 0 must be rejected before the write ever reaches the schema's
// CHECK - proven here by asserting nothing was inserted, not only that an
// error came back.
func TestPostTransactionRejectsNonPositiveAmountBeforeTheWrite(t *testing.T) {
	tests := []struct {
		name   string
		amount money.Amount
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.PostTransaction(ctx, PostTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				Direction: "in", Amount: tt.amount, OccurredOn: "2026-08-12",
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PostTransaction() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
			}
			if len(rows) != 0 {
				t.Errorf("ledger holds %d rows after a rejected post, want 0 - the CHECK should never have been reached", len(rows))
			}
		})
	}
}

// Both a malformed occurred_on and a calendar-invalid one are rejected the
// same way, before the schema's date() CHECK ever sees them.
func TestPostTransactionRejectsInvalidOccurredOn(t *testing.T) {
	tests := []struct {
		name       string
		occurredOn string
	}{
		{"malformed", "12 August 2026"},
		{"calendar-invalid", "2026-02-30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.PostTransaction(ctx, PostTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				Direction: "in", Amount: 10_000, OccurredOn: tt.occurredOn,
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PostTransaction() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
			}
			if len(rows) != 0 {
				t.Errorf("ledger holds %d rows after a rejected post, want 0", len(rows))
			}
		})
	}
}

// direction is as much a caller-typed shape as amount and occurred_on, so an
// unrecognized value gets the same named error rather than a raw CHECK
// failure.
func TestPostTransactionRejectsAnUnrecognizedDirection(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "sideways", Amount: 10_000, OccurredOn: "2026-08-12",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostTransaction() = %v, want an error wrapping ErrInvalidArgument", err)
	}
}

// Everything past argument shape - here, an account borrowed from another
// fund - is a domain bug, not a caller mistake, and is wrapped generically
// rather than folded into ErrInvalidArgument (ADR-027).
func TestPostTransactionWrapsASchemaViolationGenerically(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	other, err := store.New(l.db).CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	_, err = l.PostTransaction(ctx, PostTransactionParams{
		FundID: other.ID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-08-12",
	})
	if err == nil {
		t.Fatal("PostTransaction() across funds = nil error, want a foreign key violation")
	}
	if errors.Is(err, ErrInvalidArgument) {
		t.Errorf("PostTransaction() = %v, want a generically wrapped error, not ErrInvalidArgument", err)
	}
}
