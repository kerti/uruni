package ledger

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// moveWorld is the fixture plus what a purpose move needs to be interesting:
// Kas Utama holding 800.000 (500.000 on cash, 300.000 on the bank), the
// envelope holding 200.000 (cash), and Titipan holding 75.000 (bank).
type moveWorld struct {
	fixture
	envelopeID int64 // an open envelope, 200.000
}

func newMoveWorld(t *testing.T, l *Ledger) moveWorld {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t, l)
	// A real envelope (a purpose with its incidental row), so it can be
	// closed; the fixture's bare incidental purpose has no such row.
	w := moveWorld{fixture: f, envelopeID: openTestIncidental(t, l, f.fundID, "Bereavement", "2026-09-01").PurposeID}
	for _, in := range []struct {
		account, purpose int64
		amount           money.Amount
	}{
		{f.cashID, f.mainID, 500_000},
		{f.bankID, f.mainID, 300_000},
		{f.cashID, w.envelopeID, 200_000},
		{f.bankID, f.passID, 75_000},
	} {
		if _, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: in.account, PurposeID: in.purpose,
			Direction: "in", Amount: in.amount, OccurredOn: "2026-09-01",
		}); err != nil {
			t.Fatalf("seeding %d into purpose %d = %v, want no error", in.amount, in.purpose, err)
		}
	}
	return w
}

// snapshot is every balance a purpose move must or must not change.
type snapshot struct {
	fund, cash, bank, main, envelope, pass money.Amount
}

func (w moveWorld) snapshot(t *testing.T, l *Ledger) snapshot {
	t.Helper()
	ctx := context.Background()
	get := func(name string, v money.Amount, err error) money.Amount {
		t.Helper()
		if err != nil {
			t.Fatalf("reading %s balance = %v, want no error", name, err)
		}
		return v
	}
	var s snapshot
	var v money.Amount
	var err error
	v, err = l.FundBalance(ctx, w.fundID)
	s.fund = get("fund", v, err)
	v, err = l.AccountBalance(ctx, w.fundID, w.cashID)
	s.cash = get("cash", v, err)
	v, err = l.AccountBalance(ctx, w.fundID, w.bankID)
	s.bank = get("bank", v, err)
	v, err = l.PurposeBalance(ctx, w.fundID, w.mainID)
	s.main = get("main", v, err)
	v, err = l.PurposeBalance(ctx, w.fundID, w.envelopeID)
	s.envelope = get("envelope", v, err)
	v, err = l.PurposeBalance(ctx, w.fundID, w.passID)
	s.pass = get("pass-through", v, err)
	return s
}

func transferCount(t *testing.T, l *Ledger, fundID int64) int {
	t.Helper()
	rows, err := store.New(l.db).ListTransfersByFund(context.Background(), fundID)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	return len(rows)
}

func (w moveWorld) move(from, to int64, amount money.Amount) PostPurposeMoveParams {
	return PostPurposeMoveParams{
		FundID: w.fundID, FromPurposeID: from, ToPurposeID: to, AccountID: w.cashID,
		Amount: amount, OccurredOn: "2026-09-10",
	}
}

// The crux (ADR-036, rule 2): a move shifts exactly the two purpose
// balances by exactly the amount and nothing else - not the fund, not either
// account, not Titipan.
func TestPostPurposeMoveShiftsOnlyThePurposeBalances(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		from, to func(w moveWorld) int64
		amount   money.Amount
	}{
		{"Kas Utama to an envelope", func(w moveWorld) int64 { return w.mainID }, func(w moveWorld) int64 { return w.envelopeID }, 120_000},
		{"an envelope back to Kas Utama", func(w moveWorld) int64 { return w.envelopeID }, func(w moveWorld) int64 { return w.mainID }, 80_000},
		{"one rupiah", func(w moveWorld) int64 { return w.mainID }, func(w moveWorld) int64 { return w.envelopeID }, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			w := newMoveWorld(t, l)
			ctx := context.Background()
			from, to := tc.from(w), tc.to(w)

			before := w.snapshot(t, l)
			if _, err := l.PostPurposeMove(ctx, w.move(from, to, tc.amount)); err != nil {
				t.Fatalf("PostPurposeMove() = %v, want no error", err)
			}
			after := w.snapshot(t, l)

			if after.fund != before.fund {
				t.Errorf("fund balance %d -> %d, want identical - nothing left the fund", before.fund, after.fund)
			}
			if after.cash != before.cash || after.bank != before.bank {
				t.Errorf("account balances cash %d -> %d, bank %d -> %d, want identical", before.cash, after.cash, before.bank, after.bank)
			}
			if after.pass != before.pass {
				t.Errorf("pass-through balance %d -> %d, want identical - Titipan is never touched", before.pass, after.pass)
			}
			shift := map[int64]money.Amount{w.mainID: after.main - before.main, w.envelopeID: after.envelope - before.envelope}
			if shift[from] != -tc.amount {
				t.Errorf("source purpose shifted by %d, want %d", shift[from], -tc.amount)
			}
			if shift[to] != tc.amount {
				t.Errorf("target purpose shifted by %d, want %d", shift[to], tc.amount)
			}
		})
	}
}

// One envelope into another, both open: the third direction ADR-036 allows.
func TestPostPurposeMoveBetweenTwoEnvelopes(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()
	other := openTestIncidental(t, l, w.fundID, "Hospital visit", "2026-09-02")

	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, other.PurposeID, 50_000)); err != nil {
		t.Fatalf("PostPurposeMove(envelope -> envelope) = %v, want no error", err)
	}
	got, _ := l.PurposeBalance(ctx, w.fundID, w.envelopeID)
	gotOther, _ := l.PurposeBalance(ctx, w.fundID, other.PurposeID)
	if got != 150_000 || gotOther != 50_000 {
		t.Errorf("balances = %d and %d, want 150000 and 50000", got, gotOther)
	}
}

// The pair's own shape: one reclass_purpose transfer reading 'allocation',
// linked to no correction, whose 'out' leg is at the source and 'in' leg at
// the target, both on the chosen account with the same amount, date and note.
func TestPostPurposeMoveWritesAnAllocationPair(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	p := w.move(w.mainID, w.envelopeID, 120_000)
	p.AccountID = w.bankID
	note := "  Bereavement  "
	p.Note = &note
	created, err := l.PostPurposeMove(ctx, p)
	if err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}

	stored, err := q.GetTransfer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTransfer() = %v, want no error", err)
	}
	if stored.Kind != "reclass_purpose" {
		t.Errorf("Kind = %q, want reclass_purpose", stored.Kind)
	}
	if stored.Reason == nil || *stored.Reason != "allocation" {
		t.Errorf("Reason = %v, want \"allocation\"", stored.Reason)
	}
	if stored.CorrectsTransactionID != nil {
		t.Errorf("CorrectsTransactionID = %v, want nil - an allocation corrects nothing", stored.CorrectsTransactionID)
	}

	legs := transactionsForTransfer(t, q, w.fundID, created.ID)
	if len(legs) != 2 {
		t.Fatalf("transfer has %d legs, want 2", len(legs))
	}
	out, in := legByDirection(t, legs, "out"), legByDirection(t, legs, "in")
	if out.PurposeID != w.mainID || in.PurposeID != w.envelopeID {
		t.Errorf("legs out at purpose %d, in at purpose %d, want %d and %d", out.PurposeID, in.PurposeID, w.mainID, w.envelopeID)
	}
	for _, leg := range legs {
		if leg.AccountID != w.bankID {
			t.Errorf("%s leg on account %d, want the chosen account %d", leg.Direction, leg.AccountID, w.bankID)
		}
		if leg.Amount != 120_000 || leg.OccurredOn != "2026-09-10" || leg.Kind != "transfer" {
			t.Errorf("%s leg = amount %d on %s kind %s, want 120000 on 2026-09-10 kind transfer", leg.Direction, leg.Amount, leg.OccurredOn, leg.Kind)
		}
		if leg.Note == nil || *leg.Note != "Bereavement" {
			t.Errorf("%s leg note = %v, want the trimmed note on both legs", leg.Direction, leg.Note)
		}
	}
}

// Boundary: the source may give exactly what it holds, down to zero, and not
// one rupiah more. The comparison is integer-exact, no tolerance (ADR-015).
func TestPostPurposeMoveSourceBalanceBoundary(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()

	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, w.mainID, 200_001)); !errors.Is(err, ErrPurposeMoveInsufficient) {
		t.Fatalf("PostPurposeMove(balance + 1) = %v, want ErrPurposeMoveInsufficient", err)
	}
	if transferCount(t, l, w.fundID) != 0 {
		t.Error("a refused move left a transfer behind")
	}

	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, w.mainID, 200_000)); err != nil {
		t.Fatalf("PostPurposeMove(exactly the balance) = %v, want no error", err)
	}
	if got, _ := l.PurposeBalance(ctx, w.fundID, w.envelopeID); got != 0 {
		t.Errorf("source balance = %d, want 0 after giving all of it", got)
	}

	// Now empty: the smallest move is refused.
	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, w.mainID, 1)); !errors.Is(err, ErrPurposeMoveInsufficient) {
		t.Errorf("PostPurposeMove(1 from an empty purpose) = %v, want ErrPurposeMoveInsufficient", err)
	}
}

// The balance is the ledger sum, so an adjusting entry that lowered it
// lowers what can be given - and one that raised it raises it. A purpose
// already below zero can give nothing.
func TestPostPurposeMoveReadsTheBalanceThroughAdjustingEntries(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()

	// A reconciliation-style adjustment out of the envelope: 200.000 -> 150.000.
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.cashID, PurposeID: w.envelopeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-09-02", IsAdjustment: true,
	}); err != nil {
		t.Fatalf("posting adjustment = %v, want no error", err)
	}
	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, w.mainID, 150_001)); !errors.Is(err, ErrPurposeMoveInsufficient) {
		t.Errorf("PostPurposeMove(150001) = %v, want ErrPurposeMoveInsufficient after the adjustment", err)
	}
	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, w.mainID, 150_000)); err != nil {
		t.Errorf("PostPurposeMove(150000) = %v, want no error", err)
	}

	// Overspend an envelope below zero: a negative source can give nothing.
	l2 := newTestLedger(t)
	w2 := newMoveWorld(t, l2)
	if _, err := l2.PostTransaction(ctx, PostTransactionParams{
		FundID: w2.fundID, AccountID: w2.cashID, PurposeID: w2.envelopeID,
		Direction: "out", Amount: 260_000, OccurredOn: "2026-09-02",
	}); err != nil {
		t.Fatalf("overspending the envelope = %v, want no error", err)
	}
	if bal, _ := l2.PurposeBalance(ctx, w2.fundID, w2.envelopeID); bal != -60_000 {
		t.Fatalf("envelope balance = %d, want -60000", bal)
	}
	if _, err := l2.PostPurposeMove(ctx, w2.move(w2.envelopeID, w2.mainID, 1)); !errors.Is(err, ErrPurposeMoveInsufficient) {
		t.Errorf("PostPurposeMove(1 from a negative purpose) = %v, want ErrPurposeMoveInsufficient", err)
	}
	// Moving INTO it is fine, and is how it gets covered.
	if _, err := l2.PostPurposeMove(ctx, w2.move(w2.mainID, w2.envelopeID, 60_000)); err != nil {
		t.Errorf("PostPurposeMove(into a negative purpose) = %v, want no error", err)
	}
}

// Amounts at the edge of int64 are refused as insufficient, never wrapped
// into something postable.
func TestPostPurposeMoveRefusesAnAmountBeyondAnyBalance(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()
	if _, err := l.PostPurposeMove(ctx, w.move(w.mainID, w.envelopeID, money.Amount(math.MaxInt64))); !errors.Is(err, ErrPurposeMoveInsufficient) {
		t.Errorf("PostPurposeMove(MaxInt64) = %v, want ErrPurposeMoveInsufficient", err)
	}
}

func TestPostPurposeMoveRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name  string
		build func(t *testing.T, l *Ledger, w moveWorld) PostPurposeMoveParams
		want  error
	}{
		{"zero amount", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.mainID, w.envelopeID, 0)
		}, ErrInvalidArgument},
		{"negative amount", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.mainID, w.envelopeID, -1)
		}, ErrInvalidArgument},
		{"bad date", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			p := w.move(w.mainID, w.envelopeID, 1_000)
			p.OccurredOn = "2026-02-30"
			return p
		}, ErrInvalidArgument},
		{"same purpose", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.mainID, w.mainID, 1_000)
		}, ErrPurposeMoveSamePurpose},
		{"pass-through as source", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.passID, w.mainID, 1_000)
		}, ErrPurposeMovePassThrough},
		{"pass-through as target", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.mainID, w.passID, 1_000)
		}, ErrPurposeMovePassThrough},
		{"pass-through into an envelope", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.passID, w.envelopeID, 1_000)
		}, ErrPurposeMovePassThrough},
		{"closed envelope as source", func(t *testing.T, l *Ledger, w moveWorld) PostPurposeMoveParams {
			closeEnvelope(t, l, w, w.envelopeID)
			return w.move(w.envelopeID, w.mainID, 1_000)
		}, ErrPurposeMoveClosed},
		{"closed envelope as target", func(t *testing.T, l *Ledger, w moveWorld) PostPurposeMoveParams {
			closeEnvelope(t, l, w, w.envelopeID)
			return w.move(w.mainID, w.envelopeID, 1_000)
		}, ErrPurposeMoveClosed},
		{"unknown source purpose", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(999_999, w.mainID, 1_000)
		}, ErrPurposeMoveUnknownPurpose},
		{"unknown target purpose", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.mainID, 999_999, 1_000)
		}, ErrPurposeMoveUnknownPurpose},
		{"another fund's purpose", func(t *testing.T, l *Ledger, w moveWorld) PostPurposeMoveParams {
			other := moveSecondFund(t, l)
			return w.move(w.mainID, other.mainID, 1_000)
		}, ErrPurposeMoveUnknownPurpose},
		{"another fund's account", func(t *testing.T, l *Ledger, w moveWorld) PostPurposeMoveParams {
			other := moveSecondFund(t, l)
			p := w.move(w.mainID, w.envelopeID, 1_000)
			p.AccountID = other.cashID
			return p
		}, ErrPurposeMoveUnknownAccount},
		{"unknown account", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			p := w.move(w.mainID, w.envelopeID, 1_000)
			p.AccountID = 999_999
			return p
		}, ErrPurposeMoveUnknownAccount},
		{"inactive account", func(t *testing.T, l *Ledger, w moveWorld) PostPurposeMoveParams {
			retired := "2026-09-05"
			if _, err := store.New(l.db).UpdateAccount(ctx, store.UpdateAccountParams{
				ID: w.bankID, SetInactiveOn: 1, InactiveOn: &retired,
			}); err != nil {
				t.Fatalf("retiring the account = %v, want no error", err)
			}
			p := w.move(w.mainID, w.envelopeID, 1_000)
			p.AccountID = w.bankID
			return p
		}, ErrPurposeMoveAccountInactive},
		{"more than the source holds", func(_ *testing.T, _ *Ledger, w moveWorld) PostPurposeMoveParams {
			return w.move(w.envelopeID, w.mainID, 200_001)
		}, ErrPurposeMoveInsufficient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			w := newMoveWorld(t, l)
			p := tc.build(t, l, w)

			before := w.snapshot(t, l)
			transfersBefore := transferCount(t, l, w.fundID)

			if _, err := l.PostPurposeMove(ctx, p); !errors.Is(err, tc.want) {
				t.Fatalf("PostPurposeMove() = %v, want %v", err, tc.want)
			}
			if got := w.snapshot(t, l); got != before {
				t.Errorf("a refused move changed balances: %+v -> %+v", before, got)
			}
			if got := transferCount(t, l, w.fundID); got != transfersBefore {
				t.Errorf("a refused move left %d transfers, want %d", got, transfersBefore)
			}
		})
	}
}

func closeEnvelope(t *testing.T, l *Ledger, w moveWorld, purposeID int64) {
	t.Helper()
	if _, err := l.CloseIncidentalAndRoll(context.Background(), CloseIncidentalAndRollParams{
		FundID: w.fundID, PurposeID: purposeID, AccountID: w.cashID, ClosedOn: "2026-09-05",
	}); err != nil {
		t.Fatalf("closing the envelope = %v, want no error", err)
	}
}

// moveSecondFund is a second fund built by hand, with its own money, so a leak across the
// boundary would show as a balance moving.
type moveOtherFund struct{ fundID, cashID, mainID int64 }

func moveSecondFund(t *testing.T, l *Ledger) moveOtherFund {
	t.Helper()
	q := store.New(l.db)
	fund, err := q.CreateFund(context.Background(), store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	o := moveOtherFund{fundID: fund.ID}
	o.cashID = createAccount(t, q, fund.ID, "cash", "Cash")
	o.mainID = createPurpose(t, q, fund.ID, "main", "Other main")
	if _, err := l.PostTransaction(context.Background(), PostTransactionParams{
		FundID: fund.ID, AccountID: o.cashID, PurposeID: o.mainID,
		Direction: "in", Amount: 999_000, OccurredOn: "2026-09-01",
	}); err != nil {
		t.Fatalf("seeding the second fund = %v, want no error", err)
	}
	return o
}

// A move in one fund is invisible to another: its balances do not shift,
// and it has no transfers of its own.
func TestPostPurposeMoveLeavesASecondFundUntouched(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	other := moveSecondFund(t, l)
	ctx := context.Background()

	if _, err := l.PostPurposeMove(ctx, w.move(w.mainID, w.envelopeID, 100_000)); err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}
	if got, _ := l.FundBalance(ctx, other.fundID); got != 999_000 {
		t.Errorf("second fund balance = %d, want 999000", got)
	}
	if got, _ := l.PurposeBalance(ctx, other.fundID, other.mainID); got != 999_000 {
		t.Errorf("second fund main balance = %d, want 999000", got)
	}
	if n := transferCount(t, l, other.fundID); n != 0 {
		t.Errorf("second fund has %d transfers, want 0", n)
	}
}

// Reconciliation is untouched (ADR-036): per account, recorded balance is the
// same before and after, so the counted cash still matches.
func TestPostPurposeMoveKeepsReconciliationFiguresIdentical(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()
	p := w.move(w.mainID, w.envelopeID, 123_456)
	p.AccountID = w.bankID
	if _, err := l.PostPurposeMove(ctx, p); err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}
	if got, _ := l.AccountBalance(ctx, w.fundID, w.bankID); got != 375_000 {
		t.Errorf("bank balance = %d, want 375000 (300000 main + 75000 Titipan), unmoved", got)
	}
	if got, _ := l.AccountBalance(ctx, w.fundID, w.cashID); got != 700_000 {
		t.Errorf("cash balance = %d, want 700000, unmoved", got)
	}
}

// reasonsByKind lists every transfer's reason in a fund, in id order.
func reasonsByKind(t *testing.T, l *Ledger, fundID int64) []*string {
	t.Helper()
	rows, err := store.New(l.db).ListTransfersByFund(context.Background(), fundID)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	out := make([]*string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Reason)
	}
	return out
}

// CloseIncidentalAndRoll labels both of its directions 'roll' (ADR-036), the
// leftover going out and a shortfall being covered.
func TestCloseIncidentalAndRollWritesReasonRoll(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		posting string
		amount  money.Amount
	}{
		{"leftover rolls out", "in", 70_000},
		{"shortfall is covered", "out", 40_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()
			env := openTestIncidental(t, l, f.fundID, "Occasion", "2026-08-01")
			if tc.posting == "out" {
				if _, err := l.PostTransaction(ctx, PostTransactionParams{
					FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
					Direction: "in", Amount: 500_000, OccurredOn: "2026-08-01",
				}); err != nil {
					t.Fatalf("seeding main = %v, want no error", err)
				}
			}
			if _, err := l.PostTransaction(ctx, PostTransactionParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: env.PurposeID,
				Direction: tc.posting, Amount: tc.amount, OccurredOn: "2026-08-02",
			}); err != nil {
				t.Fatalf("PostTransaction() = %v, want no error", err)
			}
			if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
				FundID: f.fundID, PurposeID: env.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
			}); err != nil {
				t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
			}
			reasons := reasonsByKind(t, l, f.fundID)
			if len(reasons) != 1 || reasons[0] == nil || *reasons[0] != "roll" {
				t.Errorf("transfer reasons = %v, want exactly one \"roll\"", reasons)
			}
		})
	}
}

// Every other pair keeps NULL: a correction is labelled by its link, a
// between_accounts pair has no reason to give.
func TestOtherTransfersKeepANullReason(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()

	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: w.fundID, PurposeID: w.mainID, FromAccountID: w.cashID, ToAccountID: w.bankID,
		Amount: 10_000, OccurredOn: "2026-09-03",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}
	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.cashID, PurposeID: w.mainID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-04",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: w.fundID, TransactionID: posted.ID, PurposeID: w.envelopeID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}
	reasons := reasonsByKind(t, l, w.fundID)
	if len(reasons) != 2 {
		t.Fatalf("got %d transfers, want 2", len(reasons))
	}
	for i, r := range reasons {
		if r != nil {
			t.Errorf("transfer %d reason = %q, want NULL", i, *r)
		}
	}
}

// The schema is the real guarantee behind the column (ADR-036): a reason
// belongs only on a reclass_purpose pair that corrects nothing, and only
// from the two values. Written around the ledger, straight at the table.
func TestTransferReasonCheckConstraint(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()
	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.cashID, PurposeID: w.mainID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-04",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	for _, tc := range []struct {
		name     string
		kind     string
		corrects *int64
		reason   *string
		ok       bool
	}{
		{"reclass roll", "reclass_purpose", nil, strPtr("roll"), true},
		{"reclass allocation", "reclass_purpose", nil, strPtr("allocation"), true},
		{"reclass NULL (pre-column)", "reclass_purpose", nil, nil, true},
		{"unknown reason", "reclass_purpose", nil, strPtr("gift"), false},
		{"empty reason", "reclass_purpose", nil, strPtr(""), false},
		{"between_accounts with a reason", "between_accounts", nil, strPtr("roll"), false},
		{"a correction with a reason", "reclass_purpose", &posted.ID, strPtr("allocation"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.New(l.db).CreateTransfer(ctx, store.CreateTransferParams{
				FundID: w.fundID, Kind: tc.kind, CorrectsTransactionID: tc.corrects, Reason: tc.reason, CreatedAt: 1,
			})
			if tc.ok && err != nil {
				t.Errorf("CreateTransfer() = %v, want no error", err)
			}
			if !tc.ok && err == nil {
				t.Error("CreateTransfer() = nil, want the CHECK constraint to refuse it")
			}
		})
	}
}

// An allocation counts toward the envelope's Terkumpul - in adds, out takes
// back - and never toward Terpakai, so collected minus disbursed keeps
// equalling the balance. The roll that closes the envelope still counts as
// neither (#215).
func TestPurposeMoveCountsAsCollectedNeverDisbursed(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newMoveWorld(t, l)
	ctx := context.Background()

	check := func(stage string, wantCollected, wantDisbursed money.Amount) {
		t.Helper()
		d, err := l.GetIncidentalDetail(ctx, w.fundID, w.envelopeID)
		if err != nil {
			t.Fatalf("%s: GetIncidentalDetail() = %v, want no error", stage, err)
		}
		if d.Collected != wantCollected || d.Disbursed != wantDisbursed {
			t.Errorf("%s: collected/disbursed = %d/%d, want %d/%d", stage, d.Collected, d.Disbursed, wantCollected, wantDisbursed)
		}
	}

	if _, err := l.PostPurposeMove(ctx, w.move(w.mainID, w.envelopeID, 100_000)); err != nil {
		t.Fatalf("PostPurposeMove(main -> envelope) = %v, want no error", err)
	}
	check("after allocating in", 300_000, 0)

	if _, err := l.PostPurposeMove(ctx, w.move(w.envelopeID, w.mainID, 50_000)); err != nil {
		t.Fatalf("PostPurposeMove(envelope -> main) = %v, want no error", err)
	}
	check("after allocating out", 250_000, 0)
	if got, _ := l.PurposeBalance(ctx, w.fundID, w.envelopeID); got != 250_000 {
		t.Errorf("envelope balance = %d, want 250000 (collected minus disbursed)", got)
	}

	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: w.fundID, PurposeID: w.envelopeID, AccountID: w.cashID, ClosedOn: "2026-09-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	check("after closing", 250_000, 0)
}
