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

	purpose, err := q.GetPurpose(ctx, created.PurposeID)
	if err != nil {
		t.Fatalf("GetPurpose() = %v, want no error", err)
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
// closes the envelope and posts nothing, exactly like the zero case. Neither
// is an error: an error inside withTx would roll the close back, and nothing
// asks for an over-disbursed envelope to stay open.
func TestCloseIncidentalAndRollNegativeLeftoverClosesWithoutPosting(t *testing.T) {
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
		t.Errorf("ListTransactionsByFund() returned %d rows after a negative-leftover close, want %d (unchanged) - nothing should have posted", len(txAfter), len(txBefore))
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
