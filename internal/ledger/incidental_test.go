package ledger

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// openTestIncidental is the test helper for a fresh envelope: the domain
// call itself, since OpenIncidental is exactly what #42 adds and every
// CloseIncidentalAndRoll test needs a real incidental row - not a purpose
// alone - to close.
func openTestIncidental(t *testing.T, l *Ledger, fundID int64, occasion, openedOn string) store.Incidental {
	t.Helper()
	created, err := l.OpenIncidental(context.Background(), OpenIncidentalParams{
		FundID: fundID, Occasion: occasion, OpenedOn: openedOn,
	})
	if err != nil {
		t.Fatalf("OpenIncidental(%q) = %v, want no error", occasion, err)
	}
	return created
}

// OpenIncidental writes one purpose row and the incidental row 1:1 with it,
// and the purpose it creates carries kind='incidental'.
func TestOpenIncidentalCreatesBothRows(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	target := money.Amount(500_000)
	created, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Jane's wedding", TargetAmount: &target, OpenedOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}
	if created.PurposeID == 0 {
		t.Fatal("OpenIncidental() returned a zero purpose id")
	}
	if created.Occasion != "Jane's wedding" {
		t.Errorf("Occasion = %q, want %q", created.Occasion, "Jane's wedding")
	}
	if created.TargetAmount == nil || *created.TargetAmount != 500_000 {
		t.Errorf("TargetAmount = %v, want 500000", created.TargetAmount)
	}
	if created.OpenedOn != "2026-08-12" {
		t.Errorf("OpenedOn = %q, want %q", created.OpenedOn, "2026-08-12")
	}
	if created.ClosedOn != nil {
		t.Errorf("ClosedOn = %v, want nil - a freshly opened envelope is not closed", created.ClosedOn)
	}

	purpose, err := q.GetPurposeForFund(ctx, store.GetPurposeForFundParams{ID: created.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetPurposeForFund() = %v, want no error", err)
	}
	if purpose.Kind != "incidental" {
		t.Errorf("purpose.Kind = %q, want %q", purpose.Kind, "incidental")
	}
	if purpose.FundID != f.fundID {
		t.Errorf("purpose.FundID = %d, want %d", purpose.FundID, f.fundID)
	}
	if purpose.Name != "Jane's wedding" {
		t.Errorf("purpose.Name = %q, want %q", purpose.Name, "Jane's wedding")
	}

	fetched, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: created.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if !reflect.DeepEqual(fetched, created) {
		t.Errorf("GetIncidental() = %+v, want %+v (the row OpenIncidental returned)", fetched, created)
	}
}

// TargetAmount is optional: the schema allows target_amount to be NULL,
// meaning "no target", and OpenIncidental must pass that through rather than
// defaulting to zero.
func TestOpenIncidentalWithoutTargetAmount(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	created, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Flood relief", OpenedOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}
	if created.TargetAmount != nil {
		t.Errorf("TargetAmount = %v, want nil", created.TargetAmount)
	}
}

// Atomicity of the open: if the second insert (incidental) fails after the
// first (purpose) already succeeded within the same withTx, neither survives
// - no orphan purpose.
//
// Forcing this requires a real conflict on incidental's own PRIMARY KEY
// (purpose_id), since every other failure mode of CreateIncidental (a
// malformed occasion, opened_on, or a non-positive target_amount) is already
// rejected by OpenIncidental's own validation before either insert runs, and
// the purpose_id it passes always names the row CreatePurpose just created
// in the same transaction, so the ordinary foreign key cannot be made to
// fail. The lever actually available: SQLite assigns a rowid table's next id
// as (current max + 1) - documented, deterministic behavior, not
// implementation trivia - so the fixture's three purposes (ids 1-3, created
// with no gaps) guarantee the next purpose OpenIncidental creates gets id 4.
// Pre-occupying purpose_id 4 in `incidental` before calling OpenIncidental
// then forces its CreateIncidental to collide with a real, already-existing
// row. Building that pre-existing row requires a purpose_id with no live
// purpose behind it, which the schema's own foreign key would refuse under
// normal enforcement - so this is the one place in the whole suite that
// toggles `PRAGMA foreign_keys` around a single raw INSERT, to construct the
// precondition; the failure OpenIncidental then hits is a genuine PRIMARY
// KEY violation on incidental.purpose_id, not a mocked or injected error.
func TestOpenIncidentalAtomicityNoOrphanPurposeWhenSecondInsertFails(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	const nextPurposeID = 4 // fixture's purposes claim ids 1-3; see the comment above.

	if _, err := l.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disabling foreign_keys for setup = %v, want no error", err)
	}
	if _, err := l.db.ExecContext(ctx,
		`INSERT INTO incidental (purpose_id, occasion, opened_on, created_at) VALUES (?, ?, ?, ?)`,
		nextPurposeID, "Squatter", "2026-01-01", 1,
	); err != nil {
		t.Fatalf("pre-occupying purpose_id %d = %v, want no error", nextPurposeID, err)
	}
	if _, err := l.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("re-enabling foreign_keys after setup = %v, want no error", err)
	}

	purposesBefore, err := q.ListPurposesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListPurposesByFund() before = %v, want no error", err)
	}
	var incidentalCountBefore int
	if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidental").Scan(&incidentalCountBefore); err != nil {
		t.Fatalf("counting incidental rows before = %v, want no error", err)
	}

	_, err = l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Jane's wedding", OpenedOn: "2026-08-12",
	})
	if err == nil {
		t.Fatal("OpenIncidental() = nil error, want a primary key conflict on the second insert")
	}

	purposesAfter, err := q.ListPurposesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListPurposesByFund() after = %v, want no error", err)
	}
	if len(purposesAfter) != len(purposesBefore) {
		t.Errorf("ListPurposesByFund() returned %d rows after the failed open, want %d (unchanged) - an orphan purpose survived", len(purposesAfter), len(purposesBefore))
	}
	for _, p := range purposesAfter {
		if p.Name == "Jane's wedding" {
			t.Errorf("found an orphan purpose %+v after a failed OpenIncidental, want none", p)
		}
	}

	var incidentalCountAfter int
	if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incidental").Scan(&incidentalCountAfter); err != nil {
		t.Fatalf("counting incidental rows after = %v, want no error", err)
	}
	if incidentalCountAfter != incidentalCountBefore {
		t.Errorf("incidental table holds %d rows after the failed open, want %d (unchanged)", incidentalCountAfter, incidentalCountBefore)
	}
}

func TestOpenIncidentalRejectsEmptyOccasion(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "   ", OpenedOn: "2026-08-12",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenIncidental() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	purposes, err := store.New(l.db).ListPurposesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListPurposesByFund() = %v, want no error", err)
	}
	if len(purposes) != 3 {
		t.Errorf("ListPurposesByFund() returned %d rows after a rejected open, want 3 (the fixture's, unchanged)", len(purposes))
	}
}

func TestOpenIncidentalRejectsNonPositiveTargetAmount(t *testing.T) {
	tests := []struct {
		name   string
		target money.Amount
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.OpenIncidental(ctx, OpenIncidentalParams{
				FundID: f.fundID, Occasion: "Jane's wedding", TargetAmount: &tt.target, OpenedOn: "2026-08-12",
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("OpenIncidental() = %v, want an error wrapping ErrInvalidArgument", err)
			}
		})
	}
}

func TestOpenIncidentalRejectsInvalidOpenedOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Jane's wedding", OpenedOn: "2026-02-30",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenIncidental() = %v, want an error wrapping ErrInvalidArgument", err)
	}
}

// A positive leftover rolls into the main purpose, and closed_on is set in
// the same call: FundBalance is unchanged (nothing moved, only what it is
// for), the incidental purpose's balance goes to exactly 0, and the main
// purpose's balance rises by exactly the leftover.
func TestCloseIncidentalAndRollPositiveLeftoverRollsAndCloses(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 30_000, OccurredOn: "2026-08-03",
	}); err != nil {
		t.Fatalf("PostTransaction(out) = %v, want no error", err)
	}

	fundBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}
	mainBefore, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) before = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 70_000 {
		t.Errorf("CloseIncidentalAndRoll() rolled = %d, want 70000", rolled)
	}

	fundAfter, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	if fundAfter != fundBefore {
		t.Errorf("FundBalance() before=%d after=%d, want identical - a roll moves money, it does not create or destroy it", fundBefore, fundAfter)
	}

	incidenBal, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(incidental) = %v, want no error", err)
	}
	if incidenBal != 0 {
		t.Errorf("PurposeBalance(incidental) = %d, want 0 - the leftover rolled out", incidenBal)
	}

	mainAfter, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) after = %v, want no error", err)
	}
	if mainAfter != mainBefore+70_000 {
		t.Errorf("PurposeBalance(main) after = %d, want %d (before + the 70000 leftover)", mainAfter, mainBefore+70_000)
	}

	closed, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if closed.ClosedOn == nil || *closed.ClosedOn != "2026-08-20" {
		t.Errorf("ClosedOn = %v, want \"2026-08-20\" - set in the same call that posted the roll", closed.ClosedOn)
	}
}

// A zero leftover closes the envelope and posts nothing - not an error, and
// not a zero-amount transfer either.
func TestCloseIncidentalAndRollZeroLeftoverClosesWithoutPosting(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-03",
	}); err != nil {
		t.Fatalf("PostTransaction(out) = %v, want no error", err)
	}

	txBefore, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() before = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 0 {
		t.Errorf("CloseIncidentalAndRoll() rolled = %d, want 0", rolled)
	}

	txAfter, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() after = %v, want no error", err)
	}
	if len(txAfter) != len(txBefore) {
		t.Errorf("ListTransactionsByFund() returned %d rows after a zero-leftover close, want %d (unchanged) - nothing should have posted", len(txAfter), len(txBefore))
	}

	closed, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if closed.ClosedOn == nil || *closed.ClosedOn != "2026-08-20" {
		t.Errorf("ClosedOn = %v, want \"2026-08-20\"", closed.ClosedOn)
	}
}

// A negative leftover - the envelope disbursed more than it collected -
// closes the envelope AND covers the shortfall from the fund's main purpose
// (ADR-031, superseding ADR-027's original "closes and posts nothing"
// branch): FundBalance is unchanged (nothing moved, only what it is for),
// the incidental purpose's balance still goes to exactly 0, and the main
// purpose's balance falls by exactly the shortfall - the same invariant
// TestCloseIncidentalAndRollPositiveLeftoverRollsAndCloses proves for the
// opposite sign.
func TestCloseIncidentalAndRollNegativeLeftoverCoversFromMainAndCloses(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-03",
	}); err != nil {
		t.Fatalf("PostTransaction(out) = %v, want no error", err)
	}

	fundBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}
	mainBefore, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) before = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != -30_000 {
		t.Errorf("CloseIncidentalAndRoll() rolled = %d, want -30000 (negative: covered from Kas Utama)", rolled)
	}

	fundAfter, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	if fundAfter != fundBefore {
		t.Errorf("FundBalance() before=%d after=%d, want identical - covering a shortfall moves money, it does not create or destroy it", fundBefore, fundAfter)
	}

	incidenBal, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(incidental) = %v, want no error", err)
	}
	if incidenBal != 0 {
		t.Errorf("PurposeBalance(incidental) = %d, want 0 - the invariant is exactly zero, always, whichever direction squared it", incidenBal)
	}

	mainAfter, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) after = %v, want no error", err)
	}
	if mainAfter != mainBefore-30_000 {
		t.Errorf("PurposeBalance(main) after = %d, want %d (before - the 30000 shortfall covered)", mainAfter, mainBefore-30_000)
	}

	closed, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if closed.ClosedOn == nil || *closed.ClosedOn != "2026-08-20" {
		t.Errorf("ClosedOn = %v, want \"2026-08-20\"", closed.ClosedOn)
	}
}

// A second call on an already-closed envelope is refused with the named
// sentinel and posts nothing - a second roll would move money that already
// moved.
func TestCloseIncidentalAndRollSecondCallReturnsAlreadyClosed(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}

	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	}); err != nil {
		t.Fatalf("first CloseIncidentalAndRoll() = %v, want no error", err)
	}

	txBefore, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() before second call = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-21",
	})
	if !errors.Is(err, ErrIncidentalAlreadyClosed) {
		t.Fatalf("second CloseIncidentalAndRoll() = %v, want an error wrapping ErrIncidentalAlreadyClosed", err)
	}
	if rolled != 0 {
		t.Errorf("second CloseIncidentalAndRoll() rolled = %d, want 0", rolled)
	}

	txAfter, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() after second call = %v, want no error", err)
	}
	if len(txAfter) != len(txBefore) {
		t.Errorf("ListTransactionsByFund() returned %d rows after a refused second roll, want %d (unchanged)", len(txAfter), len(txBefore))
	}

	closed, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if closed.ClosedOn == nil || *closed.ClosedOn != "2026-08-20" {
		t.Errorf("ClosedOn = %v, want \"2026-08-20\" (the first call's date) - the refused second call must not have touched it", closed.ClosedOn)
	}
}

// A roll whose pair fails leaves the envelope open: the close and the pair
// are one transaction, so neither half survives alone. Forced with an
// account that belongs to another fund entirely, mirroring
// TestPostTransferBetweenAccountsLeavesNoRowsWhenTheSecondLegFails's
// approach - a real composite foreign key violation, not a fake.
func TestCloseIncidentalAndRollPairFailureLeavesEnvelopeOpen(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}

	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	otherAccount := createAccount(t, q, other.ID, "cash", "Other Fund's Cash")

	txBefore, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() before = %v, want no error", err)
	}

	_, err = l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: otherAccount, ClosedOn: "2026-08-20",
	})
	if err == nil {
		t.Fatal("CloseIncidentalAndRoll() across funds = nil error, want a foreign key violation")
	}
	if errors.Is(err, ErrIncidentalAlreadyClosed) {
		t.Errorf("CloseIncidentalAndRoll() = %v, want a foreign key error, not ErrIncidentalAlreadyClosed", err)
	}

	txAfter, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() after = %v, want no error", err)
	}
	if len(txAfter) != len(txBefore) {
		t.Errorf("ListTransactionsByFund() returned %d rows after a failed roll, want %d (unchanged) - the whole write must have rolled back", len(txAfter), len(txBefore))
	}

	stillOpen, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if stillOpen.ClosedOn != nil {
		t.Errorf("ClosedOn = %v, want nil - the failed pair must leave the envelope open, not closed", stillOpen.ClosedOn)
	}
}

func TestCloseIncidentalAndRollRejectsInvalidClosedOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	_, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-02-30",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	stillOpen, err := store.New(l.db).GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if stillOpen.ClosedOn != nil {
		t.Errorf("ClosedOn = %v, want nil - a rejected call must not close the envelope", stillOpen.ClosedOn)
	}
}

// A second fund's envelope is invisible to the first fund, and closing it
// across the fund boundary is refused. An id names a row; it does not prove
// the caller may see it. PRD section 6 allows a server to hold more than one
// fund, so these two are the scoping tests that keep that honest - v1's
// single-fund rule is a setup constraint, not a reason to read unscoped.
func TestASecondFundsIncidentalIsInvisibleToTheFirstFund(t *testing.T) {
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

	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: other.ID, Occasion: "Fund 2's occasion", OpenedOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("OpenIncidental(fund 2) = %v, want no error", err)
	}

	if _, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetIncidentalDetail(fund 1, fund 2's envelope) = %v, want an error wrapping sql.ErrNoRows", err)
	}

	// The same envelope read through its own fund still resolves, so the
	// test above is scoping and not a broken lookup.
	if _, err := l.GetIncidentalDetail(ctx, other.ID, envelope.PurposeID); err != nil {
		t.Errorf("GetIncidentalDetail(fund 2, fund 2's envelope) = %v, want no error", err)
	}
}

func TestClosingASecondFundsIncidentalAcrossTheBoundaryIsRefused(t *testing.T) {
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

	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: other.ID, Occasion: "Fund 2's occasion", OpenedOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("OpenIncidental(fund 2) = %v, want no error", err)
	}

	_, err = l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-13",
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CloseIncidentalAndRoll(fund 1, fund 2's envelope) = %v, want an error wrapping sql.ErrNoRows", err)
	}

	stillOpen, err := l.GetIncidentalDetail(ctx, other.ID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if stillOpen.Incidental.ClosedOn != nil {
		t.Errorf("ClosedOn = %v, want nil - a cross-fund close must not close the envelope", stillOpen.Incidental.ClosedOn)
	}
}

// The roll is a transfer like any other, so its note lands on both legs -
// and the ledger writes no note of its own when none is given: an
// unexplained roll stays unexplained rather than acquiring a sentence
// nobody wrote (ADR-014).
func TestCloseIncidentalAndRollWritesTheNoteToBothRollLegs(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}

	note := "Sisa dana digulung ke kas utama"
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID,
		ClosedOn: "2026-08-20", Note: &note,
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	legs := 0
	for _, row := range rows {
		if row.Kind != "transfer" {
			continue
		}
		legs++
		if row.Note == nil || *row.Note != note {
			t.Errorf("roll leg(%s).Note = %v, want %q", row.Direction, row.Note, note)
		}
	}
	if legs != 2 {
		t.Fatalf("found %d transfer legs, want 2", legs)
	}
}

func TestCloseIncidentalAndRollWritesNoNoteWhenNoneIsGiven(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}

	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	for _, row := range rows {
		if row.Kind == "transfer" && row.Note != nil {
			t.Errorf("roll leg(%s).Note = %q, want NULL", row.Direction, *row.Note)
		}
	}
}

// --- #215: GetIncidentalDetail must not report a roll's own leg as fresh
// collection or disbursement -------------------------------------------

// The regression #215 was filed for: collect 100.000, spend 30.000, close
// (positive leftover, 70.000 rolls out). Before the fix, IncidentalTotals'
// unfiltered sum counted the roll's own "out" leg toward disbursed_amount,
// so the detail screen read "Terkumpul 100.000 / Terpakai 100.000" - a lie,
// since 70.000 of that went back to Kas Utama, not to anything the occasion
// spent. IncidentalActivityTotals must exclude that leg.
func TestGetIncidentalDetailExcludesTheRolledOutLegAfterAPositiveClose(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 30_000, OccurredOn: "2026-08-03",
	}); err != nil {
		t.Fatalf("PostTransaction(out) = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 70_000 {
		t.Fatalf("CloseIncidentalAndRoll() rolled = %d, want 70000", rolled)
	}

	detail, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Collected != 100_000 {
		t.Errorf("Collected = %d, want 100000 - the roll's own legs must not inflate this", detail.Collected)
	}
	if detail.Disbursed != 30_000 {
		t.Errorf("Disbursed = %d, want 30000, not 100000 (#215: the rolled-out leg is not money the occasion spent)", detail.Disbursed)
	}
}

// The mirror case on the "in" side: an over-disbursed envelope's shortfall
// is covered by an "in" leg at the incidental purpose (ADR-031). That leg
// must not inflate collected_amount either, or a treasurer would read a
// covering transfer from Kas Utama as if it were a fresh contribution.
func TestGetIncidentalDetailExcludesTheCoveringInLegAfterANegativeClose(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-03",
	}); err != nil {
		t.Fatalf("PostTransaction(out) = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != -30_000 {
		t.Fatalf("CloseIncidentalAndRoll() rolled = %d, want -30000", rolled)
	}

	detail, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Collected != 20_000 {
		t.Errorf("Collected = %d, want 20000, not 50000 (the covering leg from Kas Utama is not a fresh contribution)", detail.Collected)
	}
	if detail.Disbursed != 50_000 {
		t.Errorf("Disbursed = %d, want 50000 - unaffected by the cover", detail.Disbursed)
	}
}

// --- Reopen (ADR-031, #214): the deliberate, visible way back ----------

func TestReopenIncidentalClearsClosedOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	reopened, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("ReopenIncidental() = %v, want no error", err)
	}
	if reopened.ClosedOn != nil {
		t.Errorf("ClosedOn = %v, want nil after reopening", reopened.ClosedOn)
	}

	fetched, err := store.New(l.db).GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if fetched.ClosedOn != nil {
		t.Errorf("persisted ClosedOn = %v, want nil", fetched.ClosedOn)
	}
}

// Reopening an envelope that is not closed is refused, not a silent no-op.
func TestReopenIncidentalRejectsAnOpenEnvelope(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	_, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID)
	if !errors.Is(err, ErrIncidentalNotClosed) {
		t.Fatalf("ReopenIncidental() on an open envelope = %v, want an error wrapping ErrIncidentalNotClosed", err)
	}
}

// A second fund's closed envelope is invisible to the first fund, and
// reopening it across the fund boundary is refused with the same
// GetIncidental-backed sql.ErrNoRows every other single-envelope call in
// this file answers with - an id names a row, not permission to see it.
func TestReopenIncidentalIsFundScoped(t *testing.T) {
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

	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: other.ID, Occasion: "Fund 2's occasion", OpenedOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("OpenIncidental(fund 2) = %v, want no error", err)
	}
	otherAccount := createAccount(t, q, other.ID, "cash", "Other Fund's Cash")
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: other.ID, PurposeID: envelope.PurposeID, AccountID: otherAccount, ClosedOn: "2026-08-13",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll(fund 2) = %v, want no error", err)
	}

	if _, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ReopenIncidental(fund 1, fund 2's envelope) = %v, want an error wrapping sql.ErrNoRows", err)
	}
}

// --- The invariant to test hardest: a closed envelope's purpose balance
// is exactly zero, always - across reopen, a late entry in either
// direction, and a second reopen. ---------------------------------------

// A late contribution after reopening: close (rolls the original leftover
// out), reopen, post a late "in", close again. The second close must roll
// only the net delta - no new arithmetic, per ADR-031 - and the purpose
// balance must land back at exactly zero.
func TestReopenLateContributionThenRecloseRollsOnlyTheNetDeltaAndZerosTheBalance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("first CloseIncidentalAndRoll() = %v, want no error", err)
	} else if rolled != 100_000 {
		t.Fatalf("first CloseIncidentalAndRoll() rolled = %d, want 100000", rolled)
	}

	if _, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("ReopenIncidental() = %v, want no error", err)
	}

	// A late contribution, dated after the original close - ADR-031 needs
	// no backdating logic (ADR-024's id cutoff already covers it), and this
	// is the ordinary case anyway: money that arrives after the envelope
	// was thought done.
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-25",
	}); err != nil {
		t.Fatalf("PostTransaction(late in) = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-26",
	})
	if err != nil {
		t.Fatalf("second CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 20_000 {
		t.Errorf("second CloseIncidentalAndRoll() rolled = %d, want 20000 (the net delta, not 120000)", rolled)
	}

	bal, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance() = %v, want no error", err)
	}
	if bal != 0 {
		t.Errorf("PurposeBalance(envelope) = %d, want 0 - the invariant is exactly zero, always", bal)
	}
}

// A late bill after reopening: close (rolls the original leftover out),
// reopen, post a late "out" larger than what remains, close again. The
// shortfall must be covered from Kas Utama, and the balance must still land
// at exactly zero.
func TestReopenLateDisbursementThenRecloseCoversTheShortfallAndZerosTheBalance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("first CloseIncidentalAndRoll() = %v, want no error", err)
	} else if rolled != 50_000 {
		t.Fatalf("first CloseIncidentalAndRoll() rolled = %d, want 50000", rolled)
	}

	if _, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("ReopenIncidental() = %v, want no error", err)
	}

	// A late bill for the occasion - PRD §7.5's own scenario for why the
	// guard is symmetric.
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 20_000, OccurredOn: "2026-08-25",
	}); err != nil {
		t.Fatalf("PostTransaction(late out) = %v, want no error", err)
	}

	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-26",
	})
	if err != nil {
		t.Fatalf("second CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != -20_000 {
		t.Errorf("second CloseIncidentalAndRoll() rolled = %d, want -20000 (the net delta, covered from Kas Utama)", rolled)
	}

	bal, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance() = %v, want no error", err)
	}
	if bal != 0 {
		t.Errorf("PurposeBalance(envelope) = %d, want 0 - the invariant is exactly zero, always", bal)
	}
}

// Reopening twice: close, reopen, close again with nothing new to roll,
// reopen again, post a late entry, close a third time. Every close leaves
// the purpose balance at exactly zero - the invariant holds across more
// than one reopen cycle, not just one.
func TestReopenTwiceEachCloseZerosTheBalance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Jane's wedding", "2026-08-01")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("first CloseIncidentalAndRoll() = %v, want no error", err)
	}

	// Reopen and close again with nothing new posted: the leftover is back
	// to 0 (the first roll's own leg already nets it), so this close posts
	// nothing.
	if _, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("first ReopenIncidental() = %v, want no error", err)
	}
	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-11",
	})
	if err != nil {
		t.Fatalf("second CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 0 {
		t.Errorf("second CloseIncidentalAndRoll() rolled = %d, want 0 (nothing new to roll)", rolled)
	}
	if bal, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("PurposeBalance() after second close = %v, want no error", err)
	} else if bal != 0 {
		t.Errorf("PurposeBalance() after second close = %d, want 0", bal)
	}

	// Reopen a second time and post a late contribution, then close a
	// third time.
	if _, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("second ReopenIncidental() = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-08-15",
	}); err != nil {
		t.Fatalf("PostTransaction(late in) = %v, want no error", err)
	}
	rolled, err = l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-16",
	})
	if err != nil {
		t.Fatalf("third CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 10_000 {
		t.Errorf("third CloseIncidentalAndRoll() rolled = %d, want 10000", rolled)
	}
	if bal, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("PurposeBalance() after third close = %v, want no error", err)
	} else if bal != 0 {
		t.Errorf("PurposeBalance() after third close = %d, want 0", bal)
	}
}
