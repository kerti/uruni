package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// --- fixture helpers local to this file -----------------------------------

func createDuesTier(t *testing.T, q *store.Queries, fundID int64, name string) int64 {
	t.Helper()
	tier, err := q.CreateDuesTier(context.Background(), store.CreateDuesTierParams{
		FundID: fundID, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateDuesTier(%q) = %v, want no error", name, err)
	}
	return tier.ID
}

func createDuesRate(t *testing.T, q *store.Queries, tierID int64, amount money.Amount, effectiveFrom string) {
	t.Helper()
	if _, err := q.CreateDuesRate(context.Background(), store.CreateDuesRateParams{
		TierID: tierID, Amount: amount.Int64(), EffectiveFrom: effectiveFrom, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateDuesRate(tier=%d, effective=%q) = %v, want no error", tierID, effectiveFrom, err)
	}
}

// duesMemberParams is the shape createDuesMember needs - a superset of
// store.CreateMemberParams narrowed to the fields these tests vary.
type duesMemberParams struct {
	name       string
	tierID     *int64
	joinedOn   *string
	inactiveOn *string
}

func createDuesMember(t *testing.T, q *store.Queries, fundID int64, p duesMemberParams) int64 {
	t.Helper()
	m, err := q.CreateMember(context.Background(), store.CreateMemberParams{
		FundID: fundID, Name: p.name, TierID: p.tierID,
		JoinedOn: p.joinedOn, InactiveOn: p.inactiveOn, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(%q) = %v, want no error", p.name, err)
	}
	return m.ID
}

func statusFor(t *testing.T, rows []MemberDuesStatus, memberID int64) (MemberDuesStatus, bool) {
	t.Helper()
	for _, r := range rows {
		if r.Member.ID == memberID {
			return r, true
		}
	}
	return MemberDuesStatus{}, false
}

// --- unpaid / partial / paid / paid in advance ------------------------------

func TestDuesStatusForPeriodUnpaidMemberHasPaidNothing(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}

	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster, want it present as unpaid", memberID)
	}
	if got.Status != DuesStatusUnpaid {
		t.Errorf("Status = %q, want %q", got.Status, DuesStatusUnpaid)
	}
	if got.OwedAmount != 25_000 {
		t.Errorf("OwedAmount = %d, want 25000", got.OwedAmount)
	}
	if got.PaidAmount != 0 {
		t.Errorf("PaidAmount = %d, want 0", got.PaidAmount)
	}
}

func TestDuesStatusForPeriodPartialMemberPaidLessThanTheRate(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-06-15",
		Periods: []PeriodAmount{{DuesPeriod: "2026-06", Amount: 10_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}

	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster", memberID)
	}
	if got.Status != DuesStatusPartial {
		t.Errorf("Status = %q, want %q", got.Status, DuesStatusPartial)
	}
	if got.PaidAmount != 10_000 {
		t.Errorf("PaidAmount = %d, want 10000", got.PaidAmount)
	}
}

func TestDuesStatusForPeriodPaidMemberPaidExactlyTheRate(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-06-15",
		Periods: []PeriodAmount{{DuesPeriod: "2026-06", Amount: 25_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}

	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster", memberID)
	}
	if got.Status != DuesStatusPaid {
		t.Errorf("Status = %q, want %q", got.Status, DuesStatusPaid)
	}
}

// Overpayment (paid more than the rate) reads as Paid, not a fifth status:
// the treasurer's question this view answers is "did they clear what they
// owe", and paying more still answers yes. The exact figure stays visible
// in PaidAmount for anyone who wants it.
func TestDuesStatusForPeriodOverpaymentReadsAsPaid(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-06-15",
		Periods: []PeriodAmount{{DuesPeriod: "2026-06", Amount: 40_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}

	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster", memberID)
	}
	if got.Status != DuesStatusPaid {
		t.Errorf("Status = %q, want %q (overpayment is still Paid)", got.Status, DuesStatusPaid)
	}
	if got.OwedAmount != 25_000 {
		t.Errorf("OwedAmount = %d, want 25000 (the rate, unaffected by what was paid)", got.OwedAmount)
	}
	if got.PaidAmount != 40_000 {
		t.Errorf("PaidAmount = %d, want 40000 (the actual overpaid figure)", got.PaidAmount)
	}
}

// Paid in advance is Paid-for-this-period plus a later period also paid, not
// merely "a later period exists somewhere in this member's history". A
// period that is itself unpaid or partial stays unpaid/partial even if the
// member later paid ahead for some other month.
func TestDuesStatusForPeriodPaidInAdvanceWhenALaterPeriodIsAlsoPaid(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	for _, period := range []string{"2026-06", "2026-08"} {
		if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			MemberID: memberID, OccurredOn: "2026-08-01",
			Periods: []PeriodAmount{{DuesPeriod: period, Amount: 25_000}},
		}); err != nil {
			t.Fatalf("PostDuesPayments(%q) = %v, want no error", period, err)
		}
	}

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}

	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster", memberID)
	}
	if got.Status != DuesStatusPaidInAdvance {
		t.Errorf("Status = %q, want %q - June is paid, and August (later) was also paid", got.Status, DuesStatusPaidInAdvance)
	}
}

// A member who skipped a period entirely still reads as Unpaid for it, even
// though a later period was paid - "paid in advance" describes a period
// that is itself settled and then some, not a hole with a payment beyond it.
func TestDuesStatusForPeriodSkippedPeriodStaysUnpaidDespiteALaterPayment(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	// Paid August, but never paid the July being queried below.
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-08-01",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-07")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}

	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster", memberID)
	}
	if got.Status != DuesStatusUnpaid {
		t.Errorf("Status = %q, want %q - July itself was never paid", got.Status, DuesStatusUnpaid)
	}
}

// Several months paid at once shows correctly for each of those periods:
// the last one paid is Paid, and every earlier paid month reads as Paid in
// advance because a later period was also paid.
func TestDuesStatusForPeriodSeveralMonthsPaidAtOnceShowCorrectlyPerPeriod(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	for _, period := range []string{"2026-06", "2026-07", "2026-08"} {
		if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
			MemberID: memberID, OccurredOn: "2026-08-01",
			Periods: []PeriodAmount{{DuesPeriod: period, Amount: 25_000}},
		}); err != nil {
			t.Fatalf("PostDuesPayments(%q) = %v, want no error", period, err)
		}
	}

	want := map[string]DuesStatus{
		"2026-06": DuesStatusPaidInAdvance,
		"2026-07": DuesStatusPaidInAdvance,
		"2026-08": DuesStatusPaid,
	}
	for period, wantStatus := range want {
		rows, err := l.DuesStatusForPeriod(ctx, f.fundID, period)
		if err != nil {
			t.Fatalf("DuesStatusForPeriod(%q) = %v, want no error", period, err)
		}
		got, ok := statusFor(t, rows, memberID)
		if !ok {
			t.Fatalf("period %q: member %d missing from roster", period, memberID)
		}
		if got.Status != wantStatus {
			t.Errorf("period %q: Status = %q, want %q", period, got.Status, wantStatus)
		}
		if got.PaidAmount != 25_000 {
			t.Errorf("period %q: PaidAmount = %d, want 25000", period, got.PaidAmount)
		}
	}
}

// --- exclusion / omission from the roster -----------------------------------

func TestDuesStatusForPeriodMemberWithNoTierIsExcluded(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane"}) // tierID left nil

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, memberID); ok {
		t.Errorf("member %d with no tier appeared in the roster, want excluded entirely", memberID)
	}
}

// The "madya TBD" case, PRD 6: a tier whose rate is not yet decided has no
// dues_rate row at all for the period. Uruni does not invent an amount, so
// the member is omitted rather than shown as owing an unknown figure.
func TestDuesStatusForPeriodMemberWhoseTierHasNoRateForThePeriodIsOmitted(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier TBD") // no dues_rate row at all
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, memberID); ok {
		t.Errorf("member %d with no effective rate appeared in the roster, want omitted", memberID)
	}
}

// A tier whose rate only takes effect later than the queried period is the
// same omission case, reached through a real effective_from rather than an
// empty dues_rate table.
func TestDuesStatusForPeriodMemberWhoseTierRateStartsAfterThePeriodIsOmitted(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-09") // starts after the period below
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, memberID); ok {
		t.Errorf("member %d whose rate starts after the period appeared in the roster, want omitted", memberID)
	}
}

// --- active window: joined_on / inactive_on, inclusive by month ------------

func TestDuesStatusForPeriodMemberOwesTheMonthTheyJoinedInFull(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-06-20" // joined mid-month
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d joined mid-period, want them owing that month in full", memberID)
	}
	if got.OwedAmount != 25_000 {
		t.Errorf("OwedAmount = %d, want the full 25000 even though they joined mid-month", got.OwedAmount)
	}
}

func TestDuesStatusForPeriodMemberExcludedBeforeTheMonthTheyJoined(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-06-20"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-05")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, memberID); ok {
		t.Errorf("member %d appeared for a period before they joined, want excluded", memberID)
	}
}

func TestDuesStatusForPeriodMemberOwesTheMonthTheyWentInactiveInFull(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	inactiveOn := "2026-06-10" // went inactive mid-month
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, inactiveOn: &inactiveOn})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d went inactive mid-period, want them owing that month in full", memberID)
	}
	if got.OwedAmount != 25_000 {
		t.Errorf("OwedAmount = %d, want the full 25000 even though they went inactive mid-month", got.OwedAmount)
	}
}

func TestDuesStatusForPeriodMemberExcludedAfterTheMonthTheyWentInactive(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	inactiveOn := "2026-06-10"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, inactiveOn: &inactiveOn})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-07")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, memberID); ok {
		t.Errorf("member %d appeared for a period after they went inactive, want excluded", memberID)
	}
}

func TestDuesStatusForPeriodNilJoinedOnMeansAlwaysWasAMember(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID}) // joinedOn nil

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2021-03")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, memberID); !ok {
		t.Errorf("member %d with joined_on = NULL missing for an early period, want them owing it", memberID)
	}
}

// --- malformed period --------------------------------------------------------

func TestDuesStatusForPeriodRejectsMalformedPeriod(t *testing.T) {
	tests := []struct {
		name   string
		period string
	}{
		{"invalid month", "2026-13"},
		{"unpadded month", "2026-1"},
		{"not a period", "not-a-month"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.DuesStatusForPeriod(ctx, f.fundID, tt.period)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("DuesStatusForPeriod(%q) = %v, want an error wrapping ErrInvalidArgument", tt.period, err)
			}
		})
	}
}

// --- fund scoping ------------------------------------------------------------

func TestDuesStatusForPeriodScopesToOneFund(t *testing.T) {
	l := newTestLedger(t)
	f1 := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	// Mirrors TestASecondFundsRowsNeverAppearInTheFirstFundsBalances in
	// balance_test.go: a second fund built by hand, not a second
	// newFixture(t, l) call, because newFixture hard-codes the fund's
	// report_slug and a second fund needs a distinct one.
	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	f2cash := createAccount(t, q, other.ID, "cash", "Cash")
	f2main := createPurpose(t, q, other.ID, "main", "Main")

	tier1 := createDuesTier(t, q, f1.fundID, "Tier A")
	createDuesRate(t, q, tier1, 25_000, "2026-01")
	member1 := createDuesMember(t, q, f1.fundID, duesMemberParams{name: "Jane", tierID: &tier1})

	tier2 := createDuesTier(t, q, other.ID, "Tier A")
	createDuesRate(t, q, tier2, 99_000, "2026-01")
	member2 := createDuesMember(t, q, other.ID, duesMemberParams{name: "John", tierID: &tier2})
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: other.ID, AccountID: f2cash, PurposeID: f2main,
		MemberID: member2, OccurredOn: "2026-06-01",
		Periods: []PeriodAmount{{DuesPeriod: "2026-06", Amount: 99_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.DuesStatusForPeriod(ctx, f1.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	if _, ok := statusFor(t, rows, member2); ok {
		t.Errorf("fund 2's member %d appeared in fund 1's roster", member2)
	}
	got1, ok := statusFor(t, rows, member1)
	if !ok {
		t.Fatalf("fund 1's own member %d missing from fund 1's roster", member1)
	}
	if got1.OwedAmount != 25_000 {
		t.Errorf("fund 1 member's OwedAmount = %d, want 25000 (fund 1's own rate, not fund 2's 99000)", got1.OwedAmount)
	}
}

// --- the accepted limitation (ADR-024) --------------------------------------

// member.tier_id is not effective-dated (ADR-024's accepted limitation), so
// a member promoted mid-year has their CURRENT tier applied to every past
// period too. This member is on the schema's only representable state -
// their present tier, Tier B (the promotion) - and January's owed figure
// comes back at Tier B's rate, not the Tier A rate that was actually in
// force when January happened. That is the accepted, documented wrong
// answer, not a bug: see ADR-024's "Accepted limitation" paragraph and the
// comment on GetEffectiveDuesRate's call site in dues_status.go.
func TestDuesStatusForPeriodMidYearPromotionAppliesCurrentTierToPastPeriodsAcceptedLimitation(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierA := createDuesTier(t, q, f.fundID, "Tier A") // the low tier, actually in force in January
	createDuesRate(t, q, tierA, 10_000, "2026-01")

	tierB := createDuesTier(t, q, f.fundID, "Tier B") // the tier the member was promoted to in June
	createDuesRate(t, q, tierB, 50_000, "2026-01")

	// The schema has no record of "was on Tier A until June, then Tier B" -
	// member.tier_id carries only the member's current tier, so this row is
	// exactly what the database holds today, after the promotion.
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierB})

	rows, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-01")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v, want no error", err)
	}
	got, ok := statusFor(t, rows, memberID)
	if !ok {
		t.Fatalf("member %d missing from January's roster", memberID)
	}
	if got.OwedAmount != 50_000 {
		t.Fatalf("OwedAmount = %d, want 50000 - the CURRENT (Tier B) rate wrongly applied to January, "+
			"not 10000 (Tier A, what was actually in force). This is ADR-024's accepted limitation, "+
			"not a bug: fix it by editing member.tier_id's effective-dating decision in a superseding ADR, "+
			"not by patching this code path.", got.OwedAmount)
	}
}

// --- OutstandingDuesForMember (#186) -----------------------------------------

// TestOutstandingDuesForMemberReturnsUnpaidAndPartialOldestFirstMatchingDuesStatusForPeriod
// is the slice's core acceptance criterion: three periods in range, one
// unpaid, one partial, one paid, come back as exactly the two outstanding
// ones, oldest first, with amounts matching what DuesStatusForPeriod itself
// reports for the same periods - this is the same derivation, not a second
// opinion.
func TestOutstandingDuesForMemberReturnsUnpaidAndPartialOldestFirstMatchingDuesStatusForPeriod(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	// 2026-01 stays unpaid; 2026-02 gets a partial payment; 2026-03 gets paid
	// in full and must not appear at all.
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-03-15",
		Periods: []PeriodAmount{
			{DuesPeriod: "2026-02", Amount: 10_000},
			{DuesPeriod: "2026-03", Amount: 25_000},
		},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-03")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 2 {
		t.Fatalf("OutstandingDuesForMember() returned %d rows, want 2 (got %+v)", len(rows), rows)
	}

	if rows[0].Period != "2026-01" || rows[1].Period != "2026-02" {
		t.Fatalf("periods = [%s, %s], want oldest first: [2026-01, 2026-02]", rows[0].Period, rows[1].Period)
	}

	if rows[0].Status != DuesStatusUnpaid {
		t.Errorf("2026-01 Status = %q, want %q", rows[0].Status, DuesStatusUnpaid)
	}
	if rows[0].OwedAmount != 25_000 || rows[0].PaidAmount != 0 {
		t.Errorf("2026-01 owed/paid = %d/%d, want 25000/0", rows[0].OwedAmount, rows[0].PaidAmount)
	}
	if rows[1].Status != DuesStatusPartial {
		t.Errorf("2026-02 Status = %q, want %q", rows[1].Status, DuesStatusPartial)
	}
	if rows[1].OwedAmount != 25_000 || rows[1].PaidAmount != 10_000 {
		t.Errorf("2026-02 owed/paid = %d/%d, want 25000/10000", rows[1].OwedAmount, rows[1].PaidAmount)
	}

	// Cross-check against DuesStatusForPeriod for the same two periods - the
	// same derivation, not a re-typed opinion of it.
	for i, period := range []string{"2026-01", "2026-02"} {
		want, err := l.DuesStatusForPeriod(ctx, f.fundID, period)
		if err != nil {
			t.Fatalf("DuesStatusForPeriod(%q) = %v, want no error", period, err)
		}
		wantRow, ok := statusFor(t, want, memberID)
		if !ok {
			t.Fatalf("DuesStatusForPeriod(%q) missing member %d", period, memberID)
		}
		if rows[i].OwedAmount != wantRow.OwedAmount || rows[i].PaidAmount != wantRow.PaidAmount {
			t.Errorf("period %q: OutstandingDuesForMember owed/paid = %d/%d, DuesStatusForPeriod = %d/%d",
				period, rows[i].OwedAmount, rows[i].PaidAmount, wantRow.OwedAmount, wantRow.PaidAmount)
		}
	}
}

func TestOutstandingDuesForMemberFullyPaidMemberReturnsEmpty(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-02-01",
		Periods: []PeriodAmount{
			{DuesPeriod: "2026-01", Amount: 25_000},
			{DuesPeriod: "2026-02", Amount: 25_000},
		},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-02")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("OutstandingDuesForMember() = %+v, want [] for a member who is square", rows)
	}
}

func TestOutstandingDuesForMemberTierLessMemberReturnsEmpty(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane"}) // tierID left nil

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-06")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("OutstandingDuesForMember() = %+v, want [] for a member with no dues obligation", rows)
	}
}

// joined_on == nil means "always was a member", so the walk has no join
// month to start from and falls back to the tier's earliest rate. A tier
// that has never had a rate at all (the "madya TBD" case) therefore has no
// start either - and nothing was ever owed against it, so the answer is
// empty rather than an arbitrary starting month.
func TestOutstandingDuesForMemberNilJoinedOnAndRatelessTierReturnsEmpty(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Madya TBD") // deliberately no rate row
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{
		name: "Jane", tierID: &tierID, // joinedOn left nil
	})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-06")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("OutstandingDuesForMember() = %+v, want [] for a tier that never had a rate", rows)
	}
}

// A member who joins after `through` has no periods in range at all. The
// walk must return empty rather than iterating backwards or returning a
// period the member cannot owe yet - the arrears case in reverse.
func TestOutstandingDuesForMemberJoinedAfterThroughReturnsEmpty(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-08-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{
		name: "Jane", tierID: &tierID, joinedOn: &joinedOn,
	})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-06")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("OutstandingDuesForMember() = %+v, want [] when joined_on is after through", rows)
	}
}

func TestOutstandingDuesForMemberJoinedOnBoundsTheStart(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01") // long before joinedOn
	joinedOn := "2026-03-20"                        // joined mid-month
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-04")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}

	var periods []string
	for _, r := range rows {
		periods = append(periods, r.Period)
	}
	want := []string{"2026-03", "2026-04"}
	if len(periods) != len(want) || periods[0] != want[0] || periods[1] != want[1] {
		t.Fatalf("periods = %v, want %v - joined_on bounds the start, owed in full the month they joined", periods, want)
	}
}

func TestOutstandingDuesForMemberInactiveOnBoundsTheEnd(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-01-01"
	inactiveOn := "2026-03-10" // went inactive mid-month
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{
		name: "Jane", tierID: &tierID, joinedOn: &joinedOn, inactiveOn: &inactiveOn,
	})

	// through reaches well past inactive_on's month - the answer must stop
	// at 2026-03 regardless.
	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-06")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 3 {
		t.Fatalf("OutstandingDuesForMember() returned %d rows, want 3 (2026-01..2026-03) - got %+v", len(rows), rows)
	}
	if last := rows[len(rows)-1].Period; last != "2026-03" {
		t.Errorf("last period = %q, want %q - inactive_on bounds the end even though through reaches further", last, "2026-03")
	}
}

// A period whose tier has no effective rate is skipped, and the range keeps
// walking past it rather than stopping there: periods before the tier's
// first rate exist (because joined_on predates it) are silently omitted,
// and periods once the rate exists still appear.
func TestOutstandingDuesForMemberSkipsPeriodWithNoEffectiveRateWithoutTruncatingTheRange(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier TBD")
	createDuesRate(t, q, tierID, 25_000, "2026-04") // no rate at all before April
	joinedOn := "2026-01-01"                        // joined well before the rate exists
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-05")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}

	var periods []string
	for _, r := range rows {
		periods = append(periods, r.Period)
	}
	want := []string{"2026-04", "2026-05"}
	if len(periods) != len(want) || periods[0] != want[0] || periods[1] != want[1] {
		t.Fatalf("periods = %v, want %v - Jan..Mar have no effective rate and are skipped, "+
			"not invented and not a reason to stop before April", periods, want)
	}
}

func TestOutstandingDuesForMemberThroughBoundsTheEnd(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-02")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 2 {
		t.Fatalf("OutstandingDuesForMember(through=2026-02) returned %d rows, want 2 (got %+v)", len(rows), rows)
	}
	if last := rows[len(rows)-1].Period; last != "2026-02" {
		t.Errorf("last period = %q, want %q - through bounds the end", last, "2026-02")
	}
}

// An omitted through defaults to the server's current month - proved here by
// comparing the omitted call against an explicit through set to
// time.Now()'s own period, rather than asserting a hard-coded period the
// test's own run date would eventually make wrong.
func TestOutstandingDuesForMemberOmittedThroughDefaultsToCurrentMonth(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID}) // joined_on nil: always was a member

	omitted, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember(through=\"\") = %v, want no error", err)
	}

	explicit, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, time.Now().Format(duesPeriodLayout))
	if err != nil {
		t.Fatalf("OutstandingDuesForMember(through=now) = %v, want no error", err)
	}

	if len(omitted) != len(explicit) {
		t.Fatalf("omitted through returned %d rows, explicit current-month through returned %d - want equal",
			len(omitted), len(explicit))
	}
	if len(omitted) == 0 {
		t.Fatal("expected at least one outstanding period (member has never paid since 2020) to compare")
	}
	if last := omitted[len(omitted)-1].Period; last != explicit[len(explicit)-1].Period {
		t.Errorf("omitted through's last period = %q, explicit current-month through's last period = %q, want equal",
			last, explicit[len(explicit)-1].Period)
	}
}

func TestOutstandingDuesForMemberRejectsMalformedThrough(t *testing.T) {
	for _, through := range []string{"2026-13", "2026-1", "not-a-period"} {
		t.Run(through, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			q := store.New(l.db)
			ctx := context.Background()

			tierID := createDuesTier(t, q, f.fundID, "Tier A")
			memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

			_, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, through)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("OutstandingDuesForMember(through=%q) = %v, want an error wrapping ErrInvalidArgument", through, err)
			}
		})
	}
}

// An unknown member id - including one belonging to another fund, since
// GetMemberForFund's WHERE clause makes the two indistinguishable - answers
// sql.ErrNoRows, which the HTTP layer's mapLedgerError already turns into
// 404 (dues_status_test in package http covers that mapping; this proves
// the ledger method itself does not swallow or misreport it).
func TestOutstandingDuesForMemberUnknownMemberIsNotFound(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.OutstandingDuesForMember(ctx, f.fundID, 999_999, "2026-06")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("OutstandingDuesForMember(unknown member) = %v, want an error wrapping sql.ErrNoRows", err)
	}
}

// The mid-year-promotion limitation (ADR-024) carries over unchanged: it is
// GetEffectiveDuesRate's own call site, reused as-is, that produces it, not
// something OutstandingDuesForMember could opt out of even if it tried.
func TestOutstandingDuesForMemberMidYearPromotionAppliesCurrentTierToPastPeriodsAcceptedLimitation(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierA := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierA, 10_000, "2026-01")

	tierB := createDuesTier(t, q, f.fundID, "Tier B")
	createDuesRate(t, q, tierB, 50_000, "2026-01")

	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierB, joinedOn: &joinedOn})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-01")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Fatalf("OutstandingDuesForMember() returned %d rows, want 1", len(rows))
	}
	if rows[0].OwedAmount != 50_000 {
		t.Errorf("OwedAmount = %d, want 50000 - the CURRENT (Tier B) rate wrongly applied to January, "+
			"the same accepted limitation DuesStatusForPeriod carries (ADR-024), not a bug to fix here.",
			rows[0].OwedAmount)
	}
}

// --- ArrearsMonthsForMember (#233, ADR-032) --------------------------------
//
// These build periods relative to time.Now() rather than hard-coded
// calendar months, the same reasoning
// TestOutstandingDuesForMemberOmittedThroughDefaultsToCurrentMonth already
// gives: a fixture pinned to "2026-06" eventually becomes a fixture about
// the past, and this function's whole job is "relative to whatever period
// is current right now".

// arrearsPeriods bundles the four periods every test below needs relative
// to the moment it runs: two months before the current one, the month
// before it, the current one itself, and the month after it. Naming them
// once here keeps each test's own arithmetic readable.
type arrearsPeriods struct {
	twoBack, oneBack, current, ahead string
}

func newArrearsPeriods(t *testing.T) arrearsPeriods {
	t.Helper()
	now := time.Now()
	return arrearsPeriods{
		twoBack: now.AddDate(0, -2, 0).Format(duesPeriodLayout),
		oneBack: now.AddDate(0, -1, 0).Format(duesPeriodLayout),
		current: now.Format(duesPeriodLayout),
		ahead:   now.AddDate(0, 1, 0).Format(duesPeriodLayout),
	}
}

// A member who joined the current period owns no period strictly before
// it - ADR-032's own "including... a member who joined this period" case.
func TestArrearsMonthsForMemberJoinedThisPeriodReadsZero(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.current + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() = %v, want no error", err)
	}
	if got != 0 {
		t.Errorf("ArrearsMonthsForMember() = %d, want 0 (joined the current period, nothing owed before it)", got)
	}
}

// A member who has fully paid every period through one ahead of the current
// one - DuesStatusForPeriod's own "paid in advance" shape - has nothing
// outstanding for any period before the current one either, so the badge
// reads zero.
func TestArrearsMonthsForMemberPaidAheadReadsZero(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.twoBack + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-01-15",
		Periods: []PeriodAmount{
			{DuesPeriod: p.twoBack, Amount: 25_000},
			{DuesPeriod: p.oneBack, Amount: 25_000},
			{DuesPeriod: p.current, Amount: 25_000},
			{DuesPeriod: p.ahead, Amount: 25_000},
		},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() = %v, want no error", err)
	}
	if got != 0 {
		t.Errorf("ArrearsMonthsForMember() = %d, want 0 (every period before the current one is fully paid)", got)
	}
}

// A part-paid earlier period counts as one month, the same as an unpaid
// one (ADR-032's own table) - this is the acceptance criterion's own
// "Tunggakan 1 bulan" case.
func TestArrearsMonthsForMemberPartPaidEarlierPeriodReadsOne(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.oneBack + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-01-15",
		Periods: []PeriodAmount{
			{DuesPeriod: p.oneBack, Amount: 10_000}, // less than the 25000 owed: partial
		},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() = %v, want no error", err)
	}
	if got != 1 {
		t.Errorf("ArrearsMonthsForMember() = %d, want 1 (one part-paid period before the current one)", got)
	}
}

// Two periods before the current one, one unpaid and one partial, both
// count - the whole-months count treats them identically, exactly as
// OutstandingDuesForMember itself returns both as outstanding with no
// further classification.
func TestArrearsMonthsForMemberCountsUnpaidAndPartialTheSame(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.twoBack + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-01-15",
		Periods: []PeriodAmount{
			{DuesPeriod: p.oneBack, Amount: 10_000}, // partial
			// twoBack: left entirely unpaid
		},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() = %v, want no error", err)
	}
	if got != 2 {
		t.Errorf("ArrearsMonthsForMember() = %d, want 2 (one unpaid + one partial period before the current one)", got)
	}
}

// The current period is excluded entirely, by construction (ADR-032): a
// member who owes only the current period, and has paid nothing toward it,
// still reads zero arrears - the badge must never flip the whole roster to
// "owing" on the first of the month.
func TestArrearsMonthsForMemberCurrentPeriodAloneIsExcluded(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.current + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})
	// Nothing paid at all - the member owes exactly the current period and
	// nothing else, since they joined it.

	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() = %v, want no error", err)
	}
	if got != 0 {
		t.Errorf("ArrearsMonthsForMember() = %d, want 0 (only the current period is owed, and it is excluded)", got)
	}
}

// A tier-less member has no dues obligation at all (mirrors
// TestOutstandingDuesForMemberTierLessMemberReturnsEmpty).
func TestArrearsMonthsForMemberTierLessMemberReadsZero(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane"})

	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() = %v, want no error", err)
	}
	if got != 0 {
		t.Errorf("ArrearsMonthsForMember() = %d, want 0 (no tier, no dues obligation)", got)
	}
}

// PRD section 7.1: backdating a join date exposes arrears that were not
// there before, because it widens OutstandingDuesForMember's own walk back
// to an earlier start - proved directly against UpdateMember rather than
// through the HTTP layer, since this is the ledger derivation's own
// property, not the route's.
func TestArrearsMonthsForMemberBackdatingJoinedOnMakesArrearsAppear(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.current + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	before, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() before backdating = %v, want no error", err)
	}
	if before != 0 {
		t.Fatalf("ArrearsMonthsForMember() before backdating = %d, want 0", before)
	}

	backdated := p.twoBack + "-01"
	setTierID := int64(1)
	if _, err := q.UpdateMember(ctx, store.UpdateMemberParams{
		ID: memberID, SetJoinedOn: 1, JoinedOn: &backdated, SetTierID: setTierID, TierID: &tierID,
	}); err != nil {
		t.Fatalf("UpdateMember(backdate joined_on) = %v, want no error", err)
	}

	after, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, p.current)
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember() after backdating = %v, want no error", err)
	}
	if after != 2 {
		t.Errorf("ArrearsMonthsForMember() after backdating joined_on to %q = %d, want 2 "+
			"(twoBack and oneBack both now fall inside the member's window and are unpaid)", backdated, after)
	}
}

// Malformed currentPeriod is rejected the same way OutstandingDuesForMember
// rejects a malformed through.
func TestArrearsMonthsForMemberRejectsMalformedCurrentPeriod(t *testing.T) {
	for _, currentPeriod := range []string{"2026-13", "2026-1", "not-a-period", ""} {
		t.Run(currentPeriod, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			q := store.New(l.db)
			ctx := context.Background()

			tierID := createDuesTier(t, q, f.fundID, "Tier A")
			memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

			_, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, currentPeriod)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("ArrearsMonthsForMember(currentPeriod=%q) = %v, want an error wrapping ErrInvalidArgument", currentPeriod, err)
			}
		})
	}
}

// An unknown member id answers sql.ErrNoRows, mirroring
// TestOutstandingDuesForMemberUnknownMemberIsNotFound - this function calls
// OutstandingDuesForMember directly, so the same GetMemberForFund lookup
// backs it.
func TestArrearsMonthsForMemberUnknownMemberIsNotFound(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	_, err := l.ArrearsMonthsForMember(ctx, f.fundID, 999_999, p.current)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ArrearsMonthsForMember(unknown member) = %v, want an error wrapping sql.ErrNoRows", err)
	}
}

// The current-period exclusion is month arithmetic, and the one input that
// can break month arithmetic is January: "2026-01" minus a month has to
// reach December of the PREVIOUS year, not December of the same one, or the
// badge silently reports a year's worth of arrears every January. Every
// other arrears test derives its periods from time.Now() through
// newArrearsPeriods, so none of them exercises this unless the suite
// happens to run in January - hence a fixed-period test rather than a
// relative one.
func TestArrearsMonthsForMemberCrossesTheYearBoundary(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2025-01")
	joinedOn := "2025-11-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	// currentPeriod is January, so the walk must end at 2025-12: November and
	// December 2025 are owed and unpaid, January 2026 itself is excluded.
	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, "2026-01")
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember(2026-01) = %v, want no error", err)
	}
	if got != 2 {
		t.Errorf("ArrearsMonthsForMember(2026-01) for a member who joined 2025-11 = %d, want 2 "+
			"(2025-11 and 2025-12 owed; 2026-01 is the current period and excluded)", got)
	}
}
