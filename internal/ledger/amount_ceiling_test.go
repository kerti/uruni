package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The ceiling (#499): every write taking an amount accepts exactly
// money.MaxAmount and refuses one rupiah more with ErrAmountTooLarge, writing
// nothing. Both sides of the boundary are pinned on every path, so a path that
// forgets the check, or is off by one, fails here by name.

// aroundTheCeiling runs write with the schema's amount CHECKs switched off on
// one connection, to plant the row a database from before the ceiling can still
// hold, so the code-side guards behind the schema stay covered (a new database
// refuses such a row outright, see db/amount_ceiling_test.go). The pragma is
// reset before the connection goes back to the pool.
func aroundTheCeiling(t *testing.T, l *Ledger, write func(q *store.Queries)) {
	t.Helper()
	ctx := context.Background()
	conn, err := l.db.Conn(ctx)
	if err != nil {
		t.Fatalf("taking a connection = %v, want no error", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatalf("PRAGMA ignore_check_constraints = ON = %v, want no error", err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "PRAGMA ignore_check_constraints = OFF"); err != nil {
			t.Errorf("PRAGMA ignore_check_constraints = OFF = %v, want no error", err)
		}
	}()
	write(store.New(conn))
}

const (
	atMax    = money.MaxAmount
	overMax  = money.MaxAmount + 1
	date     = "2026-08-12"
	period   = "2026-08"
	maxLabel = "at the ceiling"
	overLbl  = "one rupiah over the ceiling"
)

// ceilingPath is one write path, called with the amount under test. seed runs
// first when the path needs a balance to draw on.
type ceilingPath struct {
	name string
	seed func(t *testing.T, l *Ledger, f fixture)
	call func(t *testing.T, l *Ledger, f fixture, amount money.Amount) error
}

func seedMainBalance(t *testing.T, l *Ledger, f fixture) {
	t.Helper()
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, atMax, "2026-08-01")
}

func ceilingPaths() []ceilingPath {
	ctx := context.Background()
	return []ceilingPath{
		{
			name: "PostTransaction in",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostTransaction(ctx, PostTransactionParams{
					FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
					Direction: "in", Amount: a, OccurredOn: date,
				})
				return err
			},
		},
		{
			name: "PostTransaction out",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostTransaction(ctx, PostTransactionParams{
					FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
					Direction: "out", Amount: a, OccurredOn: date,
				})
				return err
			},
		},
		{
			name: "PostTransaction adjustment",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostTransaction(ctx, PostTransactionParams{
					FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
					Direction: "in", Amount: a, OccurredOn: date, IsAdjustment: true,
				})
				return err
			},
		},
		{
			name: "PostTransferBetweenAccounts",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
					FundID: f.fundID, PurposeID: f.mainID,
					FromAccountID: f.cashID, ToAccountID: f.bankID,
					Amount: a, OccurredOn: date,
				})
				return err
			},
		},
		{
			name: "PostPurposeMove",
			seed: seedMainBalance,
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostPurposeMove(ctx, PostPurposeMoveParams{
					FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: f.incidenID,
					AccountID: f.cashID, Amount: a, OccurredOn: date,
				})
				return err
			},
		},
		{
			name: "CreateReimbursement",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.CreateReimbursement(ctx, CreateReimbursementParams{
					FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID,
					Amount: a, IncurredOn: date,
				})
				return err
			},
		},
		{
			name: "UpdateReimbursement",
			call: func(t *testing.T, l *Ledger, f fixture, a money.Amount) error {
				claim := createReimbursement(t, store.New(l.db), f, 1_000, date, nil)
				_, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
					FundID: f.fundID, ReimbursementID: claim.ID, Amount: &a,
				})
				return err
			},
		},
		{
			// The payout copies the claim's stored amount. A claim past the ceiling
			// (written before it existed; a new schema refuses it) must not become a
			// payout above it.
			name: "SettleReimbursement of a stored claim",
			call: func(t *testing.T, l *Ledger, f fixture, a money.Amount) error {
				var claim store.Reimbursement
				aroundTheCeiling(t, l, func(q *store.Queries) { claim = createReimbursement(t, q, f, a.Int64(), date, nil) })
				_, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
					FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: date,
				})
				return err
			},
		},
		{
			name: "PostDuesPayments",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
					FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: date,
					Periods: []PeriodAmount{{DuesPeriod: period, Amount: a}},
				})
				return err
			},
		},
		{
			name: "PostDuesPayments second period",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
					FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: date,
					Periods: []PeriodAmount{{DuesPeriod: "2026-07", Amount: 1_000}, {DuesPeriod: period, Amount: a}},
				})
				return err
			},
		},
		{
			name: "OpenIncidental target",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.OpenIncidental(ctx, OpenIncidentalParams{
					FundID: f.fundID, Occasion: "Target", OpenedOn: date, TargetAmount: &a,
				})
				return err
			},
		},
		{
			name: "OpenIncidental minimum per member",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.OpenIncidental(ctx, OpenIncidentalParams{
					FundID: f.fundID, Occasion: "Minimum", OpenedOn: date, MinimumPerMember: &a,
				})
				return err
			},
		},
		{
			name: "SetIncidentalParticipation target",
			call: func(t *testing.T, l *Ledger, f fixture, a money.Amount) error {
				env := openTestIncidental(t, l, f.fundID, "Envelope", date)
				_, err := l.SetIncidentalParticipation(ctx, SetIncidentalParticipationParams{
					FundID: f.fundID, PurposeID: env.PurposeID, TargetAmount: &a,
				})
				return err
			},
		},
		{
			name: "SetIncidentalParticipation minimum per member",
			call: func(t *testing.T, l *Ledger, f fixture, a money.Amount) error {
				env := openTestIncidental(t, l, f.fundID, "Envelope", date)
				_, err := l.SetIncidentalParticipation(ctx, SetIncidentalParticipationParams{
					FundID: f.fundID, PurposeID: env.PurposeID, MinimumPerMember: &a,
				})
				return err
			},
		},
		{
			name: "CreateAccount opening balance",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.CreateAccount(ctx, CreateAccountParams{
					FundID: f.fundID, Kind: "bank", Name: "Second bank",
					OpeningBalance: &OpeningBalance{Amount: a, OccurredOn: date},
				})
				return err
			},
		},
		{
			name: "TakeReconciliation actual_amount",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
					FundID: f.fundID,
					Counts: []AccountCount{{AccountID: f.cashID, ActualAmount: a, Resolution: "left_open"}, f.bankZero()},
				})
				return err
			},
		},
		{
			name: "TakeReconciliation adjusted fix",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
					FundID: f.fundID,
					Counts: []AccountCount{{
						AccountID: f.cashID, ActualAmount: atMax, Resolution: "adjusted",
						Fix: &Fix{PurposeID: f.mainID, Direction: "in", Amount: a, OccurredOn: date},
					}, f.bankZero()},
				})
				return err
			},
		},
		{
			name: "TakeReconciliation entry_added fix",
			call: func(_ *testing.T, l *Ledger, f fixture, a money.Amount) error {
				_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
					FundID: f.fundID,
					Counts: []AccountCount{{
						AccountID: f.cashID, ActualAmount: atMax, Resolution: "entry_added",
						Fix: &Fix{PurposeID: f.mainID, Direction: "in", Amount: a, OccurredOn: date},
					}, f.bankZero()},
				})
				return err
			},
		},
	}
}

func TestEveryAmountPathAcceptsTheCeilingAndRefusesOneMore(t *testing.T) {
	t.Parallel()
	for _, p := range ceilingPaths() {
		t.Run(p.name+"/"+maxLabel, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			if p.seed != nil {
				p.seed(t, l, f)
			}
			if err := p.call(t, l, f, atMax); err != nil {
				t.Fatalf("%s at %d = %v, want no error", p.name, atMax.Int64(), err)
			}
		})
		t.Run(p.name+"/"+overLbl, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			if p.seed != nil {
				p.seed(t, l, f)
			}
			q := store.New(l.db)
			before := countRows(t, q, f.fundID)

			err := p.call(t, l, f, overMax)
			if !errors.Is(err, ErrAmountTooLarge) {
				t.Fatalf("%s at %d = %v, want an error wrapping ErrAmountTooLarge", p.name, overMax.Int64(), err)
			}
			if errors.Is(err, ErrInvalidArgument) {
				t.Errorf("%s: ErrAmountTooLarge must not also be ErrInvalidArgument, the wire maps them to different codes", p.name)
			}
			if after := countRows(t, q, f.fundID); after != before {
				t.Errorf("%s left rows behind a refused amount: before %+v, after %+v", p.name, before, after)
			}
		})
	}
}

type rowCounts struct{ transactions, transfers int }

func countRows(t *testing.T, q *store.Queries, fundID int64) rowCounts {
	t.Helper()
	ctx := context.Background()
	txs, err := q.ListTransactionsByFund(ctx, fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	trs, err := q.ListTransfersByFund(ctx, fundID)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	return rowCounts{transactions: len(txs), transfers: len(trs)}
}

// Refusing the ceiling must not reorder the sign checks: zero and negative are
// still ErrInvalidArgument, never ErrAmountTooLarge.
func TestCeilingDoesNotChangeTheSignChecks(t *testing.T) {
	t.Parallel()
	for _, amount := range []money.Amount{0, -1, -overMax} {
		l := newTestLedger(t)
		f := newFixture(t, l)
		_, err := l.PostTransaction(context.Background(), PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			Direction: "in", Amount: amount, OccurredOn: date,
		})
		if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrAmountTooLarge) {
			t.Errorf("PostTransaction(%d) = %v, want ErrInvalidArgument and not ErrAmountTooLarge", amount.Int64(), err)
		}
	}

	l := newTestLedger(t)
	f := newFixture(t, l)
	_, err := l.TakeReconciliation(context.Background(), TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{AccountID: f.cashID, ActualAmount: -1, Resolution: "left_open"}, f.bankZero()},
	})
	if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrAmountTooLarge) {
		t.Errorf("TakeReconciliation(actual -1) = %v, want ErrInvalidArgument and not ErrAmountTooLarge", err)
	}
}

func TestCheckAmountCeilingNamesTheFieldNotTheValue(t *testing.T) {
	t.Parallel()
	if err := CheckAmountCeiling("amount", atMax); err != nil {
		t.Fatalf("CheckAmountCeiling(max) = %v, want nil", err)
	}
	err := CheckAmountCeiling("target_amount", overMax)
	if !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("CheckAmountCeiling(max+1) = %v, want ErrAmountTooLarge", err)
	}
	if got := err.Error(); got != "ledger: amount is above the ceiling: target_amount" {
		t.Errorf("message = %q, want the field named and no figure in it", got)
	}
}

// Two entries at the ceiling are what it exists for: their sum must be a
// number SQLite can add, so a balance stays readable.
func TestTwoEntriesAtTheCeilingStillSum(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			Direction: "in", Amount: atMax, OccurredOn: date,
		}); err != nil {
			t.Fatalf("PostTransaction() #%d = %v, want no error", i, err)
		}
	}
	got, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if want := 2 * atMax; got != want {
		t.Errorf("FundBalance() = %d, want %d", got, want)
	}
}

// SetUpFund is the other door for an opening balance and runs on an empty
// database, so it sits outside the table above (which starts from a fund).
func TestSetUpFundOpeningBalanceCeiling(t *testing.T) {
	t.Parallel()
	setUp := func(l *Ledger, a money.Amount) error {
		_, err := l.SetUpFund(context.Background(), SetUpFundParams{
			FundName: "Fund",
			Accounts: []AccountInput{{Kind: "cash", Name: "Tunai", OpeningBalance: &OpeningBalance{Amount: a, OccurredOn: date}}},
		})
		return err
	}

	if err := setUp(newTestLedger(t), atMax); err != nil {
		t.Fatalf("SetUpFund() at the ceiling = %v, want no error", err)
	}

	l := newTestLedger(t)
	if err := setUp(l, overMax); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("SetUpFund() one over the ceiling = %v, want ErrAmountTooLarge", err)
	}
	// Nothing was created, so the fund can still be set up properly.
	if err := setUp(l, atMax); err != nil {
		t.Errorf("SetUpFund() after a refused one = %v, want no error: the refusal left a fund behind", err)
	}
}
