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

// The guard's whole point (ADR-031, #214): a closed envelope refuses every
// posting, in both directions - a late bill deserves attribution to the
// occasion exactly as much as a late contribution does. Proven by asserting
// nothing was inserted, not only that an error came back, the same shape
// TestPostTransactionRejectsNonPositiveAmountBeforeTheWrite uses.
func TestPostTransactionRefusesAClosedIncidentalBothDirections(t *testing.T) {
	for _, direction := range []string{"in", "out"} {
		t.Run(direction, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")
			if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
				FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
			}); err != nil {
				t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
			}

			rowsBefore, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() before = %v, want no error", err)
			}

			_, err = l.PostTransaction(ctx, PostTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
				Direction: direction, Amount: 10_000, OccurredOn: "2026-08-15",
			})
			if !errors.Is(err, ErrIncidentalClosed) {
				t.Fatalf("PostTransaction(%s) on a closed incidental = %v, want an error wrapping ErrIncidentalClosed", direction, err)
			}

			rowsAfter, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() after = %v, want no error", err)
			}
			if len(rowsAfter) != len(rowsBefore) {
				t.Errorf("ledger holds %d rows after a refused post, want %d (unchanged)", len(rowsAfter), len(rowsBefore))
			}
		})
	}
}

// An open incidental, main and pass-through purposes are all unaffected by
// the guard: IncidentalClosedOnForPurpose returns zero rows for the latter
// two, and closed_on is NULL for the first, so PostTransaction proceeds
// exactly as it always has.
func TestPostTransactionUnaffectedByTheGuardOnOpenOrNonIncidentalPurposes(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	for name, purposeID := range map[string]int64{
		"open incidental": envelope.PurposeID,
		"main":            f.mainID,
		"pass-through":    f.passID,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := l.PostTransaction(ctx, PostTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: purposeID,
				Direction: "in", Amount: 5_000, OccurredOn: "2026-08-12",
			}); err != nil {
				t.Fatalf("PostTransaction() = %v, want no error", err)
			}
		})
	}
}

// TestPostTransactionAcceptsAnOptionalMemberOnAContribution (ADR-034,
// #211): a kind='normal', direction='in' row tagged to an envelope may name
// its member, and the posted row carries it straight through.
func TestPostTransactionAcceptsAnOptionalMemberOnAContribution(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &f.memberID,
	})
	if err != nil {
		t.Fatalf("PostTransaction(named contribution) = %v, want no error", err)
	}
	if posted.MemberID == nil || *posted.MemberID != f.memberID {
		t.Errorf("posted.MemberID = %v, want %d", posted.MemberID, f.memberID)
	}
	if posted.Kind != "normal" {
		t.Errorf("posted.Kind = %q, want %q - naming a member adds no new kind", posted.Kind, "normal")
	}
}

// TestPostTransactionRefusesAMemberOutsideAnIncidentalPurpose: a friendly,
// named error ahead of the BEFORE INSERT trigger's raw message (ADR-034) -
// f.mainID is 'main', not 'incidental'.
func TestPostTransactionRefusesAMemberOutsideAnIncidentalPurpose(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &f.memberID,
	})
	if !errors.Is(err, ErrContributionRequiresIncidentalPurpose) {
		t.Errorf("PostTransaction(member, main purpose) = %v, want an error wrapping ErrContributionRequiresIncidentalPurpose", err)
	}
}

// TestPostTransactionRefusesAMemberOnAnOutgoingOrAdjustingRow: naming a
// member is only ever valid on a contribution - a kind='normal',
// direction='in' row - refused before the write is even attempted, whatever
// the purpose (ADR-034).
func TestPostTransactionRefusesAMemberOnAnOutgoingOrAdjustingRow(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")

	t.Run("outgoing", func(t *testing.T) {
		_, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
			Direction: "out", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &f.memberID,
		})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("PostTransaction(member, direction=out) = %v, want an error wrapping ErrInvalidArgument", err)
		}
	})

	t.Run("adjustment", func(t *testing.T) {
		_, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
			Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &f.memberID,
			IsAdjustment: true,
		})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("PostTransaction(member, is_adjustment) = %v, want an error wrapping ErrInvalidArgument", err)
		}
	})
}

// TestPostTransactionRefusesANamedContributionToAClosedEnvelope: the
// closed-incidental guard (ADR-031) still applies once a row can carry a
// member - naming a giver does not exempt the posting from it.
func TestPostTransactionRefusesANamedContributionToAClosedEnvelope(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	_, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-15", MemberID: &f.memberID,
	})
	if !errors.Is(err, ErrIncidentalClosed) {
		t.Fatalf("PostTransaction(named contribution) on a closed envelope = %v, want an error wrapping ErrIncidentalClosed", err)
	}
}
