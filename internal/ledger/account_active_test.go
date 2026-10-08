package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// retiredWorld is the fixture fund with money in it and its bank retired
// (#444). Cash holds 500.000 in Kas Utama; the bank holds 200.000 in Kas
// Utama and is retired on retiredOn. An open envelope holds 50.000 of
// contributions, on cash.
//
// The bank still holding money is the point: a retired location is not
// necessarily empty (nothing requires a zero balance to retire), and every
// refusal below has to leave those integers exactly where they were.
type retiredWorld struct {
	fixture
	l          *Ledger
	envelopeID int64 // a real envelope (purpose + incidental row), so it can be closed
}

const retiredOn = "2026-09-05"

func newRetiredWorld(t *testing.T, l *Ledger) retiredWorld {
	t.Helper()
	f := newFixture(t, l)
	ctx := context.Background()
	envelopeID := openTestIncidental(t, l, f.fundID, "Bereavement", "2026-09-01").PurposeID

	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 500_000, "2026-09-01")
	postOpeningBalance(t, l, f.fundID, f.bankID, f.mainID, 200_000, "2026-09-01")
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelopeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-09-02",
	}); err != nil {
		t.Fatalf("seeding the envelope's contribution = %v, want no error", err)
	}

	w := retiredWorld{fixture: f, l: l, envelopeID: envelopeID}
	w.setRetired(t, f.bankID, retiredOn)
	return w
}

func (w retiredWorld) setRetired(t *testing.T, accountID int64, on string) {
	t.Helper()
	if _, err := store.New(w.l.db).UpdateAccount(context.Background(), store.UpdateAccountParams{
		ID: accountID, SetInactiveOn: 1, InactiveOn: &on,
	}); err != nil {
		t.Fatalf("retiring account %d on %q = %v, want no error", accountID, on, err)
	}
}

func (w retiredWorld) reinstate(t *testing.T, accountID int64) {
	t.Helper()
	if _, err := store.New(w.l.db).UpdateAccount(context.Background(), store.UpdateAccountParams{
		ID: accountID, SetInactiveOn: 1, InactiveOn: nil,
	}); err != nil {
		t.Fatalf("reinstating account %d = %v, want no error", accountID, err)
	}
}

// ledgerState is every integer a refused posting must leave alone, plus the
// row counts of every table a posting could write to.
type ledgerState struct {
	transactions, transfers, reconciliations int
	fund, cash, bank, main, envelope         money.Amount
	envelopeClosed                           bool
}

func (w retiredWorld) state(t *testing.T) ledgerState {
	t.Helper()
	ctx := context.Background()
	q := store.New(w.l.db)
	var s ledgerState

	txs, err := q.ListTransactionsByFund(ctx, w.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	s.transactions = len(txs)
	s.transfers = transferCount(t, w.l, w.fundID)
	recs, err := q.ListReconciliationsByFund(ctx, w.fundID)
	if err != nil {
		t.Fatalf("ListReconciliationsByFund() = %v, want no error", err)
	}
	s.reconciliations = len(recs)

	get := func(name string, v money.Amount, err error) money.Amount {
		t.Helper()
		if err != nil {
			t.Fatalf("reading the %s balance = %v, want no error", name, err)
		}
		return v
	}
	v, err := w.l.FundBalance(ctx, w.fundID)
	s.fund = get("fund", v, err)
	v, err = w.l.AccountBalance(ctx, w.fundID, w.cashID)
	s.cash = get("cash", v, err)
	v, err = w.l.AccountBalance(ctx, w.fundID, w.bankID)
	s.bank = get("bank", v, err)
	v, err = w.l.PurposeBalance(ctx, w.fundID, w.mainID)
	s.main = get("main purpose", v, err)
	v, err = w.l.PurposeBalance(ctx, w.fundID, w.envelopeID)
	s.envelope = get("envelope", v, err)

	env, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: w.envelopeID, FundID: w.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	s.envelopeClosed = env.ClosedOn != nil
	return s
}

func TestRetiredWorldStartsWhereTheTestsExpect(t *testing.T) {
	t.Parallel()
	w := newRetiredWorld(t, newTestLedger(t))
	got := w.state(t)
	want := ledgerState{
		transactions: 3, fund: 750_000, cash: 550_000, bank: 200_000, main: 700_000, envelope: 50_000,
	}
	if got != want {
		t.Fatalf("starting state = %+v, want %+v", got, want)
	}
}

// Every posting that names a location the caller chose refuses a retired one
// with ErrAccountInactive, writes nothing, and moves no integer anywhere
// (#444, PRD 7.8 and 6).
func TestPostingsRefuseARetiredLocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// duesWorld builds a member with a rate, which is not a posting against
	// the location, and returns the member id.
	duesMember := func(t *testing.T, w retiredWorld) int64 {
		q := store.New(w.l.db)
		tierID := createDuesTier(t, q, w.fundID, "Tier A")
		createDuesRate(t, q, tierID, 25_000, "2026-01")
		return createDuesMember(t, q, w.fundID, duesMemberParams{name: "Budi", tierID: &tierID})
	}
	var memberID, claimID int64

	cases := []struct {
		name string
		// setup builds whatever the posting needs that is not itself a
		// posting against the location (a member, a claim, a prior
		// spend). It runs before the state is taken.
		setup func(t *testing.T, w retiredWorld)
		post  func(w retiredWorld) error
	}{
		{"record money in", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.mainID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"record money out", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.mainID,
				Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"adjusting entry", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.mainID,
				Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10", IsAdjustment: true,
			})
			return err
		}},
		{"contribution to an envelope", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.envelopeID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"named contribution", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.envelopeID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10", MemberID: &w.memberID,
			})
			return err
		}},
		{"titipan received", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.passID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"titipan forwarded", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.passID,
				Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"dues payment", func(t *testing.T, w retiredWorld) { memberID = duesMember(t, w) }, func(w retiredWorld) error {
			_, err := w.l.PostDuesPayments(ctx, PostDuesPaymentsParams{
				FundID: w.fundID, AccountID: w.bankID,
				MemberID: memberID, OccurredOn: "2026-09-10",
				Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
			})
			return err
		}},
		{"multi-period dues payment", func(t *testing.T, w retiredWorld) { memberID = duesMember(t, w) }, func(w retiredWorld) error {
			_, err := w.l.PostDuesPayments(ctx, PostDuesPaymentsParams{
				FundID: w.fundID, AccountID: w.bankID,
				MemberID: memberID, OccurredOn: "2026-09-10",
				Periods: []PeriodAmount{
					{DuesPeriod: "2026-09", Amount: 25_000},
					{DuesPeriod: "2026-10", Amount: 25_000},
					{DuesPeriod: "2026-11", Amount: 25_000},
				},
			})
			return err
		}},
		{"transfer out of the retired location", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
				FundID: w.fundID, PurposeID: w.mainID,
				FromAccountID: w.bankID, ToAccountID: w.cashID, Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"transfer into the retired location", nil, func(w retiredWorld) error {
			_, err := w.l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
				FundID: w.fundID, PurposeID: w.mainID,
				FromAccountID: w.cashID, ToAccountID: w.bankID, Amount: 10_000, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"settle a reimbursement", func(t *testing.T, w retiredWorld) {
			claimID = createReimbursement(t, store.New(w.l.db), w.fixture, 75_000, "2026-09-03", nil).ID
		}, func(w retiredWorld) error {
			_, err := w.l.SettleReimbursement(ctx, SettleReimbursementParams{
				FundID: w.fundID, ReimbursementID: claimID, AccountID: w.bankID, OccurredOn: "2026-09-10",
			})
			return err
		}},
		{"close an envelope holding a leftover", nil, func(w retiredWorld) error {
			_, err := w.l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
				FundID: w.fundID, PurposeID: w.envelopeID, AccountID: w.bankID, ClosedOn: "2026-09-10",
			})
			return err
		}},
		{"close an envelope overspent", func(t *testing.T, w retiredWorld) {
			// Disburse 80.000 against 50.000 collected: a 30.000 shortfall
			// Kas Utama would cover - on the retired location, which is
			// refused. The spend itself goes on cash.
			if _, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.cashID, PurposeID: w.envelopeID,
				Direction: "out", Amount: 80_000, OccurredOn: "2026-09-04",
			}); err != nil {
				t.Fatalf("overspending the envelope = %v, want no error", err)
			}
		}, func(w retiredWorld) error {
			_, err := w.l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
				FundID: w.fundID, PurposeID: w.envelopeID, AccountID: w.bankID, ClosedOn: "2026-09-10",
			})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: setup writes through the shared closure
			// variables above. The ledgers are private in-memory
			// databases, so the cost is a few milliseconds each.
			w := newRetiredWorld(t, newTestLedger(t))
			if tc.setup != nil {
				tc.setup(t, w)
			}
			before := w.state(t)

			if err := tc.post(w); !errors.Is(err, ErrAccountInactive) {
				t.Fatalf("posting to a retired location = %v, want ErrAccountInactive", err)
			}
			if got := w.state(t); got != before {
				t.Errorf("a refused posting changed the ledger: %+v -> %+v", before, got)
			}
		})
	}
}

// The control for the table above: with the bank retired, every posting that
// names only active locations still goes through, so the refusal is about the
// location and not a blanket failure.
func TestPostingsStillAcceptActiveLocationsBesideARetiredOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	tinID := createAccount(t, store.New(w.l.db), w.fundID, "cash", "Tin")

	if _, err := w.l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.cashID, PurposeID: w.mainID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
	}); err != nil {
		t.Fatalf("PostTransaction(active cash) = %v, want no error", err)
	}
	if _, err := w.l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: w.fundID, PurposeID: w.mainID,
		FromAccountID: w.cashID, ToAccountID: tinID, Amount: 4_000, OccurredOn: "2026-09-10",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts(active to active) = %v, want no error", err)
	}
	if _, err := w.l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: w.fundID, PurposeID: w.envelopeID, AccountID: w.cashID, ClosedOn: "2026-09-10",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll(active cash) = %v, want no error", err)
	}

	got := w.state(t)
	// 550.000 + 10.000 in, 4.000 moved to the tin, the envelope's 50.000
	// reclassed to Kas Utama on cash: cash 556.000, fund 760.000.
	if got.cash != 556_000 || got.bank != 200_000 || got.fund != 760_000 || got.envelope != 0 || got.main != 760_000 {
		t.Errorf("state = %+v, want cash 556000, bank 200000, fund 760000, envelope 0, main 760000", got)
	}
}

// "Retired" is inactive_on non-NULL, whatever date it holds - PostPurposeMove's
// and the SPA's reading. A posting dated before the retirement, and a
// retirement dated in the future, are both refused.
func TestRetiredMeansInactiveOnIsSetWhateverTheDates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name       string
		retiredOn  string
		occurredOn string
	}{
		{"posting dated before the retirement", "2026-09-05", "2026-08-01"},
		{"posting dated on the retirement day", "2026-09-05", "2026-09-05"},
		{"posting dated after the retirement", "2026-09-05", "2026-10-01"},
		{"retirement dated in the future", "2099-01-01", "2026-09-10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newRetiredWorld(t, newTestLedger(t))
			w.setRetired(t, w.bankID, tc.retiredOn)

			_, err := w.l.PostTransaction(ctx, PostTransactionParams{
				FundID: w.fundID, AccountID: w.bankID, PurposeID: w.mainID,
				Direction: "in", Amount: 10_000, OccurredOn: tc.occurredOn,
			})
			if !errors.Is(err, ErrAccountInactive) {
				t.Fatalf("PostTransaction() = %v, want ErrAccountInactive", err)
			}
		})
	}
}

// Reinstating is the way back: a location retired by mistake takes postings
// again, and nothing it carried was lost.
func TestAReinstatedLocationTakesPostingsAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	w.reinstate(t, w.bankID)

	if _, err := w.l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.bankID, PurposeID: w.mainID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
	}); err != nil {
		t.Fatalf("PostTransaction(reinstated bank) = %v, want no error", err)
	}
	if got := w.state(t).bank; got != 210_000 {
		t.Errorf("bank balance = %d, want 210000", got)
	}
}

// Emptying a retired location is a reinstate-move-retire round trip, and the
// move itself is an ordinary transfer: the fund total never changes, only
// where the money sits.
func TestEmptyingARetiredLocationNeedsReinstatingItFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	move := PostTransferBetweenAccountsParams{
		FundID: w.fundID, PurposeID: w.mainID,
		FromAccountID: w.bankID, ToAccountID: w.cashID, Amount: 200_000, OccurredOn: "2026-09-10",
	}

	if _, err := w.l.PostTransferBetweenAccounts(ctx, move); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("transfer out of the retired bank = %v, want ErrAccountInactive", err)
	}
	w.reinstate(t, w.bankID)
	if _, err := w.l.PostTransferBetweenAccounts(ctx, move); err != nil {
		t.Fatalf("transfer out of the reinstated bank = %v, want no error", err)
	}
	w.setRetired(t, w.bankID, "2026-09-11")

	got := w.state(t)
	if got.bank != 0 || got.cash != 750_000 || got.fund != 750_000 {
		t.Errorf("after the round trip bank/cash/fund = %d/%d/%d, want 0/750000/750000", got.bank, got.cash, got.fund)
	}
}

// A close that has nothing to roll posts nothing, so the account it names is
// never written to: it must not be refused for a location the screen merely
// defaulted to.
func TestClosingAnEnvelopeWithNothingToRollIgnoresARetiredAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))

	// Spend exactly what was collected: leftover 0.
	if _, err := w.l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.cashID, PurposeID: w.envelopeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-09-04",
	}); err != nil {
		t.Fatalf("spending the envelope = %v, want no error", err)
	}
	rolled, err := w.l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: w.fundID, PurposeID: w.envelopeID, AccountID: w.bankID, ClosedOn: "2026-09-10",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll(leftover 0, retired account) = %v, want no error", err)
	}
	if rolled != 0 {
		t.Errorf("rolled = %d, want 0", rolled)
	}
	if got := w.state(t); !got.envelopeClosed || got.bank != 200_000 {
		t.Errorf("state = %+v, want the envelope closed and the bank untouched at 200000", got)
	}
}

// An id that is not this fund's is not "retired": the guard stays out of the
// way and the composite foreign key refuses it, exactly as before (ADR-027).
func TestAnUnknownLocationIsTheSchemasToRefuseNotTheRetiredGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	other := newFixtureWithSlug(t, w.l, "Other Fund", "zyxwvutsrqponmlkjihgfe")

	for name, accountID := range map[string]int64{"nonexistent": 999_999, "another fund's": other.cashID} {
		_, err := w.l.PostTransaction(ctx, PostTransactionParams{
			FundID: w.fundID, AccountID: accountID, PurposeID: w.mainID,
			Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
		})
		if err == nil {
			t.Fatalf("%s account: PostTransaction() = nil, want the foreign key to refuse it", name)
		}
		if errors.Is(err, ErrAccountInactive) {
			t.Errorf("%s account: error = %v, must not be ErrAccountInactive", name, err)
		}
	}
}

// Corrections never lose their way home. A reversal and a purpose correction
// take their location from the row they correct, not from the treasurer, so a
// mistake on a since-retired location stays correctable without reinstating
// anything (CLAUDE.md rule 3).
func TestCorrectionsOnARowOfARetiredLocationStillWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	w.reinstate(t, w.bankID)

	q := store.New(w.l.db)
	tierID := createDuesTier(t, q, w.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, w.fundID, duesMemberParams{name: "Budi", tierID: &tierID})
	paid, err := w.l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: w.fundID, AccountID: w.bankID, MemberID: memberID,
		OccurredOn: "2026-09-04", Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	spent, err := w.l.PostTransaction(ctx, PostTransactionParams{
		FundID: w.fundID, AccountID: w.bankID, PurposeID: w.mainID,
		Direction: "out", Amount: 40_000, OccurredOn: "2026-09-04",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	w.setRetired(t, w.bankID, retiredOn)
	before := w.state(t)
	if before.bank != 185_000 {
		t.Fatalf("bank balance before correcting = %d, want 185000", before.bank)
	}

	if _, err := w.l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: w.fundID, TransactionID: paid[0].ID, OccurredOn: "2026-09-12",
	}); err != nil {
		t.Fatalf("ReverseDuesPayment(row on a retired location) = %v, want no error", err)
	}
	if _, err := w.l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: w.fundID, TransactionID: spent.ID, PurposeID: w.passID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection(row on a retired location) = %v, want no error", err)
	}

	after := w.state(t)
	// The reversal took the 25.000 back out of the bank; the purpose
	// correction moved no money between locations at all.
	if after.bank != 160_000 {
		t.Errorf("bank balance = %d, want 160000 (185000 less the reversed 25000)", after.bank)
	}
	if after.fund != before.fund-25_000 {
		t.Errorf("fund balance = %d, want %d", after.fund, before.fund-25_000)
	}
}

// --- Cek kas: each active location, no retired one ---------------------------

func countOf(accountID int64, amount money.Amount, resolution string) AccountCount {
	return AccountCount{AccountID: accountID, ActualAmount: amount, Resolution: resolution}
}

// A snapshot must name every active location. Omitting one is refused whole:
// no reconciliation row, no line, and no fix posted for the lines that were
// named (PRD 7.8).
func TestTakeReconciliationRefusesASnapshotThatOmitsAnActiveLocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name   string
		counts func(t *testing.T, w retiredWorld) []AccountCount
	}{
		{"only cash counted", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{countOf(w.cashID, 550_000, "matched")}
		}},
		{"only the bank counted", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{countOf(w.bankID, 200_000, "matched")}
		}},
		{"an adjusted fix on the counted line must not post", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{{
				AccountID: w.cashID, ActualAmount: 540_000, Resolution: "adjusted",
				Fix: &Fix{PurposeID: w.mainID, Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10"},
			}}
		}},
		{"an entry_added fix on the counted line must not post", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{{
				AccountID: w.cashID, ActualAmount: 540_000, Resolution: "entry_added",
				Fix: &Fix{PurposeID: w.mainID, Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10"},
			}}
		}},
		{"a third location, only two counted", func(t *testing.T, w retiredWorld) []AccountCount {
			createAccount(t, store.New(w.l.db), w.fundID, "cash", "Tin")
			return []AccountCount{countOf(w.cashID, 550_000, "matched"), countOf(w.bankID, 200_000, "matched")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newRetiredWorld(t, newTestLedger(t))
			w.reinstate(t, w.bankID) // both locations active
			counts := tc.counts(t, w)
			before := w.state(t)

			_, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{FundID: w.fundID, Counts: counts})
			if !errors.Is(err, ErrReconciliationMissingLocation) {
				t.Fatalf("TakeReconciliation() = %v, want ErrReconciliationMissingLocation", err)
			}
			if errors.Is(err, ErrInvalidArgument) {
				t.Errorf("error = %v, want it distinct from ErrInvalidArgument", err)
			}
			if got := w.state(t); got != before {
				t.Errorf("a refused snapshot changed the ledger: %+v -> %+v", before, got)
			}
		})
	}
}

func TestTakeReconciliationRefusesACountOfARetiredLocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name   string
		counts func(t *testing.T, w retiredWorld) []AccountCount
	}{
		{"both counted, one retired", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{countOf(w.cashID, 550_000, "matched"), countOf(w.bankID, 200_000, "matched")}
		}},
		{"only the retired one counted", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{countOf(w.bankID, 200_000, "matched")}
		}},
		{"retired one counted left_open", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{countOf(w.cashID, 550_000, "matched"), countOf(w.bankID, 0, "left_open")}
		}},
		{"a fix aimed at the retired one must not post", func(_ *testing.T, w retiredWorld) []AccountCount {
			return []AccountCount{
				countOf(w.cashID, 550_000, "matched"),
				{
					AccountID: w.bankID, ActualAmount: 190_000, Resolution: "adjusted",
					Fix: &Fix{PurposeID: w.mainID, Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10"},
				},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newRetiredWorld(t, newTestLedger(t))
			before := w.state(t)

			_, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{FundID: w.fundID, Counts: tc.counts(t, w)})
			if !errors.Is(err, ErrAccountInactive) {
				t.Fatalf("TakeReconciliation() = %v, want ErrAccountInactive", err)
			}
			if got := w.state(t); got != before {
				t.Errorf("a refused snapshot changed the ledger: %+v -> %+v", before, got)
			}
		})
	}
}

// The happy path the two refusals frame: count every active location, skip the
// retired one, even when the retired one still holds money. The snapshot
// carries a line for exactly the active locations.
func TestTakeReconciliationCountsExactlyTheActiveLocations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))

	rec, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: w.fundID,
		Counts: []AccountCount{countOf(w.cashID, 550_000, "matched")},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation(active only) = %v, want no error", err)
	}
	lines, err := store.New(w.l.db).ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	if len(lines) != 1 || lines[0].AccountID != w.cashID || lines[0].DifferenceAmount != 0 {
		t.Fatalf("lines = %+v, want exactly one matched line for cash", lines)
	}
}

// Reinstating a location puts it back in every later count: the rule reads the
// account's state at snapshot time, not at some remembered moment.
func TestTakeReconciliationReadsRetirementAtSnapshotTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	cashOnly := []AccountCount{countOf(w.cashID, 550_000, "matched")}

	if _, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{FundID: w.fundID, Counts: cashOnly}); err != nil {
		t.Fatalf("snapshot while the bank is retired = %v, want no error", err)
	}
	w.reinstate(t, w.bankID)
	if _, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{FundID: w.fundID, Counts: cashOnly}); !errors.Is(err, ErrReconciliationMissingLocation) {
		t.Fatalf("cash-only snapshot after reinstating the bank = %v, want ErrReconciliationMissingLocation", err)
	}
}

// A second fund's locations are not this fund's to count, and its retired
// ones are not this fund's to refuse.
func TestTakeReconciliationCompletenessIsPerFund(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	other := newFixtureWithSlug(t, w.l, "Other Fund", "zyxwvutsrqponmlkjihgfe")
	w.setRetired(t, other.bankID, retiredOn)
	createAccount(t, store.New(w.l.db), other.fundID, "cash", "Other tin")

	if _, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: w.fundID,
		Counts: []AccountCount{countOf(w.cashID, 550_000, "matched")},
	}); err != nil {
		t.Fatalf("snapshot of the first fund, second fund's locations present = %v, want no error", err)
	}
}

// An id that is not this fund's is still the foreign key's to refuse - the
// completeness check steps aside for it rather than answering "missing" for a
// request whose real fault is the unknown id.
func TestTakeReconciliationUnknownLocationStaysAForeignKeyRefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newRetiredWorld(t, newTestLedger(t))
	w.reinstate(t, w.bankID)
	before := w.state(t)

	_, err := w.l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: w.fundID,
		Counts: []AccountCount{countOf(w.cashID, 550_000, "matched"), countOf(999_999, 0, "left_open")},
	})
	if err == nil {
		t.Fatal("TakeReconciliation(unknown account) = nil, want a refusal")
	}
	if errors.Is(err, ErrReconciliationMissingLocation) || errors.Is(err, ErrAccountInactive) {
		t.Errorf("error = %v, want the foreign key's refusal, not a retired/missing-location one", err)
	}
	if got := w.state(t); got != before {
		t.Errorf("a refused snapshot changed the ledger: %+v -> %+v", before, got)
	}
}
