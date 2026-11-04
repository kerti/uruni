package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ADR-048: a dues payment is one record binding its rows, and it is reversed
// whole. These tests hold the ledger half; the schema's refusals (a dues row
// without a payment, any other kind with one) are internal/db's.

func payDues(t *testing.T, l *Ledger, f fixture, on string, amount money.Amount, periods ...string) []store.Transaction {
	t.Helper()
	rows := make([]PeriodAmount, len(periods))
	for i, p := range periods {
		rows[i] = PeriodAmount{DuesPeriod: p, Amount: amount}
	}
	posted, err := l.at(onTheDay(on)).PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: on, Periods: rows,
	})
	if err != nil {
		t.Fatalf("PostDuesPayments(%v) = %v, want no error", periods, err)
	}
	return posted
}

func duesPaymentCount(t *testing.T, l *Ledger, fundID int64) int {
	t.Helper()
	rows, err := store.New(l.db).ListDuesPaymentsByFund(context.Background(), fundID)
	if err != nil {
		t.Fatalf("ListDuesPaymentsByFund() = %v, want no error", err)
	}
	return len(rows)
}

func TestPostDuesPaymentsWritesOneHeaderPerPayment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		payments [][]string // each inner slice is one PostDuesPayments call
	}{
		{"one month", [][]string{{"2026-08"}}},
		{"three months in one act", [][]string{{"2026-06", "2026-07", "2026-08"}}},
		{"two acts stay two payments, however alike", [][]string{{"2026-06"}, {"2026-06"}}},
		{"a single and a multi-month payment", [][]string{{"2026-05"}, {"2026-06", "2026-07"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)

			var total money.Amount
			seen := map[int64]int{}
			for _, periods := range tc.payments {
				rows := payDues(t, l, f, "2026-08-12", 25_000, periods...)
				if len(rows) != len(periods) {
					t.Fatalf("posted %d rows, want %d", len(rows), len(periods))
				}
				first := rows[0].DuesPaymentID
				for _, r := range rows {
					if r.DuesPaymentID == nil || first == nil || *r.DuesPaymentID != *first {
						t.Fatalf("rows of one payment name payments %v and %v, want one", r.DuesPaymentID, first)
					}
					total += money.Amount(r.Amount)
				}
				seen[*first] += len(rows)
			}

			if got := duesPaymentCount(t, l, f.fundID); got != len(tc.payments) || len(seen) != len(tc.payments) {
				t.Errorf("headers = %d, distinct payments on rows = %d, want %d of each", got, len(seen), len(tc.payments))
			}
			// The header moves no money: the fund holds exactly what the rows sum to.
			if got, err := l.FundBalance(context.Background(), f.fundID); err != nil || got != total {
				t.Errorf("FundBalance() = %d, %v, want %d", got, err, total)
			}
		})
	}
}

func TestPostDuesPaymentsReplayReturnsTheFirstRowsAndNoSecondHeader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	post := func() ([]store.Transaction, *Idempotency) {
		idem := &Idempotency{Key: "k-dues-0000000001", Fingerprint: strings.Repeat("a", 64), Now: 1_000, ExpiresAt: 2_000}
		rows, err := l.at(onTheDay("2026-08-12")).PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-12",
			Periods:     []PeriodAmount{{DuesPeriod: "2026-07", Amount: 25_000}, {DuesPeriod: "2026-08", Amount: 25_000}},
			Idempotency: idem,
		})
		if err != nil {
			t.Fatalf("PostDuesPayments() = %v, want no error", err)
		}
		return rows, idem
	}
	first, _ := post()
	again, idem := post()

	if !idem.Replayed {
		t.Fatal("the second call did not replay, want it to")
	}
	if len(again) != len(first) {
		t.Fatalf("replay returned %d rows, want the first call's %d", len(again), len(first))
	}
	for i := range first {
		if again[i].ID != first[i].ID || again[i].DuesPaymentID == nil || first[i].DuesPaymentID == nil || *again[i].DuesPaymentID != *first[i].DuesPaymentID {
			t.Errorf("replayed row %d = %+v, want the first call's %+v", i, again[i], first[i])
		}
	}
	if got := duesPaymentCount(t, l, f.fundID); got != 1 {
		t.Errorf("headers after a replay = %d, want 1", got)
	}
	if got := transactionCount(t, l, f.fundID); got != 2 {
		t.Errorf("rows after a replay = %d, want 2", got)
	}
}

// A refused payment leaves no header behind: the header is inserted in the same
// transaction as its rows.
func TestPostDuesPaymentsRefusedLeavesNoHeader(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	on := "2026-01-01"
	if _, err := store.New(l.db).UpdateAccount(context.Background(), store.UpdateAccountParams{ID: f.cashID, SetInactiveOn: 1, InactiveOn: &on}); err != nil {
		t.Fatalf("retiring the location = %v, want no error", err)
	}
	_, err := l.at(onTheDay("2026-08-12")).PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("PostDuesPayments(retired account) = %v, want ErrAccountInactive", err)
	}
	if got := duesPaymentCount(t, l, f.fundID); got != 0 {
		t.Errorf("headers after a refused payment = %d, want 0", got)
	}
}

func reversalRowsOf(t *testing.T, l *Ledger, fundID int64) []store.Transaction {
	t.Helper()
	all, err := store.New(l.db).ListTransactionsByFund(context.Background(), fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	var out []store.Transaction
	for _, r := range all {
		if r.ReversesTransactionID != nil {
			out = append(out, r)
		}
	}
	return out
}

func TestReverseDuesPaymentReversesEveryStandingMonthInOneAct(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	before, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	rows := payDues(t, l, f, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	note := "salah catat"

	got, err := l.at(onTheDay("2026-08-20")).ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: *rows[0].DuesPaymentID, Note: &note})
	if err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}
	if len(got) != 3 {
		t.Fatalf("posted %d reversals, want one per month (3)", len(got))
	}
	for i, r := range got {
		orig := rows[i]
		switch {
		case r.Kind != "adjustment" || r.Direction != "out":
			t.Errorf("reversal %d = kind %q direction %q, want an outgoing adjustment", i, r.Kind, r.Direction)
		case r.ReversesTransactionID == nil || *r.ReversesTransactionID != orig.ID:
			t.Errorf("reversal %d reverses %v, want the row of %s (%d)", i, r.ReversesTransactionID, *orig.DuesPeriod, orig.ID)
		case r.Amount != orig.Amount || r.AccountID != orig.AccountID || r.PurposeID != orig.PurposeID:
			t.Errorf("reversal %d = amount %d account %d purpose %d, want the original's %d %d %d", i, r.Amount, r.AccountID, r.PurposeID, orig.Amount, orig.AccountID, orig.PurposeID)
		case r.MemberID == nil || *r.MemberID != f.memberID || r.DuesPeriod == nil || *r.DuesPeriod != *orig.DuesPeriod:
			t.Errorf("reversal %d = member %v period %v, want the original's", i, r.MemberID, r.DuesPeriod)
		case r.DuesPaymentID != nil:
			t.Errorf("reversal %d carries dues_payment_id %d, want none: it names the row it undoes", i, *r.DuesPaymentID)
		case r.OccurredOn != "2026-08-12":
			t.Errorf("reversal %d dated %s, want the payment's own day 2026-08-12", i, r.OccurredOn)
		case r.Note == nil || *r.Note != note:
			t.Errorf("reversal %d note = %v, want the caller's %q", i, r.Note, note)
		}
	}
	if after, err := l.FundBalance(ctx, f.fundID); err != nil || after != before {
		t.Errorf("FundBalance() after reversing = %d, %v, want the original %d", after, err, before)
	}
	if n := len(reversalRowsOf(t, l, f.fundID)); n != 3 {
		t.Errorf("reversal rows in the ledger = %d, want 3", n)
	}
}

// Naming one month of a payment through the transaction path reverses the
// payment: there is no way to take one month alone (ADR-048).
func TestReverseTransactionOnADuesRowReversesTheWholePayment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	rows := payDues(t, l, f, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	other := payDues(t, l, f, "2026-08-12", 25_000, "2026-09") // a separate payment, left alone

	middle := rows[1]
	got, err := l.at(onTheDay("2026-08-20")).ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: middle.ID})
	if err != nil {
		t.Fatalf("ReverseTransaction(middle month) = %v, want no error", err)
	}
	if got.ReversesTransactionID == nil || *got.ReversesTransactionID != middle.ID {
		t.Errorf("returned reversal reverses %v, want the month named (%d)", got.ReversesTransactionID, middle.ID)
	}

	reversed := map[int64]bool{}
	for _, r := range reversalRowsOf(t, l, f.fundID) {
		reversed[*r.ReversesTransactionID] = true
	}
	for _, r := range rows {
		if !reversed[r.ID] {
			t.Errorf("month %s of the payment was not reversed", *r.DuesPeriod)
		}
	}
	if reversed[other[0].ID] {
		t.Error("a different payment was reversed with it, want it left standing")
	}
	if bal, err := l.FundBalance(ctx, f.fundID); err != nil || bal != 25_000 {
		t.Errorf("FundBalance() = %d, %v, want only the other payment's 25000", bal, err)
	}
}

// Data from before ADR-048 can hold a payment reversed in part. Batalkan takes
// the rest, and only the rest, by either door.
func TestReverseDuesPaymentOfAPartlyReversedPaymentTakesTheRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A payment of three months whose first has a single-month reversal, written
	// around the ledger as the pre-ADR-048 ReverseTransaction wrote it.
	partlyReversed := func(t *testing.T) (*Ledger, fixture, []store.Transaction) {
		t.Helper()
		l := newTestLedger(t)
		f := newFixture(t, l)
		rows := payDues(t, l, f, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
		if _, err := store.New(l.db).CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: rows[0].AccountID, PurposeID: rows[0].PurposeID, Direction: "out",
			Amount: rows[0].Amount, OccurredOn: "2026-08-12", Kind: "adjustment", MemberID: rows[0].MemberID,
			DuesPeriod: rows[0].DuesPeriod, ReversesTransactionID: &rows[0].ID, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("inserting the legacy reversal = %v, want no error", err)
		}
		return l, f, rows
	}
	assertTakenInFull := func(t *testing.T, l *Ledger, f fixture) {
		t.Helper()
		if n := len(reversalRowsOf(t, l, f.fundID)); n != 3 {
			t.Errorf("reversal rows = %d, want the legacy one and the two just posted (3)", n)
		}
		if bal, err := l.FundBalance(ctx, f.fundID); err != nil || bal != 0 {
			t.Errorf("FundBalance() = %d, %v, want 0", bal, err)
		}
	}

	t.Run("by the payment", func(t *testing.T) {
		t.Parallel()
		l, f, rows := partlyReversed(t)
		got, err := l.at(onTheDay("2026-08-20")).ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: *rows[1].DuesPaymentID})
		if err != nil {
			t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
		}
		if len(got) != 2 {
			t.Fatalf("posted %d reversals, want the 2 months still standing", len(got))
		}
		assertTakenInFull(t, l, f)
	})

	t.Run("by the month already reversed", func(t *testing.T) {
		t.Parallel()
		l, f, rows := partlyReversed(t)
		if _, err := l.at(onTheDay("2026-08-20")).ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: rows[0].ID}); err != nil {
			t.Fatalf("ReverseTransaction(the month already reversed) = %v, want the rest taken", err)
		}
		assertTakenInFull(t, l, f)
	})
}

func TestReverseDuesPaymentRefusesWhatIsAlreadyReversedOrNotThere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	rows := payDues(t, l, f, "2026-08-12", 25_000, "2026-07", "2026-08")
	paymentID := *rows[0].DuesPaymentID

	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: paymentID}); err != nil {
		t.Fatalf("first ReverseDuesPayment() = %v, want no error", err)
	}
	before := transactionCount(t, l, f.fundID)

	otherFund := newFixture2(t, l)
	for _, tc := range []struct {
		name string
		call func() error
		want error
	}{
		{"the payment again", func() error {
			_, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: paymentID})
			return err
		}, ErrAlreadyReversed},
		{"any month of it by the transaction path", func() error {
			_, err := l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: rows[1].ID})
			return err
		}, ErrAlreadyReversed},
		{"a payment that does not exist", func() error {
			_, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: paymentID + 100})
			return err
		}, ErrReversalNotFound},
		{"another fund's payment", func() error {
			_, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: otherFund, DuesPaymentID: paymentID})
			return err
		}, ErrReversalNotFound},
	} {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}
	if got := transactionCount(t, l, f.fundID); got != before {
		t.Errorf("rows after the refusals = %d, want the %d before them", got, before)
	}
}

// One month the ledger refuses refuses the whole act, and nothing is written:
// the other months, which alone could be reversed, stay standing.
func TestReverseDuesPaymentIsAllOrNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)

	// A payment whose second month is dated after the day a count has covered it:
	// the counted reversal would take today, ahead of what it reverses. Rows are
	// written around the ledger because PostDuesPayments never dates two months
	// apart.
	payment, err := q.CreateDuesPayment(ctx, store.CreateDuesPaymentParams{FundID: f.fundID, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ period, on string }{{"2026-06", "2026-08-01"}, {"2026-07", "2026-12-20"}} {
		period := r.period
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 25_000,
			OccurredOn: r.on, Kind: "dues", MemberID: &f.memberID, DuesPeriod: &period, DuesPaymentID: &payment.ID, CreatedAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	countEverything(t, l, f.fundID, f.cashID, f.bankID)
	before := transactionCount(t, l, f.fundID)

	_, err = l.at(onTheDay("2026-10-02")).ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: payment.ID})
	if !errors.Is(err, ErrReversalBeforeOriginal) {
		t.Fatalf("ReverseDuesPayment() = %v, want ErrReversalBeforeOriginal", err)
	}
	if got := transactionCount(t, l, f.fundID); got != before {
		t.Errorf("rows after the refusal = %d, want %d: the refused act left nothing", got, before)
	}
}

// The cek kas rule is ADR-042's, applied month by month: a counted payment's
// reversals are all dated today, an uncounted one's on its own day.
func TestReverseDuesPaymentDatesEveryMonthByTheCountRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		counted bool
		want    string
	}{
		{"uncounted, the payment's own day", false, "2026-08-12"},
		{"counted, today in the fund's zone", true, "2026-10-02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			rows := payDues(t, l, f, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
			if tc.counted {
				countEverything(t, l, f.fundID, f.cashID, f.bankID)
			}
			got, err := l.at(onTheDay("2026-10-02")).ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: *rows[0].DuesPaymentID})
			if err != nil {
				t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
			}
			for _, r := range got {
				if r.OccurredOn != tc.want {
					t.Errorf("reversal of %s dated %s, want %s", *r.DuesPeriod, r.OccurredOn, tc.want)
				}
			}
		})
	}
}

// newFixture2 is a second, empty-handed fund in the same database: only its id.
func newFixture2(t *testing.T, l *Ledger) int64 {
	t.Helper()
	fund, err := store.New(l.db).CreateFund(context.Background(), store.CreateFundParams{
		Name: "Another Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "zzzzzzzzzzzzzzzzzzzzzzzz", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund(second) = %v, want no error", err)
	}
	return fund.ID
}
