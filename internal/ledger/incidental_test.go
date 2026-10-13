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

// openTestIncidental opens a fresh envelope through OpenIncidental, since
// every CloseIncidentalAndRoll test needs a real incidental row to close.
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
	t.Parallel()
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
	t.Parallel()
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

// Atomicity of the open: if the incidental insert fails after the purpose
// insert succeeded in the same withTx, neither survives (no orphan purpose).
//
// The lever is a real PRIMARY KEY conflict on incidental.purpose_id: every
// other CreateIncidental failure is rejected by OpenIncidental's validation
// first, and the purpose_id it passes always names the row just created. A
// rowid table's next id is (current max + 1) (documented SQLite behaviour),
// so the fixture's three purposes (ids 1-3) guarantee the next purpose gets
// id 4. Pre-occupying purpose_id 4 in `incidental` forces the collision. That
// row has no live purpose behind it, which the foreign key would refuse, so
// this is the one place in the suite that toggles `PRAGMA foreign_keys`
// around a single raw INSERT. The failure OpenIncidental hits is a genuine
// PRIMARY KEY violation, not an injected error.
func TestOpenIncidentalAtomicityNoOrphanPurposeWhenSecondInsertFails(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	tests := []struct {
		name   string
		target money.Amount
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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
	t.Parallel()
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

// --- Rename (#264): correcting a mistyped occasion moves both rows and no
// money -------------------------------------------------------------------

// RenameIncidental moves both purpose.name and incidental.occasion together,
// and posts no transaction and changes no balance - the trust-core
// assertion, checked with exact integer comparisons (ADR-015, CLAUDE.md
// rule 2).
func TestRenameIncidentalMovesBothRowsAndNoMoney(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Halal bihalal RT", "2026-08-01")
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); err != nil {
		t.Fatalf("PostTransaction(in) = %v, want no error", err)
	}

	fundBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}
	envelopeBalBefore, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(envelope) before = %v, want no error", err)
	}
	txBefore, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() before = %v, want no error", err)
	}

	renamed, err := l.RenameIncidental(ctx, RenameIncidentalParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, Occasion: "Halal bihalal RT 2026",
	})
	if err != nil {
		t.Fatalf("RenameIncidental() = %v, want no error", err)
	}
	if renamed.Occasion != "Halal bihalal RT 2026" {
		t.Errorf("Occasion = %q, want %q", renamed.Occasion, "Halal bihalal RT 2026")
	}

	purpose, err := q.GetPurposeForFund(ctx, store.GetPurposeForFundParams{ID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetPurposeForFund() = %v, want no error", err)
	}
	if purpose.Name != "Halal bihalal RT 2026" {
		t.Errorf("purpose.Name = %q, want %q - both rows must move together", purpose.Name, "Halal bihalal RT 2026")
	}

	fetched, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if fetched.Occasion != "Halal bihalal RT 2026" {
		t.Errorf("incidental.Occasion = %q, want %q", fetched.Occasion, "Halal bihalal RT 2026")
	}

	// The trust-core assertion: no transaction posted, no balance moved.
	txAfter, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() after = %v, want no error", err)
	}
	if len(txAfter) != len(txBefore) {
		t.Errorf("ListTransactionsByFund() returned %d rows after a rename, want %d (unchanged) - a rename must post nothing", len(txAfter), len(txBefore))
	}
	fundAfter, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	if fundAfter != fundBefore {
		t.Errorf("FundBalance() before=%d after=%d, want identical - a rename moves no money", fundBefore, fundAfter)
	}
	envelopeBalAfter, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(envelope) after = %v, want no error", err)
	}
	if envelopeBalAfter != envelopeBalBefore {
		t.Errorf("PurposeBalance(envelope) before=%d after=%d, want identical - a rename moves no money", envelopeBalBefore, envelopeBalAfter)
	}
}

// A closed envelope renames successfully: the typo is usually found after the
// occasion is over.
func TestRenameIncidentalOnAClosedEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Halal bihalal RT", "2026-08-01")
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	renamed, err := l.RenameIncidental(ctx, RenameIncidentalParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, Occasion: "Halal bihalal RT (typo fixed)",
	})
	if err != nil {
		t.Fatalf("RenameIncidental() on a closed envelope = %v, want no error", err)
	}
	if renamed.Occasion != "Halal bihalal RT (typo fixed)" {
		t.Errorf("Occasion = %q, want %q", renamed.Occasion, "Halal bihalal RT (typo fixed)")
	}
	if renamed.ClosedOn == nil {
		t.Error("ClosedOn = nil, want still closed - renaming must not reopen the envelope")
	}
}

// An empty or whitespace-only occasion is refused, as in OpenIncidental, and
// the row is left untouched.
func TestRenameIncidentalRejectsEmptyOccasion(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Halal bihalal RT", "2026-08-01")

	_, err := l.RenameIncidental(ctx, RenameIncidentalParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, Occasion: "   ",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("RenameIncidental() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	unchanged, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if unchanged.Occasion != "Halal bihalal RT" {
		t.Errorf("Occasion = %q, want %q (unchanged) - a rejected rename must not touch the row", unchanged.Occasion, "Halal bihalal RT")
	}
}

// A second fund's envelope is invisible to the first fund, as for every
// single-envelope write in this file.
func TestRenameIncidentalIsFundScoped(t *testing.T) {
	t.Parallel()
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

	if _, err := l.RenameIncidental(ctx, RenameIncidentalParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, Occasion: "Hijacked",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RenameIncidental(fund 1, fund 2's envelope) = %v, want an error wrapping sql.ErrNoRows", err)
	}

	stillNamed, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: other.ID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if stillNamed.Occasion != "Fund 2's occasion" {
		t.Errorf("Occasion = %q, want %q - a cross-fund rename must not have touched it", stillNamed.Occasion, "Fund 2's occasion")
	}
}

// A positive leftover rolls into the main purpose and closed_on is set in
// the same call: FundBalance is unchanged (only what it is for moved), the
// incidental purpose's balance goes to exactly 0, and the main purpose's
// rises by exactly the leftover.
func TestCloseIncidentalAndRollPositiveLeftoverRollsAndCloses(t *testing.T) {
	t.Parallel()
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

// A zero leftover closes the envelope and posts nothing: not an error, and
// not a zero-amount transfer.
func TestCloseIncidentalAndRollZeroLeftoverClosesWithoutPosting(t *testing.T) {
	t.Parallel()
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

// A negative leftover (the envelope disbursed more than it collected) closes
// the envelope and covers the shortfall from the main purpose (ADR-031,
// superseding ADR-027's "closes and posts nothing"): FundBalance is
// unchanged, the incidental purpose's balance goes to exactly 0, and the main
// purpose's falls by exactly the shortfall, the mirror of
// TestCloseIncidentalAndRollPositiveLeftoverRollsAndCloses.
func TestCloseIncidentalAndRollNegativeLeftoverCoversFromMainAndCloses(t *testing.T) {
	t.Parallel()
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
// sentinel and posts nothing; a second roll would move money twice.
func TestCloseIncidentalAndRollSecondCallReturnsAlreadyClosed(t *testing.T) {
	t.Parallel()
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
// are one transaction. Forced with an account of another fund, a real
// composite foreign key violation, as in
// TestPostTransferBetweenAccountsLeavesNoRowsWhenTheSecondLegFails.
func TestCloseIncidentalAndRollPairFailureLeavesEnvelopeOpen(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
// across the boundary is refused. An id names a row, not permission to see
// it. PRD section 6 allows more than one fund; v1's single-fund rule is a
// setup constraint, not a reason to read unscoped.
func TestASecondFundsIncidentalIsInvisibleToTheFirstFund(t *testing.T) {
	t.Parallel()
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

	// The same envelope read through its fund still resolves, so the test above
	// is scoping and not a broken lookup.
	if _, err := l.GetIncidentalDetail(ctx, other.ID, envelope.PurposeID); err != nil {
		t.Errorf("GetIncidentalDetail(fund 2, fund 2's envelope) = %v, want no error", err)
	}
}

func TestClosingASecondFundsIncidentalAcrossTheBoundaryIsRefused(t *testing.T) {
	t.Parallel()
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

// The roll is a transfer like any other, so its note lands on both legs, and
// the ledger writes no note when none is given: the list labels such a roll
// from the transfer's facts at read time (#257), never from stored text.
func TestCloseIncidentalAndRollWritesTheNoteToBothRollLegs(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// --- #215: GetIncidentalDetail must not report a roll's leg as fresh activity

// The regression #215: collect 100.000, spend 30.000, close (70.000 rolls
// out). IncidentalTotals' unfiltered sum counted the roll's "out" leg toward
// disbursed_amount, so the detail read "Terkumpul 100.000 / Terpakai
// 100.000", though 70.000 went back to Kas Utama. IncidentalActivityTotals
// must exclude that leg.
func TestGetIncidentalDetailExcludesTheRolledOutLegAfterAPositiveClose(t *testing.T) {
	t.Parallel()
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

// A cancelled contribution (ADR-034) is neither collected nor spent: a dev
// envelope read "Terkumpul 345.000 / Terpakai 210.000" with nothing paid out,
// because the reversals counted as disbursements and the cancelled rows as
// collections. Both halves drop; collected minus disbursed still equals the
// envelope's balance.
func TestGetIncidentalDetailDropsBothHalvesOfACancelledContribution(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Amplop Kosong", "2026-08-01")
	bayu := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Bayu"})
	ijah := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Ijah"})
	trias := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Trias"})

	post := func(amount money.Amount, memberID *int64) int64 {
		t.Helper()
		row, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
			Direction: "in", Amount: amount, OccurredOn: "2026-08-02", MemberID: memberID,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%d) = %v, want no error", amount, err)
		}
		return row.ID
	}
	post(20_000, &bayu)
	post(15_000, &ijah)
	ijahSecond := post(10_000, &ijah)
	post(100_000, nil)
	triasRow := post(200_000, &trias)

	for _, id := range []int64{triasRow, ijahSecond} {
		if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, TransactionID: id, OccurredOn: "2026-08-03"}); err != nil {
			t.Fatalf("ReverseDuesPayment(%d) = %v, want no error", id, err)
		}
	}

	detail, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Collected != 135_000 {
		t.Errorf("Collected = %d, want 135000 - a cancelled contribution was never collected", detail.Collected)
	}
	if detail.Disbursed != 0 {
		t.Errorf("Disbursed = %d, want 0 - a cancellation is not money the occasion spent", detail.Disbursed)
	}

	balance, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance() = %v, want no error", err)
	}
	if balance != 135_000 {
		t.Errorf("PurposeBalance() = %d, want 135000 (the ledger itself is unchanged)", balance)
	}
}

// The mirror case on the "in" side: an over-disbursed envelope's shortfall is
// covered by an "in" leg at the incidental purpose (ADR-031), which must not
// inflate collected_amount.
func TestGetIncidentalDetailExcludesTheCoveringInLegAfterANegativeClose(t *testing.T) {
	t.Parallel()
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

// #342: corrections that bring money INTO an envelope (back after an earlier
// correction out, or from Kas Utama) read as collected there, and an expense
// corrected away un-spends. Collected minus disbursed equals the balance at
// every step.
func TestGetIncidentalDetailReadsCorrectionsIntoAndOutOfTheEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Amplop Bolak-balik", "2026-08-01")

	assertDetail := func(step string, wantCollected, wantDisbursed money.Amount) {
		t.Helper()
		detail, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID)
		if err != nil {
			t.Fatalf("%s: GetIncidentalDetail() = %v, want no error", step, err)
		}
		balance, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
		if err != nil {
			t.Fatalf("%s: PurposeBalance() = %v, want no error", step, err)
		}
		if detail.Collected != wantCollected || detail.Disbursed != wantDisbursed {
			t.Errorf("%s: Collected, Disbursed = %d, %d, want %d, %d", step, detail.Collected, detail.Disbursed, wantCollected, wantDisbursed)
		}
		if detail.Collected-detail.Disbursed != balance {
			t.Errorf("%s: Collected - Disbursed = %d, want it to equal PurposeBalance() = %d", step, detail.Collected-detail.Disbursed, balance)
		}
	}
	post := func(purposeID int64, direction string, amount money.Amount) int64 {
		t.Helper()
		posted, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: purposeID,
			Direction: direction, Amount: amount, OccurredOn: "2026-08-12",
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d) = %v, want no error", direction, amount, err)
		}
		return posted.ID
	}
	correct := func(transactionID, purposeID int64) {
		t.Helper()
		if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
			FundID: f.fundID, TransactionID: transactionID, PurposeID: purposeID,
		}); err != nil {
			t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
		}
	}

	contribution := post(envelope.PurposeID, "in", 25_000)
	correct(contribution, f.mainID)
	assertDetail("corrected out", 0, 0)
	correct(contribution, envelope.PurposeID)
	assertDetail("corrected back in", 25_000, 0)

	fromMain := post(f.mainID, "in", 10_000)
	correct(fromMain, envelope.PurposeID)
	assertDetail("Kas Utama contribution corrected in", 35_000, 0)

	expense := post(envelope.PurposeID, "out", 4_000)
	assertDetail("expense posted", 35_000, 4_000)
	correct(expense, f.mainID)
	assertDetail("expense corrected away", 35_000, 0)
}

// #342: an unnamed contribution moved out of an envelope with a peruntukan
// correction (ADR-033) is money that left, not money collected.
// IncidentalActivityTotals excluded the correction pair's legs (the
// tr.kind = 'reclass_purpose' filter from #215) but left the original
// contribution standing, so Terkumpul counted money no longer there. As with
// ADR-034's cancellation, both halves (the original row and its matching
// reclass_purpose legs) drop from the envelope's activity, and collected
// minus disbursed still equals the balance (zero: the pair is value-neutral).
func TestGetIncidentalDetailExcludesAContributionCorrectedOutOfTheEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Amplop Kosong", "2026-08-01")

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("PostTransaction(unnamed contribution) = %v, want no error", err)
	}

	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	detail, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Collected != 0 {
		t.Errorf("Collected = %d, want 0 - the contribution moved to Kas Utama, the envelope never kept it", detail.Collected)
	}
	if detail.Disbursed != 0 {
		t.Errorf("Disbursed = %d, want 0 - a purpose correction is not money the envelope spent", detail.Disbursed)
	}

	balance, err := l.PurposeBalance(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance() = %v, want no error", err)
	}
	if balance != 0 {
		t.Fatalf("PurposeBalance() = %d, want 0", balance)
	}
	if detail.Collected-detail.Disbursed != balance {
		t.Errorf("Collected - Disbursed = %d, want it to equal PurposeBalance() = %d", detail.Collected-detail.Disbursed, balance)
	}
}

// --- Reopen (ADR-031, #214)

func TestReopenIncidentalClearsClosedOn(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
// reopening it across the boundary is refused with the GetIncidental-backed
// sql.ErrNoRows that every single-envelope call here answers with.
func TestReopenIncidentalIsFundScoped(t *testing.T) {
	t.Parallel()
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

// --- The invariant to test hardest: a closed envelope's purpose balance is
// exactly zero, always, across reopen, a late entry either way, and a second
// reopen.

// A late contribution after reopening: close (rolls the leftover out),
// reopen, post a late "in", close again. The second close rolls only the net
// delta (ADR-031) and the purpose balance lands back at exactly zero.
func TestReopenLateContributionThenRecloseRollsOnlyTheNetDeltaAndZerosTheBalance(t *testing.T) {
	t.Parallel()
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

	// A late contribution dated after the original close: ADR-031 needs no
	// backdating logic (ADR-024's id cutoff covers it); this is money arriving
	// after the envelope was thought done.
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

// A late bill after reopening: close, reopen, post a late "out" larger than
// what remains, close again. The shortfall is covered from Kas Utama and the
// balance still lands at exactly zero.
func TestReopenLateDisbursementThenRecloseCoversTheShortfallAndZerosTheBalance(t *testing.T) {
	t.Parallel()
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

	// A late bill for the occasion: PRD section 7.5's scenario for why the guard
	// is symmetric.
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

// Reopening twice: close, reopen, close with nothing new, reopen, post a late
// entry, close a third time. Every close leaves the purpose balance at exactly
// zero across more than one reopen cycle.
func TestReopenTwiceEachCloseZerosTheBalance(t *testing.T) {
	t.Parallel()
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

	// Reopen and close with nothing new posted: the leftover is 0 (the first
	// roll's leg already nets it), so this close posts nothing.
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

	// Reopen again, post a late contribution, close a third time.
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

// TestGetIncidentalDetailBalanceIsThePurposeBalanceNotCollectedMinusDisbursed:
// once a closed envelope is reopened, collected minus disbursed (rolls left
// out, #215) no longer says what it holds; Balance does. Open: 100k in, 40k
// out; close (60k rolls out, balance 0); reopen; 30k more in. Collected is
// 130k, disbursed 40k, and the envelope holds 30k, not 90k.
func TestGetIncidentalDetailBalanceIsThePurposeBalanceNotCollectedMinusDisbursed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	envelope := openTestIncidental(t, l, f.fundID, "Test Collection", "2026-08-01")
	postEntry(t, l, f.fundID, f.cashID, envelope.PurposeID, "in", 100_000, "2026-08-02", nil)
	postEntry(t, l, f.fundID, f.cashID, envelope.PurposeID, "out", 40_000, "2026-08-03", nil)

	detail, err := l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Balance != 60_000 {
		t.Errorf("open Balance = %d, want 60000", detail.Balance)
	}

	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if detail, err = l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Balance != 0 {
		t.Errorf("closed Balance = %d, want 0 (ADR-031)", detail.Balance)
	}

	if _, err := l.ReopenIncidental(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("ReopenIncidental() = %v, want no error", err)
	}
	postEntry(t, l, f.fundID, f.cashID, envelope.PurposeID, "in", 30_000, "2026-08-12", nil)

	if detail, err = l.GetIncidentalDetail(ctx, f.fundID, envelope.PurposeID); err != nil {
		t.Fatalf("GetIncidentalDetail() = %v, want no error", err)
	}
	if detail.Collected != 130_000 || detail.Disbursed != 40_000 {
		t.Errorf("Collected/Disbursed = %d/%d, want 130000/40000", detail.Collected, detail.Disbursed)
	}
	if detail.Balance != 30_000 {
		t.Errorf("reopened Balance = %d, want 30000 - not Collected minus Disbursed (90000)", detail.Balance)
	}
}
