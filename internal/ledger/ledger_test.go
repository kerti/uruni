package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// The harness is only worth anything if it reaches the real schema, so these
// tests prove the plumbing rather than any domain behaviour: an entry survives
// a round trip, foreign keys are actually enforced, and withTx commits and
// rolls back when it should.

func TestHarnessRoundTripsAnEntry(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := store.New(l.db).CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-12", Kind: "normal",
		CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction() = %v, want no error", err)
	}

	got, err := store.New(l.db).GetTransaction(ctx, posted.ID)
	if err != nil {
		t.Fatalf("GetTransaction(%d) = %v, want no error", posted.ID, err)
	}
	if got.Amount != 50_000 || got.Direction != "in" {
		t.Errorf("read back (%d, %q), want (50000, \"in\")", got.Amount, got.Direction)
	}
}

// The self-check that matters: foreign keys are off by default in SQLite, per
// connection, so a harness that forgot them would let every later test pass
// against a database enforcing nothing. A composite FK is the sharper probe —
// it fails only if the pragma is on *and* ADR-024's (fund_id, id) references
// are doing their job.
func TestHarnessEnforcesCompositeForeignKeys(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	other, err := store.New(l.db).CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	// The account belongs to the first fund; the transaction claims the second.
	// Only the composite reference can catch this — a single-column
	// REFERENCES account(id) would find the row and be satisfied.
	_, err = store.New(l.db).CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: other.ID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-12", Kind: "normal",
		CreatedAt: 1,
	})
	if err == nil {
		t.Fatal("posting into another fund's account = nil error, want a foreign key violation")
	}
	if want := "FOREIGN KEY"; !strings.Contains(strings.ToUpper(err.Error()), want) {
		t.Errorf("error = %q, want it to mention %q", err, want)
	}
}

func TestWithTxCommits(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	var id int64
	if err := l.withTx(ctx, func(q store.Querier) error {
		posted, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			Direction: "in", Amount: 120_000, OccurredOn: "2026-08-12", Kind: "normal",
			CreatedAt: 1,
		})
		id = posted.ID
		return err
	}); err != nil {
		t.Fatalf("withTx() = %v, want no error", err)
	}

	if _, err := store.New(l.db).GetTransaction(ctx, id); err != nil {
		t.Errorf("GetTransaction(%d) after commit = %v, want the row", id, err)
	}
}

func TestWithTxRollsBackAndReturnsTheError(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	sentinel := errors.New("the caller changed its mind")

	// Two writes, then a failure: the second row is what proves the rollback
	// covers the whole function rather than only its last statement.
	err := l.withTx(ctx, func(q store.Querier) error {
		for _, amount := range []int64{50_000, 70_000} {
			if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				Direction: "in", Amount: amount, OccurredOn: "2026-08-12", Kind: "normal",
				CreatedAt: 1,
			}); err != nil {
				return err
			}
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("withTx() = %v, want the function's own error unwrapped", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a rolled-back transaction, want 0", len(rows))
	}
}

// The failure that happens before fn is ever called: withTx has to report it
// as its own, not hand back a nil error and a transaction nobody opened.
func TestWithTxReportsAFailureToBegin(t *testing.T) {
	l := newTestLedger(t)
	if err := l.db.Close(); err != nil {
		t.Fatalf("Close() = %v, want no error", err)
	}

	called := false
	err := l.withTx(context.Background(), func(store.Querier) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("withTx() on a closed database = nil error, want a failure to begin")
	}
	if called {
		t.Error("withTx() called fn despite failing to begin a transaction")
	}
}

// A rollback must also survive the database refusing a write mid-function -
// the immutability triggers are the realistic way that happens, and the
// rollback has to leave the earlier insert undone rather than half-applied.
func TestWithTxRollsBackWhenTheSchemaRefusesAWrite(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	err := l.withTx(ctx, func(q store.Querier) error {
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			Direction: "in", Amount: 50_000, OccurredOn: "2026-08-12", Kind: "normal",
			CreatedAt: 1,
		}); err != nil {
			return err
		}
		// amount > 0 is a CHECK, so the schema refuses this one.
		_, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			Direction: "in", Amount: 0, OccurredOn: "2026-08-12", Kind: "normal",
			CreatedAt: 1,
		})
		return err
	})
	if err == nil {
		t.Fatal("withTx() = nil error, want the CHECK violation")
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a refused write, want 0", len(rows))
	}
}
