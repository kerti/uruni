package db

import (
	"context"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// What the database refuses about a snapshot and an envelope, as opposed to
// what the application remembers to check. As in ledger_test.go, most of these
// fail by *not* getting an error.

func TestReconciliationLineDifferenceMustAgreeWithItsInputs(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 100, CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}

	// A stored derived figure that disagrees with its inputs is worse than no
	// figure at all, so the CHECK does the subtraction too.
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.accountID,
		RecordedAmount: 100000, ActualAmount: 95000, DifferenceAmount: -1000,
		Resolution: "left_open",
	}); err == nil {
		t.Error("difference_amount = -1000 against 95000 - 100000 = nil error, want the CHECK to reject it")
	}

	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.accountID,
		RecordedAmount: 100000, ActualAmount: 95000, DifferenceAmount: -5000,
		Resolution: "left_open",
	}); err != nil {
		t.Errorf("difference_amount = -5000 against 95000 - 100000 = %v, want no error", err)
	}
}

func TestAdjustedResolutionMustNameTheEntryThatSquaredTheLine(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 100, CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}

	line := func() store.CreateReconciliationLineParams {
		return store.CreateReconciliationLineParams{
			FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.accountID,
			RecordedAmount: 100000, ActualAmount: 95000, DifferenceAmount: -5000,
			Resolution: "adjusted",
		}
	}

	t.Run("adjusted with nothing to point at", func(t *testing.T) {
		if _, err := q.CreateReconciliationLine(ctx, line()); err == nil {
			t.Error("resolution='adjusted' with no adjustment_transaction_id = nil error, want the CHECK to reject it")
		}
	})

	t.Run("adjusted naming another fund's entry", func(t *testing.T) {
		other := newLedgerFixture(t, sqlDB, "Kas RW", "zyxwvutsrqponmlkjihgfe")
		stranger := other.post(t, sqlDB, "out", 5000)

		p := line()
		p.AdjustmentTransactionID = &stranger
		if _, err := q.CreateReconciliationLine(ctx, p); err == nil {
			t.Error("an adjustment belonging to another fund = nil error, want the composite FK to reject it")
		}
	})

	t.Run("adjusted naming this fund's entry", func(t *testing.T) {
		fix := f.post(t, sqlDB, "out", 5000)

		p := line()
		p.AdjustmentTransactionID = &fix
		if _, err := q.CreateReconciliationLine(ctx, p); err != nil {
			t.Errorf("resolution='adjusted' naming this fund's entry = %v, want no error", err)
		}
	})
}

func TestSnapshotsAreImmutable(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 100, CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}
	l, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.accountID,
		RecordedAmount: 100000, ActualAmount: 100000, DifferenceAmount: 0,
		Resolution: "matched",
	})
	if err != nil {
		t.Fatalf("CreateReconciliationLine = %v, want no error", err)
	}

	for _, tc := range []struct {
		name, stmt, want string
		arg              int64
	}{
		{"update a snapshot", `UPDATE reconciliation SET note = 'diubah' WHERE id = ?`, "reconciliation rows are immutable", rec.ID},
		{"delete a snapshot", `DELETE FROM reconciliation WHERE id = ?`, "reconciliation rows are immutable", rec.ID},
		{"update a line", `UPDATE reconciliation_line SET actual_amount = 1 WHERE id = ?`, "reconciliation_line rows are immutable", l.ID},
		{"delete a line", `DELETE FROM reconciliation_line WHERE id = ?`, "reconciliation_line rows are immutable", l.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sqlDB.Exec(tc.stmt, tc.arg)
			if err == nil {
				t.Fatalf("%s = nil error, want the trigger to abort it", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestSnapshotCannotBorrowAnotherFundsRow(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)
	other := newLedgerFixture(t, sqlDB, "Kas RW", "zyxwvutsrqponmlkjihgfe")

	t.Run("a cutoff in another fund's ledger", func(t *testing.T) {
		stranger := other.post(t, sqlDB, "in", 10000)
		if _, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
			FundID: f.fundID, PerformedAt: 100, ThroughTransactionID: &stranger, CreatedAt: 100,
		}); err == nil {
			t.Error("through_transaction_id from another fund = nil error, want the composite FK to reject it")
		}
	})

	t.Run("a line counting another fund's location", func(t *testing.T) {
		rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
			FundID: f.fundID, PerformedAt: 100, CreatedAt: 100,
		})
		if err != nil {
			t.Fatalf("CreateReconciliation = %v, want no error", err)
		}
		if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
			FundID: f.fundID, ReconciliationID: rec.ID, AccountID: other.accountID,
			RecordedAmount: 0, ActualAmount: 0, DifferenceAmount: 0, Resolution: "matched",
		}); err == nil {
			t.Error("a line naming another fund's account = nil error, want the composite FK to reject it")
		}
	})
}

func TestALocationIsCountedOncePerSnapshot(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 100, CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}
	line := store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.accountID,
		RecordedAmount: 100000, ActualAmount: 100000, DifferenceAmount: 0, Resolution: "matched",
	}
	if _, err := q.CreateReconciliationLine(ctx, line); err != nil {
		t.Fatalf("the first line for a location = %v, want no error", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, line); err == nil {
		t.Error("a second line for the same location = nil error, want the UNIQUE to reject it")
	}
}

func TestIncidentalIsOneEnvelopePerPurposeAndStaysEditable(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Kas RT", validSlug)
	purposeID := createPurpose(t, sqlDB, fundID, "incidental", "Kurban 2026")

	target := int64(3000000)
	env := store.CreateIncidentalParams{
		PurposeID: purposeID, Occasion: "Kurban 2026", TargetAmount: &target,
		OpenedOn: "2026-05-01", CreatedAt: 1,
	}
	if _, err := q.CreateIncidental(ctx, env); err != nil {
		t.Fatalf("CreateIncidental = %v, want no error", err)
	}
	if _, err := q.CreateIncidental(ctx, env); err == nil {
		t.Error("a second envelope on the same purpose = nil error, want the primary key to reject it")
	}

	// No immutability trigger here on purpose: opening a collection is a
	// decision that gets revised, and closing one moves no money.
	closed := "2026-06-10"
	got, err := q.CloseIncidental(ctx, store.CloseIncidentalParams{ClosedOn: &closed, PurposeID: purposeID})
	if err != nil {
		t.Fatalf("CloseIncidental = %v, want no error", err)
	}
	if got.ClosedOn == nil || *got.ClosedOn != closed {
		t.Errorf("closed_on = %v, want %q", got.ClosedOn, closed)
	}
}

func TestIncidentalRejectsImpossibleDatesAndTargets(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Kas RT", validSlug)

	base := func(name string) store.CreateIncidentalParams {
		return store.CreateIncidentalParams{
			PurposeID: createPurpose(t, sqlDB, fundID, "incidental", name),
			Occasion:  "Kurban 2026", OpenedOn: "2026-05-01", CreatedAt: 1,
		}
	}

	t.Run("an impossible opened_on", func(t *testing.T) {
		p := base("a")
		p.OpenedOn = "2026-13-45"
		if _, err := q.CreateIncidental(ctx, p); err == nil {
			t.Error("opened_on '2026-13-45' = nil error, want the CHECK to reject it")
		}
	})

	t.Run("an unparseable closed_on", func(t *testing.T) {
		bad := "not-a-date"
		p := base("b")
		p.ClosedOn = &bad
		if _, err := q.CreateIncidental(ctx, p); err == nil {
			t.Error("closed_on 'not-a-date' = nil error, want the CHECK to reject it")
		}
	})

	t.Run("a target of zero", func(t *testing.T) {
		zero := int64(0)
		p := base("c")
		p.TargetAmount = &zero
		if _, err := q.CreateIncidental(ctx, p); err == nil {
			t.Error("target_amount 0 = nil error, want the CHECK to reject it")
		}
	})

	t.Run("an envelope with no target", func(t *testing.T) {
		if _, err := q.CreateIncidental(ctx, base("d")); err != nil {
			t.Errorf("target_amount NULL = %v, want no error - a collection may be open-ended", err)
		}
	})
}
