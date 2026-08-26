package ledger

import (
	"context"
	"errors"
	"testing"

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
