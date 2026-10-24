package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// ADR-042: any ordinary transaction is reversed by one linked entry going the
// other way. These tests hold the ledger method's contract: what it copies, the
// day it picks, and the refusal it gives each target it will not take. The
// walk's footing over reversals is reversal_walk_test.go's; the schema's matrix
// is internal/db's.

// reverseAt is ReverseTransaction at a clock, failing the test on a refusal.
func reverseAt(t *testing.T, l *Ledger, fundID, id int64, now time.Time) store.Transaction {
	t.Helper()
	row, err := l.at(now).ReverseTransaction(context.Background(), ReverseTransactionParams{FundID: fundID, TransactionID: id})
	if err != nil {
		t.Fatalf("ReverseTransaction(%d) = %v, want no error", id, err)
	}
	return row
}

func transactionCount(t *testing.T, l *Ledger, fundID int64) int {
	t.Helper()
	rows, err := store.New(l.db).ListTransactionsByFund(context.Background(), fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	return len(rows)
}

// A reversed out, a reversed in and a corrected-then-reversed row each return
// the fund, the location and the pos to the balance they had before the original
// (ADR-015: integers, compared exactly).
func TestReverseTransactionReturnsEveryBalanceToWhatItWasBefore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type balances struct{ fund, cash, bank, main, pass, envelope money.Amount }
	read := func(t *testing.T, l *Ledger, f fixture, envelope int64) balances {
		t.Helper()
		var b balances
		var err error
		if b.fund, err = l.FundBalance(ctx, f.fundID); err != nil {
			t.Fatal(err)
		}
		if b.cash, err = l.AccountBalance(ctx, f.fundID, f.cashID); err != nil {
			t.Fatal(err)
		}
		if b.bank, err = l.AccountBalance(ctx, f.fundID, f.bankID); err != nil {
			t.Fatal(err)
		}
		if b.main, err = l.PurposeBalance(ctx, f.fundID, f.mainID); err != nil {
			t.Fatal(err)
		}
		if b.pass, err = l.PurposeBalance(ctx, f.fundID, f.passID); err != nil {
			t.Fatal(err)
		}
		if b.envelope, err = l.PurposeBalance(ctx, f.fundID, envelope); err != nil {
			t.Fatal(err)
		}
		return b
	}

	cases := []struct {
		name string
		// prepare posts what the original needs around it; the balances it leaves are
		// the ones the reversal must restore.
		prepare func(t *testing.T, l *Ledger, f fixture)
		// do posts the original (and a correction of it, where the case has one).
		do func(t *testing.T, l *Ledger, f fixture, envelope int64) store.Transaction
	}{
		{"a reversed out", nil, func(t *testing.T, l *Ledger, f fixture, _ int64) store.Transaction {
			return postEntry(t, l, f.fundID, f.bankID, f.mainID, "out", 40_000, "2026-08-12", nil)
		}},
		{"a reversed in", nil, func(t *testing.T, l *Ledger, f fixture, _ int64) store.Transaction {
			return postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 35_000, "2026-08-12", nil)
		}},
		{"a pass-through movement", nil, func(t *testing.T, l *Ledger, f fixture, _ int64) store.Transaction {
			return postEntry(t, l, f.fundID, f.cashID, f.passID, "in", 90_000, "2026-08-12", nil)
		}},
		{"an in corrected to an envelope, then reversed", nil, func(t *testing.T, l *Ledger, f fixture, envelope int64) store.Transaction {
			row := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 50_000, "2026-08-12", nil)
			if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: row.ID, PurposeID: envelope}); err != nil {
				t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
			}
			return row
		}},
		{"an out corrected to Titipan, then reversed", func(t *testing.T, l *Ledger, f fixture) {
			postEntry(t, l, f.fundID, f.cashID, f.passID, "in", 80_000, "2026-08-01", nil)
		}, func(t *testing.T, l *Ledger, f fixture, _ int64) store.Transaction {
			row := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 20_000, "2026-08-12", nil)
			if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: row.ID, PurposeID: f.passID}); err != nil {
				t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
			}
			return row
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01").PurposeID
			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 300_000, "2026-07-01", nil)
			if tc.prepare != nil {
				tc.prepare(t, l, f)
			}
			before := read(t, l, f, envelope)

			original := tc.do(t, l, f, envelope)
			if during := read(t, l, f, envelope); during == before {
				t.Fatalf("balances did not move when the original was posted (%+v): the case proves nothing", during)
			}
			reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-08-12"))

			if after := read(t, l, f, envelope); after != before {
				t.Errorf("balances after the reversal = %+v, want the pre-original %+v", after, before)
			}
		})
	}
}

// The reversal copies amount, account, member, period and the EFFECTIVE purpose
// from the original and takes nothing from the caller but the note; it goes the
// other way, links the original, and is never given the original's note.
func TestReverseTransactionCopiesTheOriginalAndNeverItsNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("an out goes in, on the same location, with the effective purpose", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		postEntry(t, l, f.fundID, f.bankID, f.passID, "in", 100_000, "2026-08-01", nil)
		note := "beli kopi"
		original := postEntry(t, l, f.fundID, f.bankID, f.mainID, "out", 40_000, "2026-08-12", &note)
		if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: original.ID, PurposeID: f.passID}); err != nil {
			t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
		}

		reversal := reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-08-12"))
		if reversal.Kind != "adjustment" || reversal.Direction != "in" || reversal.Amount != 40_000 {
			t.Errorf("reversal = kind %q direction %q amount %d, want an adjustment going in for 40000", reversal.Kind, reversal.Direction, reversal.Amount)
		}
		if reversal.AccountID != f.bankID {
			t.Errorf("reversal AccountID = %d, want the original's %d", reversal.AccountID, f.bankID)
		}
		if reversal.PurposeID != f.passID {
			t.Errorf("reversal PurposeID = %d, want the EFFECTIVE purpose %d (the stored one is %d)", reversal.PurposeID, f.passID, f.mainID)
		}
		if reversal.ReversesTransactionID == nil || *reversal.ReversesTransactionID != original.ID {
			t.Errorf("reversal ReversesTransactionID = %v, want %d", reversal.ReversesTransactionID, original.ID)
		}
		if reversal.Note != nil {
			t.Errorf("reversal Note = %q, want nil: the original's note is not copied", *reversal.Note)
		}
		if reversal.MemberID != nil || reversal.DuesPeriod != nil {
			t.Errorf("reversal of an unnamed row carries member %v period %v, want neither", reversal.MemberID, reversal.DuesPeriod)
		}
	})

	t.Run("the caller's note is hers, trimmed", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, "2026-08-12", nil)
		typed := "  salah arah  "
		reversal, err := l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: original.ID, Note: &typed})
		if err != nil {
			t.Fatalf("ReverseTransaction() = %v, want no error", err)
		}
		if reversal.Note == nil || *reversal.Note != "salah arah" {
			t.Errorf("reversal Note = %v, want %q", reversal.Note, "salah arah")
		}
	})

	t.Run("a named contribution and a dues payment keep their member and period as they are", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
		gift, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID, Direction: "in",
			Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &f.memberID,
		})
		if err != nil {
			t.Fatalf("PostTransaction() = %v, want no error", err)
		}
		giftReversal := reverseAt(t, l, f.fundID, gift.ID, time.Time{})
		if giftReversal.MemberID == nil || *giftReversal.MemberID != f.memberID || giftReversal.DuesPeriod != nil {
			t.Errorf("contribution reversal member %v period %v, want the member and no period", giftReversal.MemberID, giftReversal.DuesPeriod)
		}

		paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-12",
			Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
		})
		if err != nil {
			t.Fatalf("PostDuesPayments() = %v, want no error", err)
		}
		duesReversal := reverseAt(t, l, f.fundID, paid[0].ID, time.Time{})
		if duesReversal.MemberID == nil || *duesReversal.MemberID != f.memberID ||
			duesReversal.DuesPeriod == nil || *duesReversal.DuesPeriod != "2026-08" {
			t.Errorf("dues reversal member %v period %v, want the member and 2026-08", duesReversal.MemberID, duesReversal.DuesPeriod)
		}
	})
}

// ReversalRefusal is the one rule the write and the page's reversible flag share:
// the table of ADR-042, in its order.
func TestReversalRefusalFollowsTheADRTableInItsOrder(t *testing.T) {
	t.Parallel()
	const today = "2026-10-02"
	ok := ReversalFacts{Kind: "normal", OccurredOn: "2026-09-01"}
	with := func(f func(*ReversalFacts)) ReversalFacts {
		x := ok
		f(&x)
		return x
	}
	cases := []struct {
		name  string
		facts ReversalFacts
		want  error
	}{
		{"a normal row", ok, nil},
		{"a dues payment", with(func(f *ReversalFacts) { f.Kind = "dues" }), nil},
		{"opening", with(func(f *ReversalFacts) { f.Kind = "opening" }), ErrReversalOpening},
		{"a reimbursement payout", with(func(f *ReversalFacts) { f.Kind = "reimbursement" }), ErrReversalReimbursement},
		{"a transfer leg", with(func(f *ReversalFacts) { f.Kind = "transfer" }), ErrReversalTransfer},
		{"a reversal", with(func(f *ReversalFacts) { f.Kind, f.IsReversal = "adjustment", true }), ErrReversalOfReversal},
		{"any other adjustment", with(func(f *ReversalFacts) { f.Kind = "adjustment" }), ErrReversalAdjustment},
		{"a normal row a cek kas line names", with(func(f *ReversalFacts) { f.IsReconciliationFix = true }), ErrReversalReconciliationFix},
		{"already reversed", with(func(f *ReversalFacts) { f.Reversed = true }), ErrAlreadyReversed},
		{"an effective purpose that is a closed envelope", with(func(f *ReversalFacts) { f.EffectiveClosed = true }), ErrIncidentalClosed},
		{"counted and dated today", with(func(f *ReversalFacts) { f.Counted, f.OccurredOn = true, today }), nil},
		{"counted and dated after today", with(func(f *ReversalFacts) { f.Counted, f.OccurredOn = true, "2026-10-03" }), ErrReversalBeforeOriginal},
		{"uncounted and dated after today reverses on its own day", with(func(f *ReversalFacts) { f.OccurredOn = "2026-10-03" }), nil},
		// Order: the kind says what the row is, before anything about its state.
		{"a reversed transfer leg is a transfer leg", with(func(f *ReversalFacts) { f.Kind, f.Reversed = "transfer", true }), ErrReversalTransfer},
		{"a fix that is also reversed is the fix", with(func(f *ReversalFacts) { f.IsReconciliationFix, f.Reversed = true, true }), ErrReversalReconciliationFix},
		{"reversed beats a closed envelope", with(func(f *ReversalFacts) { f.Reversed, f.EffectiveClosed = true, true }), ErrAlreadyReversed},
		{"a closed envelope beats the date assertion", with(func(f *ReversalFacts) {
			f.EffectiveClosed, f.Counted, f.OccurredOn = true, true, "2026-10-03"
		}), ErrIncidentalClosed},
	}
	for _, tc := range cases {
		got := ReversalRefusal(tc.facts, today)
		if !errors.Is(got, tc.want) || (tc.want == nil && got != nil) {
			t.Errorf("%s: ReversalRefusal() = %v, want %v", tc.name, got, tc.want)
		}
	}
	if err := ReversalRefusal(ReversalFacts{Kind: "mystery"}, today); err == nil {
		t.Error("ReversalRefusal(unknown kind) = nil, want a refusal rather than a guessed yes")
	}
}

// One test per refusal code, against real rows. Each refusal writes nothing.
func TestReverseTransactionRefusesWhatItWillNotTake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type world struct {
		l *Ledger
		f fixture
		q *store.Queries
	}
	setup := func(t *testing.T) world {
		t.Helper()
		l := newTestLedger(t)
		return world{l: l, f: newFixture(t, l), q: store.New(l.db)}
	}
	refused := func(t *testing.T, w world, id int64, want error) {
		t.Helper()
		before := transactionCount(t, w.l, w.f.fundID)
		_, err := w.l.at(onTheDay("2026-10-02")).ReverseTransaction(ctx, ReverseTransactionParams{FundID: w.f.fundID, TransactionID: id})
		if !errors.Is(err, want) {
			t.Fatalf("ReverseTransaction(%d) = %v, want an error wrapping %v", id, err, want)
		}
		if after := transactionCount(t, w.l, w.f.fundID); after != before {
			t.Errorf("rows after a refused reversal = %d, want %d: a refusal writes nothing", after, before)
		}
	}

	t.Run("an opening balance", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		opening := postOpeningBalance(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, 100_000, "2026-08-01")
		refused(t, w, opening.ID, ErrReversalOpening)
	})

	t.Run("a reimbursement payout", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 100_000, "2026-08-01", nil)
		claim := createReimbursement(t, w.q, w.f, 30_000, "2026-08-05", nil)
		payout, err := w.l.at(onTheDay("2026-08-10")).SettleReimbursement(ctx, SettleReimbursementParams{
			FundID: w.f.fundID, ReimbursementID: claim.ID, AccountID: w.f.cashID, OccurredOn: "2026-08-10",
		})
		if err != nil {
			t.Fatalf("SettleReimbursement() = %v, want no error", err)
		}
		refused(t, w, payout.ID, ErrReversalReimbursement)
	})

	t.Run("a transfer leg", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 100_000, "2026-08-01", nil)
		transfer, err := w.l.at(onTheDay("2026-08-02")).PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
			FundID: w.f.fundID, PurposeID: w.f.mainID, FromAccountID: w.f.cashID, ToAccountID: w.f.bankID,
			Amount: 40_000, OccurredOn: "2026-08-02",
		})
		if err != nil {
			t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
		}
		for _, leg := range transactionsForTransfer(t, w.q, w.f.fundID, transfer.ID) {
			refused(t, w, leg.ID, ErrReversalTransfer)
		}
	})

	t.Run("a purpose correction's leg", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		row := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "out", 8_000, "2026-08-12", nil)
		correction, err := w.l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: w.f.fundID, TransactionID: row.ID, PurposeID: w.f.passID})
		if err != nil {
			t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
		}
		for _, leg := range transactionsForTransfer(t, w.q, w.f.fundID, correction.ID) {
			refused(t, w, leg.ID, ErrReversalTransfer)
		}
	})

	t.Run("a reversal", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		original := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "out", 8_000, "2026-08-12", nil)
		reversal := reverseAt(t, w.l, w.f.fundID, original.ID, onTheDay("2026-08-12"))
		refused(t, w, reversal.ID, ErrReversalOfReversal)
	})

	t.Run("an adjustment posted alone", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		adjustment, err := w.l.PostTransaction(ctx, PostTransactionParams{
			FundID: w.f.fundID, AccountID: w.f.cashID, PurposeID: w.f.mainID, Direction: "out",
			Amount: 5_000, OccurredOn: "2026-08-12", IsAdjustment: true,
		})
		if err != nil {
			t.Fatalf("PostTransaction(adjustment) = %v, want no error", err)
		}
		refused(t, w, adjustment.ID, ErrReversalAdjustment)
	})

	t.Run("the plug a cek kas line names", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 100_000, "2026-08-01", nil)
		rec, err := w.l.at(onTheDay("2026-08-20")).TakeReconciliation(ctx, TakeReconciliationParams{FundID: w.f.fundID, Counts: []AccountCount{
			{AccountID: w.f.cashID, ActualAmount: 95_000, Resolution: "adjusted",
				Fix: &Fix{PurposeID: w.f.mainID, Direction: "out", Amount: 5_000, OccurredOn: "2026-08-20"}},
			w.f.bankZero(),
		}})
		if err != nil {
			t.Fatalf("TakeReconciliation() = %v, want no error", err)
		}
		refused(t, w, countFixID(t, w.q, rec.ID, w.f.cashID), ErrReversalAdjustment)
	})

	t.Run("an entry a cek kas line names as its fix", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 100_000, "2026-08-01", nil)
		rec, err := w.l.at(onTheDay("2026-08-20")).TakeReconciliation(ctx, TakeReconciliationParams{FundID: w.f.fundID, Counts: []AccountCount{
			{AccountID: w.f.cashID, ActualAmount: 95_000, Resolution: "entry_added",
				Fix: &Fix{PurposeID: w.f.mainID, Direction: "out", Amount: 5_000, OccurredOn: "2026-08-20"}},
			w.f.bankZero(),
		}})
		if err != nil {
			t.Fatalf("TakeReconciliation() = %v, want no error", err)
		}
		refused(t, w, countFixID(t, w.q, rec.ID, w.f.cashID), ErrReversalReconciliationFix)
	})

	t.Run("a row reversed already", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		original := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 8_000, "2026-08-12", nil)
		reverseAt(t, w.l, w.f.fundID, original.ID, onTheDay("2026-08-12"))
		refused(t, w, original.ID, ErrAlreadyReversed)
	})

	t.Run("a closed envelope, read on the effective purpose", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 100_000, "2026-08-01", nil)
		envelope := openTestIncidental(t, w.l, w.f.fundID, "Sunatan", "2026-08-01")
		other := openTestIncidental(t, w.l, w.f.fundID, "Arisan", "2026-08-01")
		closeEnvelope := func(id int64) {
			t.Helper()
			if _, err := w.l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{FundID: w.f.fundID, PurposeID: id, AccountID: w.f.cashID, ClosedOn: "2026-08-20"}); err != nil {
				t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
			}
		}

		// Stored in the envelope, still there: refused once it closes.
		inside := postEntry(t, w.l, w.f.fundID, w.f.cashID, envelope.PurposeID, "in", 20_000, "2026-08-05", nil)
		// Stored in Kas Utama, corrected INTO the envelope: refused by the envelope it now sits in.
		movedIn := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 30_000, "2026-08-06", nil)
		if _, err := w.l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: w.f.fundID, TransactionID: movedIn.ID, PurposeID: other.PurposeID}); err != nil {
			t.Fatalf("PostPurposeCorrection(into) = %v, want no error", err)
		}
		// Stored in the envelope, corrected OUT of it to Kas Utama before it closes:
		// the row sits in an open pos, so it stays reversible.
		movedOut := postEntry(t, w.l, w.f.fundID, w.f.cashID, envelope.PurposeID, "in", 10_000, "2026-08-07", nil)
		if _, err := w.l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: w.f.fundID, TransactionID: movedOut.ID, PurposeID: w.f.mainID}); err != nil {
			t.Fatalf("PostPurposeCorrection(out) = %v, want no error", err)
		}

		closeEnvelope(envelope.PurposeID)
		closeEnvelope(other.PurposeID)
		refused(t, w, inside.ID, ErrIncidentalClosed)
		refused(t, w, movedIn.ID, ErrIncidentalClosed)
		if reversal := reverseAt(t, w.l, w.f.fundID, movedOut.ID, onTheDay("2026-08-25")); reversal.PurposeID != w.f.mainID {
			t.Errorf("reversal of a row corrected out of a closed envelope sits in %d, want Kas Utama %d", reversal.PurposeID, w.f.mainID)
		}
	})

	t.Run("a row that is not in this fund, or not there at all", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		original := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 8_000, "2026-08-12", nil)
		other := newSecondFund(t, w.l)
		for _, id := range []int64{original.ID + 1000, -1} {
			if _, err := w.l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: w.f.fundID, TransactionID: id}); !errors.Is(err, ErrReversalNotFound) {
				t.Errorf("ReverseTransaction(%d) = %v, want ErrReversalNotFound", id, err)
			}
		}
		if _, err := w.l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: other.fundID, TransactionID: original.ID}); !errors.Is(err, ErrReversalNotFound) {
			t.Errorf("ReverseTransaction(another fund's row) = %v, want ErrReversalNotFound", err)
		}
		reverseAt(t, w.l, w.f.fundID, original.ID, time.Time{})
	})

	t.Run("a counted row dated after today", func(t *testing.T) {
		t.Parallel()
		w := setup(t)
		// A row from before the date ceiling: posted when "today" was later.
		ahead := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 8_000, "2026-12-20", nil)
		// Uncounted, it reverses on its own date, ahead of today.
		if reversal := reverseAt(t, w.l, w.f.fundID, ahead.ID, onTheDay("2026-10-02")); reversal.OccurredOn != "2026-12-20" {
			t.Errorf("uncounted reversal OccurredOn = %q, want the original's 2026-12-20", reversal.OccurredOn)
		}

		second := postEntry(t, w.l, w.f.fundID, w.f.cashID, w.f.mainID, "in", 9_000, "2026-12-21", nil)
		countEverything(t, w.l, w.f.fundID, w.f.cashID, w.f.bankID)
		refused(t, w, second.ID, ErrReversalBeforeOriginal)
	})
}

// countFixID is the transaction a snapshot's line for one location names as its fix.
func countFixID(t *testing.T, q *store.Queries, reconciliationID, accountID int64) int64 {
	t.Helper()
	lines, err := q.ListReconciliationLines(context.Background(), reconciliationID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	line := lineFor(t, lines, accountID)
	if line.AdjustmentTransactionID == nil {
		t.Fatalf("line for account %d names no fix", accountID)
	}
	return *line.AdjustmentTransactionID
}

// The date is the ledger's (ADR-042): the original's own day if no cek kas has
// counted it since, otherwise today in the fund's zone from the injected clock.
func TestReverseTransactionPicksTheDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("an uncounted row is reversed on its own day, whatever the clock says", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		normal := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 8_000, "2026-08-12", nil)
		paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-14",
			Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
		})
		if err != nil {
			t.Fatalf("PostDuesPayments() = %v, want no error", err)
		}
		if got := reverseAt(t, l, f.fundID, normal.ID, onTheDay("2026-10-02")).OccurredOn; got != "2026-08-12" {
			t.Errorf("normal reversal OccurredOn = %q, want the original's 2026-08-12", got)
		}
		if got := reverseAt(t, l, f.fundID, paid[0].ID, onTheDay("2026-10-02")).OccurredOn; got != "2026-08-14" {
			t.Errorf("dues reversal OccurredOn = %q, want the payment's own 2026-08-14", got)
		}
	})

	t.Run("a counted row is reversed today in the fund's zone", func(t *testing.T) {
		t.Parallel()
		// 22:30 WIB is the instant the three zones disagree about: still the 10th for
		// a WIB or WITA fund (23:30), already the 11th for a WIT one (00:30).
		now := time.Date(2026, 9, 10, 22, 30, 0, 0, wib)
		for zone, want := range map[string]string{tz.Jakarta: "2026-09-10", tz.Makassar: "2026-09-10", tz.Jayapura: "2026-09-11"} {
			l := newTestLedger(t)
			f := fundInZone(t, l, zone)
			original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 8_000, "2026-08-12", nil)
			countEverything(t, l, f.fundID, f.cashID, f.bankID)
			if got := reverseAt(t, l, f.fundID, original.ID, now).OccurredOn; got != want {
				t.Errorf("%s fund: counted reversal OccurredOn = %q, want today %q", zone, got, want)
			}
		}
	})

	t.Run("the row at the cutoff is counted, the next id is not", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		atCutoff := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 8_000, "2026-08-12", nil)
		countEverything(t, l, f.fundID, f.cashID, f.bankID)
		next := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 9_000, "2026-08-13", nil)

		if got := reverseAt(t, l, f.fundID, atCutoff.ID, onTheDay("2026-10-02")).OccurredOn; got != "2026-10-02" {
			t.Errorf("reversal of the row at the cutoff OccurredOn = %q, want today 2026-10-02", got)
		}
		if got := reverseAt(t, l, f.fundID, next.ID, onTheDay("2026-10-02")).OccurredOn; got != "2026-08-13" {
			t.Errorf("reversal of the row after the cutoff OccurredOn = %q, want its own 2026-08-13", got)
		}
	})

	t.Run("a count of an empty ledger, which has no cutoff, counts nothing", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		rec := countEverything(t, l, f.fundID, f.cashID, f.bankID)
		if rec.ThroughTransactionID != nil {
			t.Fatalf("the count of an empty ledger has cutoff %v, want NULL", *rec.ThroughTransactionID)
		}
		original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 8_000, "2026-08-12", nil)
		if got := reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-10-02")).OccurredOn; got != "2026-08-12" {
			t.Errorf("reversal after a NULL-cutoff count OccurredOn = %q, want the original's 2026-08-12", got)
		}
	})

	t.Run("a count taken before the row was posted does not cover it, a later one does", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 1_000, "2026-07-01", nil)
		countEverything(t, l, f.fundID, f.cashID, f.bankID)
		original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 8_000, "2026-08-12", nil)
		countEverything(t, l, f.fundID, f.cashID, f.bankID)
		if got := reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-10-02")).OccurredOn; got != "2026-10-02" {
			t.Errorf("reversal OccurredOn = %q, want today: the second count covers the row", got)
		}
	})
}

// Correct, then reverse: allowed (the reversal copies the effective purpose).
// Reverse, then correct: refused with its own code, and writes nothing. A reversal
// is never itself re-tagged.
func TestPurposeCorrectionAndReversalOfTheSameRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reverse, then correct is refused", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 100, "2026-08-12", nil)
		reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-08-12"))

		before := transactionCount(t, l, f.fundID)
		_, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: original.ID, PurposeID: f.passID})
		if !errors.Is(err, ErrPurposeCorrectionAlreadyReversed) {
			t.Fatalf("PostPurposeCorrection(a reversed row) = %v, want ErrPurposeCorrectionAlreadyReversed", err)
		}
		if after := transactionCount(t, l, f.fundID); after != before {
			t.Errorf("rows after the refused correction = %d, want %d", after, before)
		}
	})

	t.Run("correct, then reverse nets both pos to zero", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 100, "2026-08-12", nil)
		if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: original.ID, PurposeID: f.passID}); err != nil {
			t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
		}
		reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-08-12"))
		for name, id := range map[string]int64{"Kas Utama": f.mainID, "Titipan": f.passID} {
			if got, err := l.PurposeBalance(ctx, f.fundID, id); err != nil || got != 0 {
				t.Errorf("%s = %d (%v), want 0", name, got, err)
			}
		}
	})

	t.Run("a reversal of an ordinary row is never re-tagged", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		f := newFixture(t, l)
		original := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 100, "2026-08-12", nil)
		reversal := reverseAt(t, l, f.fundID, original.ID, onTheDay("2026-08-12"))
		_, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: reversal.ID, PurposeID: f.passID})
		if !errors.Is(err, ErrPurposeCorrectionReversal) {
			t.Fatalf("PostPurposeCorrection(a reversal of an ordinary row) = %v, want ErrPurposeCorrectionReversal", err)
		}
	})
}

// The envelope's collected and disbursed read a reversal as a minus on the
// original's bucket, so collected minus disbursed is the balance even when a
// purpose correction put the two halves of a pair in two pos (ADR-042). Dropping
// the pair whole would read A -100 and B +100 here.
func TestIncidentalActivityTotalsNetAReversalAgainstItsOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	a := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
	b := openTestIncidental(t, l, f.fundID, "Arisan", "2026-08-01")

	post := func(purpose int64, direction string, amount money.Amount, member *int64) store.Transaction {
		t.Helper()
		row, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: purpose, Direction: direction,
			Amount: amount, OccurredOn: "2026-08-12", MemberID: member,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d) = %v, want no error", direction, amount, err)
		}
		return row
	}
	later := onTheDay("2026-09-01")

	post(a.PurposeID, "in", 300, nil)
	post(a.PurposeID, "in", 200, &f.memberID)
	post(a.PurposeID, "out", 200, nil)
	sameIn := post(a.PurposeID, "in", 100, nil)
	sameOut := post(a.PurposeID, "out", 50, nil)
	named := post(a.PurposeID, "in", 400, &f.memberID)
	crossing := post(a.PurposeID, "in", 70, nil)
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: crossing.ID, PurposeID: b.PurposeID}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}
	// A cek kas counts every row so far, so each reversal is dated today (1 September)
	// and a pair straddles a month as well as, for the last, a pos.
	countEverything(t, l, f.fundID, f.cashID, f.bankID)
	reverseAt(t, l, f.fundID, sameIn.ID, later)   // same pos, unnamed in
	reverseAt(t, l, f.fundID, sameOut.ID, later)  // same pos, out
	reverseAt(t, l, f.fundID, named.ID, later)    // same pos, named
	reverseAt(t, l, f.fundID, crossing.ID, later) // across two pos

	for _, tc := range []struct {
		name                          string
		purpose                       int64
		collected, disbursed, wantBal money.Amount
	}{
		{"Sunatan", a.PurposeID, 500, 200, 300},
		{"Arisan", b.PurposeID, 0, 0, 0},
	} {
		d, err := l.GetIncidentalDetail(ctx, f.fundID, tc.purpose)
		if err != nil {
			t.Fatalf("GetIncidentalDetail(%s) = %v, want no error", tc.name, err)
		}
		if d.Collected != tc.collected || d.Disbursed != tc.disbursed || d.Balance != tc.wantBal {
			t.Errorf("%s: collected %d, disbursed %d, balance %d; want %d, %d, %d",
				tc.name, d.Collected, d.Disbursed, d.Balance, tc.collected, tc.disbursed, tc.wantBal)
		}
		// The equation that makes the figures mean something.
		if d.Collected-d.Disbursed != d.Balance {
			t.Errorf("%s: collected %d - disbursed %d != balance %d", tc.name, d.Collected, d.Disbursed, d.Balance)
		}
	}

	// A bound before the reversals: only the originals are in, each half of a pair
	// by its own date, so the figures are the originals' alone (and the correction's
	// leg, dated its original's day, already takes the 70 back out).
	totals, err := store.New(l.db).IncidentalActivityTotals(ctx, store.IncidentalActivityTotalsParams{
		FundID: f.fundID, PurposeID: a.PurposeID, Through: strPtr("2026-08-31"),
	})
	if err != nil {
		t.Fatalf("IncidentalActivityTotals(through August) = %v, want no error", err)
	}
	// 300 + 200 + 100 + 400 + 70 collected, less the correction's -70 leg; 200 + 50 spent.
	if totals.CollectedAmount != 1_000 || totals.DisbursedAmount != 250 {
		t.Errorf("through August: collected %d, disbursed %d; want 1000, 250", totals.CollectedAmount, totals.DisbursedAmount)
	}
}

// The Iuran history is dues payments and their reversals, nothing else: a named
// contribution's reversal (member set, no period) must not leak into it
// (ADR-042 narrows the query to dues reversals).
func TestDuesPaymentHistoryDoesNotCarryOtherReversals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")

	paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	duesReversal := reverseAt(t, l, f.fundID, paid[0].ID, time.Time{})

	gift, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID, Direction: "in",
		Amount: 15_000, OccurredOn: "2026-08-13", MemberID: &f.memberID,
	})
	if err != nil {
		t.Fatalf("PostTransaction(named contribution) = %v, want no error", err)
	}
	reverseAt(t, l, f.fundID, gift.ID, time.Time{})
	reverseAt(t, l, f.fundID, postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 4_000, "2026-08-14", nil).ID, time.Time{})

	rows, err := store.New(l.db).ListDuesPaymentsPage(ctx, store.ListDuesPaymentsPageParams{FundID: f.fundID, PageLimit: 50})
	if err != nil {
		t.Fatalf("ListDuesPaymentsPage() = %v, want no error", err)
	}
	if len(rows) != 2 {
		t.Fatalf("history has %d rows, want the payment and its reversal only: %+v", len(rows), rows)
	}
	ids := map[int64]bool{rows[0].ID: true, rows[1].ID: true}
	if !ids[paid[0].ID] || !ids[duesReversal.ID] {
		t.Errorf("history rows = %v, want the payment %d and its reversal %d", ids, paid[0].ID, duesReversal.ID)
	}
}
