package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// lineFor is a small test helper: find the one reconciliation_line for
// accountID among a snapshot's lines, or fail the test - every assertion
// below is about one specific line, and a missing line is itself a failure.
func lineFor(t *testing.T, lines []store.ReconciliationLine, accountID int64) store.ReconciliationLine {
	t.Helper()
	for _, ln := range lines {
		if ln.AccountID == accountID {
			return ln
		}
	}
	t.Fatalf("no reconciliation_line for account %d among %+v", accountID, lines)
	return store.ReconciliationLine{}
}

// An empty ledger is TakeReconciliation's own code path (ADR-024): no
// transaction exists yet, so MaxTransactionIDByFund returns sql.ErrNoRows,
// through_transaction_id is stored NULL, and AccountBalanceThrough is never
// called - every counted account's recorded_amount is 0 by construction, not
// by querying an empty sum.
func TestTakeReconciliationEmptyLedgerStoresNullCutoffAndZeroRecorded(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.cashID, ActualAmount: 0, Resolution: "matched"},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	if rec.ThroughTransactionID != nil {
		t.Errorf("ThroughTransactionID = %v, want nil for an empty ledger", rec.ThroughTransactionID)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if lines[0].RecordedAmount != 0 {
		t.Errorf("RecordedAmount = %d, want 0", lines[0].RecordedAmount)
	}
	if lines[0].DifferenceAmount != 0 {
		t.Errorf("DifferenceAmount = %d, want 0", lines[0].DifferenceAmount)
	}
}

// The false-record regression this slice exists to prevent: a gap is found
// and resolved by an "adjusted" fix in the same call, and the stored
// difference_amount is the gap that was actually found - never 0. 0 would
// mean the cutoff was taken after the fix landed, which is exactly the bug
// ADR-024/027 describe: schema-legal, and a permanent lie that no gap was
// ever found.
func TestTakeReconciliationRegressionAdjustedStoresTheGapFoundNotZero(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 100_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}

	note := "tin was short"
	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID: f.cashID, ActualAmount: 80_000, Resolution: "adjusted",
			Fix: &Fix{
				PurposeID: f.mainID, Direction: "out", Amount: 20_000,
				OccurredOn: "2026-08-31", Note: &note,
			},
		}},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	line := lineFor(t, lines, f.cashID)

	if line.RecordedAmount != 100_000 {
		t.Errorf("RecordedAmount = %d, want 100000 (before the fix)", line.RecordedAmount)
	}
	if line.ActualAmount != 80_000 {
		t.Errorf("ActualAmount = %d, want 80000", line.ActualAmount)
	}
	// The regression assertion: the gap that was actually found, not 0.
	if line.DifferenceAmount != -20_000 {
		t.Fatalf("DifferenceAmount = %d, want -20000 (the gap actually found, not 0)", line.DifferenceAmount)
	}
	if line.AdjustmentTransactionID == nil {
		t.Fatal("AdjustmentTransactionID is nil, want the fix's transaction id")
	}

	fix, err := q.GetTransaction(ctx, *line.AdjustmentTransactionID)
	if err != nil {
		t.Fatalf("GetTransaction(fix) = %v, want no error", err)
	}
	if fix.Kind != "adjustment" || fix.Direction != "out" || fix.Amount != 20_000 {
		t.Errorf("fix = (kind=%q direction=%q amount=%d), want (adjustment, out, 20000)", fix.Kind, fix.Direction, fix.Amount)
	}
}

// The same regression, for "entry_added": a forgotten real entry posted in
// the same call still leaves the snapshot holding the gap that was found,
// not 0 - and unlike "adjusted", it names no transaction.
func TestTakeReconciliationRegressionEntryAddedStoresTheGapFoundNotZero(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 80_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}

	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID: f.cashID, ActualAmount: 100_000, Resolution: "entry_added",
			Fix: &Fix{
				PurposeID: f.mainID, Direction: "in", Amount: 20_000,
				OccurredOn: "2026-08-15",
			},
		}},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	line := lineFor(t, lines, f.cashID)

	if line.RecordedAmount != 80_000 {
		t.Errorf("RecordedAmount = %d, want 80000 (before the fix)", line.RecordedAmount)
	}
	if line.DifferenceAmount != 20_000 {
		t.Fatalf("DifferenceAmount = %d, want 20000 (the gap actually found, not 0)", line.DifferenceAmount)
	}
	if line.AdjustmentTransactionID != nil {
		t.Errorf("AdjustmentTransactionID = %v, want nil - entry_added names nothing, the entry is self-explanatory", line.AdjustmentTransactionID)
	}

	// The fix itself must still exist, correctly dated and kind='normal'.
	all, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	var found bool
	for _, tx := range all {
		if tx.Kind == "normal" && tx.Amount == 20_000 && tx.OccurredOn == "2026-08-15" {
			found = true
		}
	}
	if !found {
		t.Error("no kind='normal' 20000 entry dated 2026-08-15 found - the fix was not posted as a real entry")
	}
}

// All four resolutions, reachable together in one snapshot across four
// accounts: adjusted always names its fix, entry_added never does, matched
// and left_open name nothing at all.
func TestTakeReconciliationAllFourResolutionsInOneSnapshot(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	extra1 := createAccount(t, q, f.fundID, "cash", "Petty Cash")
	extra2 := createAccount(t, q, f.fundID, "bank", "Second Bank")

	for accountID, amount := range map[int64]money.Amount{
		f.cashID: 100_000, // matched
		f.bankID: 50_000,  // left_open
		extra1:   200_000, // adjusted
		extra2:   80_000,  // entry_added
	} {
		if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
			FundID: f.fundID, AccountID: accountID, PurposeID: f.mainID,
			Amount: amount, OccurredOn: "2026-08-01",
		}); err != nil {
			t.Fatalf("PostOpeningBalance(%d) = %v, want no error", accountID, err)
		}
	}

	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.cashID, ActualAmount: 100_000, Resolution: "matched"},
			{AccountID: f.bankID, ActualAmount: 40_000, Resolution: "left_open"},
			{
				AccountID: extra1, ActualAmount: 180_000, Resolution: "adjusted",
				Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 20_000, OccurredOn: "2026-08-15"},
			},
			{
				AccountID: extra2, ActualAmount: 100_000, Resolution: "entry_added",
				Fix: &Fix{PurposeID: f.mainID, Direction: "in", Amount: 20_000, OccurredOn: "2026-08-10"},
			},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4", len(lines))
	}

	matched := lineFor(t, lines, f.cashID)
	if matched.Resolution != "matched" || matched.DifferenceAmount != 0 || matched.AdjustmentTransactionID != nil {
		t.Errorf("matched line = %+v, want resolution=matched, difference=0, no adjustment id", matched)
	}

	leftOpen := lineFor(t, lines, f.bankID)
	if leftOpen.Resolution != "left_open" || leftOpen.DifferenceAmount != -10_000 || leftOpen.AdjustmentTransactionID != nil {
		t.Errorf("left_open line = %+v, want resolution=left_open, difference=-10000, no adjustment id", leftOpen)
	}

	adjusted := lineFor(t, lines, extra1)
	if adjusted.Resolution != "adjusted" || adjusted.DifferenceAmount != -20_000 {
		t.Errorf("adjusted line = %+v, want resolution=adjusted, difference=-20000", adjusted)
	}
	if adjusted.AdjustmentTransactionID == nil {
		t.Error("adjusted line names no transaction, want one")
	}

	entryAdded := lineFor(t, lines, extra2)
	if entryAdded.Resolution != "entry_added" || entryAdded.DifferenceAmount != 20_000 {
		t.Errorf("entry_added line = %+v, want resolution=entry_added, difference=20000", entryAdded)
	}
	if entryAdded.AdjustmentTransactionID != nil {
		t.Error("entry_added line names a transaction, want nil")
	}
}

// Reproduces internal/db's TestReconciliationRecordsWhatWasCountedAndHowItWasResolved
// at the domain layer: a proof the domain wraps the store's adjusted and
// left_open scenarios correctly, not a re-test of the store.
func TestTakeReconciliationReproducesM2AdjustedAndLeftOpenScenarios(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 500_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance(cash) = %v, want no error", err)
	}
	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.bankID, PurposeID: f.mainID,
		Amount: 2_000_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance(bank) = %v, want no error", err)
	}

	// The bank statement agrees to the rupiah; the tin is 20000 short and gets
	// an adjusting entry.
	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.bankID, ActualAmount: 2_000_000, Resolution: "matched"},
			{
				AccountID: f.cashID, ActualAmount: 480_000, Resolution: "adjusted",
				Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 20_000, OccurredOn: "2026-08-31"},
			},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	diff, err := q.ReconciliationDifferenceTotal(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ReconciliationDifferenceTotal() = %v, want no error", err)
	}
	if diff != -20_000 {
		t.Errorf("ReconciliationDifferenceTotal() = %d, want -20000", diff)
	}

	fundBalance, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBalance != 2_480_000 {
		t.Errorf("FundBalance() = %d, want 2480000", fundBalance)
	}

	// A second fund, where the treasurer decides to sleep on a difference.
	g := newFixtureWithSlug(t, l, "Other Fund", "zyxwvutsrqponmlkjihgfe")
	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: g.fundID, AccountID: g.cashID, PurposeID: g.mainID,
		Amount: 100_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance(g.cash) = %v, want no error", err)
	}
	if _, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: g.fundID,
		Counts: []AccountCount{
			{AccountID: g.cashID, ActualAmount: 95_000, Resolution: "left_open"},
		},
	}); err != nil {
		t.Fatalf("TakeReconciliation(g) = %v, want no error", err)
	}

	open, err := q.ListOpenReconciliationLinesByFund(ctx, g.fundID)
	if err != nil {
		t.Fatalf("ListOpenReconciliationLinesByFund() = %v, want no error", err)
	}
	if len(open) != 1 || open[0].DifferenceAmount != -5_000 {
		t.Errorf("open lines = %+v, want one line with difference -5000", open)
	}
}

// Reproduces internal/db's TestALeftOpenDifferenceIsRevisitedAsASecondSnapshot
// at the domain layer: a left_open difference is picked up by a second
// TakeReconciliation call, never by editing the first. The immutability
// triggers make an edit impossible even for this package's own code, so the
// only proof available is that both snapshots coexist with their own,
// independent numbers.
func TestTakeReconciliationLeftOpenIsRevisitedAsASecondSnapshotFirstUntouched(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 300_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-05",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	first, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.cashID, ActualAmount: 240_000, Resolution: "left_open"},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation(first) = %v, want no error", err)
	}
	firstLines, err := q.ListReconciliationLines(ctx, first.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines(first) = %v, want no error", err)
	}
	firstLine := lineFor(t, firstLines, f.cashID)
	if firstLine.RecordedAmount != 250_000 || firstLine.DifferenceAmount != -10_000 {
		t.Fatalf("first line = %+v, want recorded=250000 difference=-10000", firstLine)
	}

	// A week later the missing receipt turns up: a 10000 expense from the 3rd,
	// posted now as an adjustment - backdated in occurred_on, current in id.
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "out", Amount: 10_000, OccurredOn: "2026-08-03", IsAdjustment: true,
	}); err != nil {
		t.Fatalf("PostTransaction(late) = %v, want no error", err)
	}

	// The first snapshot's frozen line must not have moved.
	firstLinesAgain, err := q.ListReconciliationLines(ctx, first.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines(first, again) = %v, want no error", err)
	}
	if got := lineFor(t, firstLinesAgain, f.cashID); got.RecordedAmount != 250_000 {
		t.Errorf("first snapshot's recorded_amount = %d, want 250000 unchanged", got.RecordedAmount)
	}

	todayBalance, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if todayBalance != 240_000 {
		t.Errorf("today's cash balance = %d, want 240000 - the late entry counts now", todayBalance)
	}

	// Counted again; this time it agrees. The first snapshot stays untouched.
	second, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.cashID, ActualAmount: 240_000, Resolution: "matched"},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation(second) = %v, want no error", err)
	}

	stillOpen, err := q.ListOpenReconciliationLinesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListOpenReconciliationLinesByFund() = %v, want no error", err)
	}
	if len(stillOpen) != 1 || stillOpen[0].ReconciliationID != first.ID {
		t.Errorf("open lines = %+v, want only the first snapshot's - history is not rewritten", stillOpen)
	}

	latest, err := q.LatestReconciliation(ctx, f.fundID)
	if err != nil {
		t.Fatalf("LatestReconciliation() = %v, want no error", err)
	}
	if latest.ID != second.ID {
		t.Errorf("LatestReconciliation() = %d, want %d", latest.ID, second.ID)
	}

	// Both snapshots coexist with their own numbers.
	firstStillThere, err := q.GetReconciliation(ctx, store.GetReconciliationParams{ID: first.ID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetReconciliation(first) = %v, want no error", err)
	}
	if firstStillThere.ID != first.ID {
		t.Error("the first snapshot no longer exists")
	}
}

// The fix a reconciliation posts is a real "transaction" row: it moves the
// live balance immediately, in the same call, while being excluded from this
// snapshot's own recorded_amount because the cutoff was taken before it
// existed. That pair of facts together is the whole design.
func TestTakeReconciliationFixMovesLiveBalanceButNotThisSnapshotsRecordedAmount(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 100_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}

	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID: f.cashID, ActualAmount: 90_000, Resolution: "adjusted",
			Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 10_000, OccurredOn: "2026-08-12"},
		}},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	live, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if live != 90_000 {
		t.Errorf("live AccountBalance() = %d, want 90000 - the fix counts immediately", live)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	if got := lineFor(t, lines, f.cashID).RecordedAmount; got != 100_000 {
		t.Errorf("snapshot RecordedAmount = %d, want 100000 - the fix is excluded from its own snapshot", got)
	}
}

// The cutoff is id order, not date order: a fix backdated earlier than
// entries already posted still gets an id above the cutoff, so it stays
// outside this snapshot and inside the next one.
func TestTakeReconciliationBackdatedFixLandsInTheNextSnapshotNotThisOne(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 100_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "out", Amount: 20_000, OccurredOn: "2026-08-05",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	first, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID: f.cashID, ActualAmount: 70_000, Resolution: "entry_added",
			// Dated the 3rd - earlier than the 2026-08-05 entry already
			// posted - but this fix is created now, so its id is higher.
			Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 10_000, OccurredOn: "2026-08-03"},
		}},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation(first) = %v, want no error", err)
	}

	firstLines, err := q.ListReconciliationLines(ctx, first.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines(first) = %v, want no error", err)
	}
	firstLine := lineFor(t, firstLines, f.cashID)
	if firstLine.RecordedAmount != 80_000 {
		t.Errorf("first snapshot RecordedAmount = %d, want 80000 - the backdated fix is not counted despite its early date", firstLine.RecordedAmount)
	}
	if firstLine.DifferenceAmount != -10_000 {
		t.Errorf("first snapshot DifferenceAmount = %d, want -10000", firstLine.DifferenceAmount)
	}

	// A second snapshot, taken after the backdated fix, includes it: the
	// live balance is now 70000, and this cutoff is above the fix's id.
	second, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.cashID, ActualAmount: 70_000, Resolution: "matched"},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation(second) = %v, want no error", err)
	}
	secondLines, err := q.ListReconciliationLines(ctx, second.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines(second) = %v, want no error", err)
	}
	if got := lineFor(t, secondLines, f.cashID).RecordedAmount; got != 70_000 {
		t.Errorf("second snapshot RecordedAmount = %d, want 70000 - the backdated fix now counts", got)
	}
}

// TakeReconciliation refuses to insert a line naming another fund's account:
// the composite foreign key on reconciliation_line rejects it, and this is a
// domain bug, not a caller mistake, so it is wrapped generically rather than
// as ErrInvalidArgument (ADR-027).
func TestTakeReconciliationRejectsAnotherFundsAccount(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	other := newFixtureWithSlug(t, l, "Other Fund", "zyxwvutsrqponmlkjihgfe")
	ctx := context.Background()

	_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: other.cashID, ActualAmount: 0, Resolution: "left_open"},
		},
	})
	if err == nil {
		t.Fatal("counting another fund's account = nil error, want the composite FK to reject it")
	}
	if errors.Is(err, ErrInvalidArgument) {
		t.Errorf("error = %v, want it not to be ErrInvalidArgument - this is a domain bug, not a caller mistake", err)
	}
	if want := "FOREIGN KEY"; !strings.Contains(strings.ToUpper(err.Error()), want) {
		t.Errorf("error = %q, want it to mention %q", err, want)
	}
}

// Every ErrInvalidArgument path TakeReconciliation checks before writing
// anything.
func TestTakeReconciliationRejectsInvalidArguments(t *testing.T) {
	validFix := &Fix{PurposeID: 0, Direction: "out", Amount: 10_000, OccurredOn: "2026-08-12"}

	tests := []struct {
		name   string
		counts func(f fixture) []AccountCount
	}{
		{
			name: "empty counts",
			counts: func(_ fixture) []AccountCount {
				return nil
			},
		},
		{
			name: "unrecognised resolution",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{AccountID: f.cashID, ActualAmount: 0, Resolution: "sort-of-matched"}}
			},
		},
		{
			name: "matched with a non-zero difference",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{AccountID: f.cashID, ActualAmount: 5_000, Resolution: "matched"}}
			},
		},
		{
			name: "adjusted with no fix",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{AccountID: f.cashID, ActualAmount: 5_000, Resolution: "adjusted"}}
			},
		},
		{
			name: "entry_added with no fix",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{AccountID: f.cashID, ActualAmount: 5_000, Resolution: "entry_added"}}
			},
		},
		{
			name: "left_open naming a fix",
			counts: func(f fixture) []AccountCount {
				fix := *validFix
				fix.PurposeID = f.mainID
				return []AccountCount{{AccountID: f.cashID, ActualAmount: 5_000, Resolution: "left_open", Fix: &fix}}
			},
		},
		{
			name: "matched naming a fix",
			counts: func(f fixture) []AccountCount {
				fix := *validFix
				fix.PurposeID = f.mainID
				return []AccountCount{{AccountID: f.cashID, ActualAmount: 0, Resolution: "matched", Fix: &fix}}
			},
		},
		{
			name: "fix amount zero",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{
					AccountID: f.cashID, ActualAmount: 5_000, Resolution: "adjusted",
					Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 0, OccurredOn: "2026-08-12"},
				}}
			},
		},
		{
			name: "fix amount negative",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{
					AccountID: f.cashID, ActualAmount: 5_000, Resolution: "adjusted",
					Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: -1, OccurredOn: "2026-08-12"},
				}}
			},
		},
		{
			name: "fix with an unrecognised direction",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{
					AccountID: f.cashID, ActualAmount: 5_000, Resolution: "adjusted",
					Fix: &Fix{PurposeID: f.mainID, Direction: "sideways", Amount: 5_000, OccurredOn: "2026-08-12"},
				}}
			},
		},
		{
			name: "fix with a malformed date",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{
					AccountID: f.cashID, ActualAmount: 5_000, Resolution: "adjusted",
					Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 5_000, OccurredOn: "not-a-date"},
				}}
			},
		},
		{
			name: "fix with a calendar-invalid date",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{
					AccountID: f.cashID, ActualAmount: 5_000, Resolution: "adjusted",
					Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 5_000, OccurredOn: "2026-02-30"},
				}}
			},
		},
		{
			name: "negative actual_amount",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{{AccountID: f.cashID, ActualAmount: -1, Resolution: "left_open"}}
			},
		},
		{
			name: "the same account counted twice",
			counts: func(f fixture) []AccountCount {
				return []AccountCount{
					{AccountID: f.cashID, ActualAmount: 0, Resolution: "left_open"},
					{AccountID: f.cashID, ActualAmount: 100, Resolution: "left_open"},
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()
			q := store.New(l.db)

			_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
				FundID: f.fundID,
				Counts: tc.counts(f),
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("TakeReconciliation() = %v, want an error matching ErrInvalidArgument", err)
			}

			// Nothing was written: an invalid call leaves no trace, whether
			// it was rejected before withTx even opened, or rolled back
			// from partway through it.
			recs, err := q.ListReconciliationsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListReconciliationsByFund() = %v, want no error", err)
			}
			if len(recs) != 0 {
				t.Errorf("ListReconciliationsByFund() = %d rows, want 0 - a rejected call must write nothing", len(recs))
			}
		})
	}
}

// newFixtureWithSlug is newFixture for a second fund in the same test - the
// fixture's own fund carries a fixed slug, so any test needing two funds
// needs a second one with a different report_slug.
func newFixtureWithSlug(t *testing.T, l *Ledger, name, slug string) fixture {
	t.Helper()
	ctx := context.Background()
	q := store.New(l.db)
	f := fixture{}

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: name, Currency: "IDR", ReportSlug: slug, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	f.fundID = fund.ID
	f.cashID = createAccount(t, q, f.fundID, "cash", "Cash")
	f.bankID = createAccount(t, q, f.fundID, "bank", "Bank Account")
	f.mainID = createPurpose(t, q, f.fundID, "main", "Primary Cash")
	return f
}

// The two code paths the suite exercised only separately: an empty ledger and
// a fix in the same call. A fund's very first reconciliation can still find a
// gap - the treasurer counts money that was never recorded at all - and the
// NULL-cutoff branch skips AccountBalanceThrough entirely, so nothing else
// proves the fix is still excluded from a recorded_amount that was never
// queried.
func TestTakeReconciliationEmptyLedgerWithAFixStillRecordsZeroAndTheFullGap(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID:    f.cashID,
			ActualAmount: 75_000, // counted, though the ledger holds nothing
			Resolution:   "entry_added",
			Fix: &Fix{
				PurposeID: f.mainID, Direction: "in", Amount: 75_000,
				OccurredOn: "2026-08-12",
			},
		}},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	if rec.ThroughTransactionID != nil {
		t.Errorf("ThroughTransactionID = %v, want nil - the ledger was empty when the cutoff was taken", rec.ThroughTransactionID)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	line := lineFor(t, lines, f.cashID)
	if line.RecordedAmount != 0 {
		t.Errorf("RecordedAmount = %d, want 0", line.RecordedAmount)
	}
	if line.DifferenceAmount != 75_000 {
		t.Errorf("DifferenceAmount = %d, want 75000 - the whole counted sum was the gap", line.DifferenceAmount)
	}

	// The fix is live immediately even though the snapshot excluded it.
	bal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if bal != 75_000 {
		t.Errorf("AccountBalance() = %d, want 75000", bal)
	}
}

// difference_amount is computed from actual - recorded, never from the fix, so
// a fix that does not close the whole gap still records the gap honestly and
// leaves the remainder to be found by the next snapshot. Nothing asserted this
// invariant, and an implementation that derived the difference from the fix
// would pass every other test in this file.
func TestTakeReconciliationFixThatUndershootsStillRecordsTheWholeGap(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	// Counted 130,000 against a recorded 100,000: a 30,000 gap, squared by a
	// fix of only 20,000.
	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID: f.cashID, ActualAmount: 130_000, Resolution: "adjusted",
			Fix: &Fix{
				PurposeID: f.mainID, Direction: "in", Amount: 20_000,
				OccurredOn: "2026-08-12",
			},
		}},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	line := lineFor(t, lines, f.cashID)
	if line.DifferenceAmount != 30_000 {
		t.Errorf("DifferenceAmount = %d, want 30000 - the gap found, not the 20000 the fix covered", line.DifferenceAmount)
	}

	// The remainder is not lost: it is simply still there, and the next
	// snapshot compares against a ledger that now holds 120,000.
	bal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if bal != 120_000 {
		t.Errorf("AccountBalance() = %d, want 120000", bal)
	}
}

// A second fund's reconciliation is invisible to the first fund - the #105
// scoping test mirrored for GetReconciliationDetail, the same shape
// TestASecondFundsIncidentalIsInvisibleToTheFirstFund uses for
// GetIncidentalDetail. An id names a row; it does not prove the caller may
// see it. PRD section 6 allows a server to hold more than one fund, so this
// is the scoping test that keeps that honest - v1's single-fund rule is a
// setup constraint, not a reason to read unscoped.
func TestASecondFundsReconciliationIsInvisibleToTheFirstFund(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	q := store.New(l.db)
	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	otherCash := createAccount(t, q, other.ID, "cash", "Other Fund's Cash")

	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: other.ID,
		Counts: []AccountCount{
			{AccountID: otherCash, ActualAmount: 0, Resolution: "matched"},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation(fund 2) = %v, want no error", err)
	}

	if _, err := l.GetReconciliationDetail(ctx, f.fundID, rec.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetReconciliationDetail(fund 1, fund 2's snapshot) = %v, want an error wrapping sql.ErrNoRows", err)
	}

	// The same snapshot read through its own fund still resolves, so the
	// test above is scoping and not a broken lookup.
	detail, err := l.GetReconciliationDetail(ctx, other.ID, rec.ID)
	if err != nil {
		t.Errorf("GetReconciliationDetail(fund 2, fund 2's snapshot) = %v, want no error", err)
	}
	if detail.Reconciliation.ID != rec.ID || len(detail.Lines) != 1 {
		t.Errorf("GetReconciliationDetail(fund 2, fund 2's snapshot) = %+v, want the snapshot with its 1 line", detail)
	}
}
