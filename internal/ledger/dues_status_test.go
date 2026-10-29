package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
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

// duesMemberParams is the shape createDuesMember needs: the fields these
// tests vary. A nil joinedOn is longAgoJoinedOn, not NULL (#471, NOT NULL).
type duesMemberParams struct {
	name       string
	tierID     *int64
	joinedOn   *string
	inactiveOn *string
}

// longAgoJoinedOn is the join date of a member whose test does not care
// about the window: before every period any test here exercises.
const longAgoJoinedOn = "2000-01-01"

func createDuesMember(t *testing.T, q *store.Queries, fundID int64, p duesMemberParams) int64 {
	t.Helper()
	joinedOn := longAgoJoinedOn
	if p.joinedOn != nil {
		joinedOn = *p.joinedOn
	}
	m, err := q.CreateMember(context.Background(), store.CreateMemberParams{
		FundID: fundID, Name: p.name,
		JoinedOn: joinedOn, InactiveOn: p.inactiveOn, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(%q) = %v, want no error", p.name, err)
	}
	if p.tierID != nil {
		// The tier from the month of joined_on, what POST /api/members writes and
		// what the backfill gives a v1 member (ADR-040).
		setMemberTier(t, q, fundID, m.ID, p.tierID, joinedOn[:len(duesPeriodLayout)], 0)
	}
	return m.ID
}

// setMemberTier writes one member_tier row straight through the store, for tests
// building a history the engine would refuse or that they want spelled out: a
// tier from a month, at a given correction number. nil is "no tier from then".
func setMemberTier(t *testing.T, q *store.Queries, fundID, memberID int64, tierID *int64, from string, correction int64) {
	t.Helper()
	if _, err := q.CreateMemberTier(context.Background(), store.CreateMemberTierParams{
		FundID: fundID, MemberID: memberID, TierID: tierID, EffectiveFrom: from, Correction: correction, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateMemberTier(member=%d, from=%q, #%d) = %v, want no error", memberID, from, correction, err)
	}
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
	t.Parallel()
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
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
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
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
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

// Overpayment (paid more than the rate) reads as Paid, not a fifth status;
// the exact figure stays visible in PaidAmount.
func TestDuesStatusForPeriodOverpaymentReadsAsPaid(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
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

// Paid in advance is Paid for this period plus a later period also paid. A
// period that is itself unpaid or partial stays so even if the member paid
// ahead for another month.
func TestDuesStatusForPeriodPaidInAdvanceWhenALaterPeriodIsAlsoPaid(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	for _, period := range []string{"2026-06", "2026-08"} {
		if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID,
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

// A member who skipped a period still reads Unpaid for it though a later
// period was paid: "ahead" describes a settled period, not a hole.
func TestDuesStatusForPeriodSkippedPeriodStaysUnpaidDespiteALaterPayment(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	// Paid August, never July.
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
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

// Several months paid at once: the last is Paid and every earlier paid month
// reads Paid in advance.
func TestDuesStatusForPeriodSeveralMonthsPaidAtOnceShowCorrectlyPerPeriod(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	for _, period := range []string{"2026-06", "2026-07", "2026-08"} {
		if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID,
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
	t.Parallel()
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

// The undecided-rate case, PRD 6: a tier with no dues_rate row for the period.
// Uruni does not invent an amount, so the member is omitted.
func TestDuesStatusForPeriodMemberWhoseTierHasNoRateForThePeriodIsOmitted(t *testing.T) {
	t.Parallel()
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

// A tier whose rate takes effect after the queried period is the same
// omission, via a real effective_from.
func TestDuesStatusForPeriodMemberWhoseTierRateStartsAfterThePeriodIsOmitted(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// PRD 7.1's live-arrears exception backdates one member's joined_on and the
// tier's rate together. The backdated rate must reach only that member
// (#471: a NULL joined_on used to walk from the tier's earliest rate and
// pick up the backdated periods too).
func TestOutstandingDuesForMemberBackdatedRateReachesOnlyTheBackdatedMember(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2025-10") // backdated for Budi's arrears
	adoption, backdated := "2026-01-05", "2025-10-01"
	ani := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Ani", tierID: &tierID, joinedOn: &adoption})
	budi := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Budi", tierID: &tierID, joinedOn: &backdated})

	for _, tt := range []struct {
		name      string
		memberID  int64
		wantFirst string
		wantLen   int
	}{
		{"joined at adoption", ani, "2026-01", 3},
		{"backdated for arrears", budi, "2025-10", 6},
	} {
		rows, err := l.OutstandingDuesForMember(ctx, f.fundID, tt.memberID, "2026-03")
		if err != nil {
			t.Fatalf("%s: OutstandingDuesForMember() = %v, want no error", tt.name, err)
		}
		if len(rows) != tt.wantLen || rows[0].Period != tt.wantFirst {
			t.Errorf("%s: OutstandingDuesForMember() = %+v, want %d periods from %s", tt.name, rows, tt.wantLen, tt.wantFirst)
		}
	}
}

// --- malformed period --------------------------------------------------------

func TestDuesStatusForPeriodRejectsMalformedPeriod(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
	l := newTestLedger(t)
	f1 := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	// Mirrors TestASecondFundsRowsNeverAppearInTheFirstFundsBalances: a second
	// fund built by hand, since newFixture hard-codes the report_slug.
	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	f2cash := createAccount(t, q, other.ID, "cash", "Cash")
	createPurpose(t, q, other.ID, "main", "Main")

	tier1 := createDuesTier(t, q, f1.fundID, "Tier A")
	createDuesRate(t, q, tier1, 25_000, "2026-01")
	member1 := createDuesMember(t, q, f1.fundID, duesMemberParams{name: "Jane", tierID: &tier1})

	tier2 := createDuesTier(t, q, other.ID, "Tier A")
	createDuesRate(t, q, tier2, 99_000, "2026-01")
	member2 := createDuesMember(t, q, other.ID, duesMemberParams{name: "John", tierID: &tier2})
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: other.ID, AccountID: f2cash,
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

// --- a tier change does not reach back (ADR-040) --------------------------

// A member promoted mid-year is priced at the tier in force in each period:
// January comes back at Tier A's rate.
func TestDuesStatusForPeriodMidYearPromotionLeavesPastPeriodsAtTheOldTier(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierA := createDuesTier(t, q, f.fundID, "Tier A") // the low tier, in force until June
	createDuesRate(t, q, tierA, 10_000, "2026-01")
	tierB := createDuesTier(t, q, f.fundID, "Tier B") // the tier the member was promoted to in June
	createDuesRate(t, q, tierB, 50_000, "2026-01")

	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierA, joinedOn: &joinedOn})
	setMemberTier(t, q, f.fundID, memberID, &tierB, "2026-06", 0)

	for _, tt := range []struct {
		period   string
		wantOwed money.Amount
		wantTier int64
	}{
		{"2026-01", 10_000, tierA},
		{"2026-05", 10_000, tierA}, // the month before the promotion
		{"2026-06", 50_000, tierB}, // the promotion applies from its own month
		{"2026-12", 50_000, tierB},
	} {
		rows, err := l.DuesStatusForPeriod(ctx, f.fundID, tt.period)
		if err != nil {
			t.Fatalf("DuesStatusForPeriod(%s) = %v, want no error", tt.period, err)
		}
		got, ok := statusFor(t, rows, memberID)
		if !ok {
			t.Fatalf("member %d missing from %s's roster", memberID, tt.period)
		}
		if got.OwedAmount != tt.wantOwed {
			t.Errorf("%s: OwedAmount = %d, want %d (the tier in force then)", tt.period, got.OwedAmount, tt.wantOwed)
		}
		if got.Member.TierID == nil || *got.Member.TierID != tt.wantTier {
			t.Errorf("%s: the row's tier = %v, want %d - the tier that priced OwedAmount", tt.period, got.Member.TierID, tt.wantTier)
		}
	}
}

// --- OutstandingDuesForMember (#186) -----------------------------------------

// TestOutstandingDuesForMemberReturnsUnpaidAndPartialOldestFirstMatchingDuesStatusForPeriod
// is the core criterion: of three periods (unpaid, partial, paid), exactly
// the two outstanding come back, oldest first, with the amounts
// DuesStatusForPeriod reports; it is the same derivation.
func TestOutstandingDuesForMemberReturnsUnpaidAndPartialOldestFirstMatchingDuesStatusForPeriod(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	// 2026-01 stays unpaid; 2026-02 gets a partial payment; 2026-03 is paid in
	// full and must not appear.
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
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

	// Cross-check against DuesStatusForPeriod for the same two periods.
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
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
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
	t.Parallel()
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

// A tier that never had a rate has nothing owed: every period is skipped,
// none invented.
func TestOutstandingDuesForMemberRatelessTierReturnsEmpty(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Senior TBD") // deliberately no rate row
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{
		name: "Jane", tierID: &tierID,
	})

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-06")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("OutstandingDuesForMember() = %+v, want [] for a tier that never had a rate", rows)
	}
}

// A member who joins after `through` has no periods in range; the walk
// returns empty rather than iterating backwards.
func TestOutstandingDuesForMemberJoinedAfterThroughReturnsEmpty(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

	// through reaches well past inactive_on's month; the answer stops at 2026-03.
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

// A period whose tier has no effective rate is skipped and the range keeps
// walking: periods before the tier's first rate are omitted, and periods once
// it exists still appear.
func TestOutstandingDuesForMemberSkipsPeriodWithNoEffectiveRateWithoutTruncatingTheRange(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// An omitted through defaults to the current month in the fund's zone, not the
// host's (#379, ADR-041): production runs UTC, so on a month's last day from
// 17:00:00Z the WIB treasurer is in the next month, from 16:00:00Z the WITA one,
// and from 15:00:00Z the WIT one. The instants straddle each boundary by one
// second, a year boundary too, and 22:30 WIB is the instant the three zones
// disagree about.
func TestOutstandingDuesForMemberOmittedThroughDefaultsToTheFundsMonth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		zone string
		now  time.Time
		want string
	}{
		{"WIB: last second of the month", tz.Jakarta, time.Date(2026, 8, 31, 16, 59, 59, 0, time.UTC), "2026-08"},
		{"WIB: first second of the next month", tz.Jakarta, time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC), "2026-09"},
		{"WIB: year boundary, before", tz.Jakarta, time.Date(2026, 12, 31, 16, 59, 59, 0, time.UTC), "2026-12"},
		{"WIB: year boundary, after", tz.Jakarta, time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC), "2027-01"},
		{"WIB: same instant written in another zone", tz.Jakarta, time.Date(2026, 9, 1, 0, 0, 0, 0, time.FixedZone("UTC+7", 7*3600)), "2026-09"},
		{"WITA: last second of the month", tz.Makassar, time.Date(2026, 8, 31, 15, 59, 59, 0, time.UTC), "2026-08"},
		{"WITA: first second of the next month", tz.Makassar, time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC), "2026-09"},
		{"WIT: last second of the month", tz.Jayapura, time.Date(2026, 8, 31, 14, 59, 59, 0, time.UTC), "2026-08"},
		{"WIT: first second of the next month", tz.Jayapura, time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC), "2026-09"},
		{"WIT: year boundary", tz.Jayapura, time.Date(2026, 12, 31, 15, 0, 0, 0, time.UTC), "2027-01"},
		// 22:30 WIB on 31 August: still August for WIB and WITA (23:30), already
		// September for WIT (00:30).
		{"22:30 WIB read by a WIB fund", tz.Jakarta, time.Date(2026, 8, 31, 22, 30, 0, 0, wib), "2026-08"},
		{"22:30 WIB read by a WITA fund", tz.Makassar, time.Date(2026, 8, 31, 22, 30, 0, 0, wib), "2026-08"},
		{"22:30 WIB read by a WIT fund", tz.Jayapura, time.Date(2026, 8, 31, 22, 30, 0, 0, wib), "2026-09"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			q := store.New(l.db)
			setFundZone(t, q, f.fundID, tc.zone)

			tierID := createDuesTier(t, q, f.fundID, "Tier A")
			createDuesRate(t, q, tierID, 25_000, "2020-01")
			memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID}) // joined long before every period here

			rows, err := l.at(tc.now).OutstandingDuesForMember(context.Background(), f.fundID, memberID, "")
			if err != nil {
				t.Fatalf("OutstandingDuesForMember(through=\"\", now=%s) = %v, want no error", tc.now.Format(time.RFC3339), err)
			}
			if len(rows) == 0 {
				t.Fatal("expected outstanding periods (member has never paid since 2020), got none")
			}
			if got := rows[len(rows)-1].Period; got != tc.want {
				t.Errorf("last outstanding period at %s = %q, want %q", tc.now.Format(time.RFC3339), got, tc.want)
			}
			if got := CurrentDuesPeriod(tc.now, zoneLoc(t, tc.zone)); got != tc.want {
				t.Errorf("CurrentDuesPeriod(%s, %s) = %q, want %q", tc.now.Format(time.RFC3339), tc.zone, got, tc.want)
			}
		})
	}
}

func TestOutstandingDuesForMemberRejectsMalformedThrough(t *testing.T) {
	t.Parallel()
	for _, through := range []string{"2026-13", "2026-1", "not-a-period"} {
		t.Run(through, func(t *testing.T) {
			t.Parallel()
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

// An unknown member id, including another fund's (GetMemberForFund makes them
// indistinguishable), answers sql.ErrNoRows, which mapLedgerError turns into
// 404; this proves the ledger method does not swallow it.
func TestOutstandingDuesForMemberUnknownMemberIsNotFound(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.OutstandingDuesForMember(ctx, f.fundID, 999_999, "2026-06")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("OutstandingDuesForMember(unknown member) = %v, want an error wrapping sql.ErrNoRows", err)
	}
}

// January stays at the tier the member was in then, and the rows after the
// promotion carry the new one.
func TestOutstandingDuesForMemberMidYearPromotionLeavesPastPeriodsAtTheOldTier(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierA := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierA, 10_000, "2026-01")
	tierB := createDuesTier(t, q, f.fundID, "Tier B")
	createDuesRate(t, q, tierB, 50_000, "2026-01")

	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierA, joinedOn: &joinedOn})
	setMemberTier(t, q, f.fundID, memberID, &tierB, "2026-04", 0)

	rows, err := l.OutstandingDuesForMember(ctx, f.fundID, memberID, "2026-05")
	if err != nil {
		t.Fatalf("OutstandingDuesForMember() = %v, want no error", err)
	}
	want := []OutstandingDuesPeriod{
		{Period: "2026-01", OwedAmount: 10_000, Status: DuesStatusUnpaid},
		{Period: "2026-02", OwedAmount: 10_000, Status: DuesStatusUnpaid},
		{Period: "2026-03", OwedAmount: 10_000, Status: DuesStatusUnpaid},
		{Period: "2026-04", OwedAmount: 50_000, Status: DuesStatusUnpaid},
		{Period: "2026-05", OwedAmount: 50_000, Status: DuesStatusUnpaid},
	}
	if len(rows) != len(want) {
		t.Fatalf("OutstandingDuesForMember() = %+v, want %d rows", rows, len(want))
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}

// --- ArrearsMonthsForMember (#233, ADR-032) --------------------------------
//
// These build periods relative to time.Now(), not hard-coded months: a
// fixture pinned to "2026-06" eventually becomes a fixture about the past.

// arrearsPeriods bundles the four periods every test below needs relative to
// now: two months before the current one, the month before it, the current
// one, and the month after it.
type arrearsPeriods struct {
	twoBack, oneBack, current, ahead string
}

func newArrearsPeriods(t *testing.T) arrearsPeriods {
	t.Helper()
	// First of the month in Jakarta (#379): no host-zone dependence, and no
	// AddDate overflow from the 31st.
	now := time.Now().In(wib)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, wib)
	return arrearsPeriods{
		twoBack: first.AddDate(0, -2, 0).Format(duesPeriodLayout),
		oneBack: first.AddDate(0, -1, 0).Format(duesPeriodLayout),
		current: first.Format(duesPeriodLayout),
		ahead:   first.AddDate(0, 1, 0).Format(duesPeriodLayout),
	}
}

// A member who joined the current period owes no period strictly before it
// (ADR-032: "a member who joined this period").
func TestArrearsMonthsForMemberJoinedThisPeriodReadsZero(t *testing.T) {
	t.Parallel()
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
// one has nothing outstanding before the current one, so the badge reads zero.
func TestArrearsMonthsForMemberPaidAheadReadsZero(t *testing.T) {
	t.Parallel()
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
		FundID: f.fundID, AccountID: f.cashID,
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

// A part-paid earlier period counts as one month, like an unpaid one
// (ADR-032): the "Tunggakan 1 bulan" case.
func TestArrearsMonthsForMemberPartPaidEarlierPeriodReadsOne(t *testing.T) {
	t.Parallel()
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
		FundID: f.fundID, AccountID: f.cashID,
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

// Two periods before the current one, one unpaid and one partial, both count
// identically.
func TestArrearsMonthsForMemberCountsUnpaidAndPartialTheSame(t *testing.T) {
	t.Parallel()
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
		FundID: f.fundID, AccountID: f.cashID,
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

// The current period is excluded by construction (ADR-032): a member owing
// only the current period still reads zero arrears, so the badge never flips
// the whole roster to "owing" on the first of the month.
func TestArrearsMonthsForMemberCurrentPeriodAloneIsExcluded(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2020-01")
	joinedOn := p.current + "-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})
	// Nothing paid: the member owes exactly the current period, since they
	// joined it.

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
	t.Parallel()
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

// PRD section 7.1: backdating a join date exposes arrears that were not there
// before, because it widens OutstandingDuesForMember's walk - and, since the
// tier is month by month, only because UpdateMember moves the member's first
// tier row back with the join date (ADR-040). Proved against the ledger
// directly, as this is the derivation's property, not the route's.
func TestArrearsMonthsForMemberBackdatingJoinedOnMakesArrearsAppear(t *testing.T) {
	t.Parallel()
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
	if _, err := l.UpdateMember(ctx, UpdateMemberParams{
		FundID: f.fundID, MemberID: memberID, JoinedOn: &backdated,
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
	t.Parallel()
	for _, currentPeriod := range []string{"2026-13", "2026-1", "not-a-period", ""} {
		t.Run(currentPeriod, func(t *testing.T) {
			t.Parallel()
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

// An unknown member id answers sql.ErrNoRows, as
// TestOutstandingDuesForMemberUnknownMemberIsNotFound; the same
// GetMemberForFund lookup backs it.
func TestArrearsMonthsForMemberUnknownMemberIsNotFound(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	p := newArrearsPeriods(t)

	_, err := l.ArrearsMonthsForMember(ctx, f.fundID, 999_999, p.current)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ArrearsMonthsForMember(unknown member) = %v, want an error wrapping sql.ErrNoRows", err)
	}
}

// The current-period exclusion is month arithmetic, and January is the input
// that can break it: "2026-01" minus a month must reach December of the
// PREVIOUS year, or the badge reports a year's arrears every January. The
// other arrears tests derive periods from time.Now(), so none covers this
// unless the suite runs in January; hence a fixed-period test.
func TestArrearsMonthsForMemberCrossesTheYearBoundary(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2025-01")
	joinedOn := "2025-11-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID, joinedOn: &joinedOn})

	// currentPeriod is January, so the walk ends at 2025-12: November and
	// December 2025 are owed and unpaid, January 2026 is excluded.
	got, err := l.ArrearsMonthsForMember(ctx, f.fundID, memberID, "2026-01")
	if err != nil {
		t.Fatalf("ArrearsMonthsForMember(2026-01) = %v, want no error", err)
	}
	if got != 2 {
		t.Errorf("ArrearsMonthsForMember(2026-01) for a member who joined 2025-11 = %d, want 2 "+
			"(2025-11 and 2025-12 owed; 2026-01 is the current period and excluded)", got)
	}
}

// --- paid through (#357) ------------------------------------------------------

// paidThroughFixture is one member on a 25k tier with the given periods paid
// in full, read back for October 2026.
type paidThroughFixture struct {
	l        *Ledger
	f        fixture
	q        *store.Queries
	tierID   int64
	memberID int64
	posted   []store.Transaction
}

func newPaidThroughFixture(t *testing.T, p duesMemberParams, paid ...string) paidThroughFixture {
	t.Helper()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	p.name, p.tierID = "Jane", &tierID
	memberID := createDuesMember(t, q, f.fundID, p)
	periods := make([]PeriodAmount, 0, len(paid))
	for _, period := range paid {
		periods = append(periods, PeriodAmount{DuesPeriod: period, Amount: 25_000})
	}
	posted, err := l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID,
		MemberID: memberID, OccurredOn: "2026-10-01", Periods: periods,
	})
	if err != nil {
		t.Fatalf("PostDuesPayments = %v, want no error", err)
	}
	return paidThroughFixture{l: l, f: f, q: q, tierID: tierID, memberID: memberID, posted: posted}
}

func (p paidThroughFixture) october(t *testing.T) MemberDuesStatus {
	t.Helper()
	rows, err := p.l.DuesStatusForPeriod(context.Background(), p.f.fundID, "2026-10")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod = %v, want no error", err)
	}
	got, ok := statusFor(t, rows, p.memberID)
	if !ok {
		t.Fatalf("member %d missing from roster", p.memberID)
	}
	return got
}

func TestDuesStatusForPeriodPaidThroughIsTheLastPeriodOfAnUnbrokenRun(t *testing.T) {
	t.Parallel()
	p := newPaidThroughFixture(t, duesMemberParams{}, "2026-10", "2026-11", "2026-12", "2027-01", "2027-02")

	got := p.october(t)
	if got.Status != DuesStatusPaidInAdvance || got.PaidThrough != "2027-02" {
		t.Errorf("Status, PaidThrough = %q, %q; want %q, \"2027-02\" - five months paid at once, across a year end", got.Status, got.PaidThrough, DuesStatusPaidInAdvance)
	}
}

func TestDuesStatusForPeriodPaidThroughStopsAtASkippedPeriod(t *testing.T) {
	t.Parallel()
	p := newPaidThroughFixture(t, duesMemberParams{}, "2026-10", "2026-11", "2027-01")

	got := p.october(t)
	if got.PaidThrough != "2026-11" {
		t.Errorf("PaidThrough = %q, want \"2026-11\" - December is unpaid, so January does not count", got.PaidThrough)
	}
}

func TestDuesStatusForPeriodPaidThroughIsEmptyWhenTheNextPeriodIsTheHole(t *testing.T) {
	t.Parallel()
	p := newPaidThroughFixture(t, duesMemberParams{}, "2026-10", "2026-12")

	got := p.october(t)
	if got.Status != DuesStatusPaidInAdvance || got.PaidThrough != "" {
		t.Errorf("Status, PaidThrough = %q, %q; want %q, \"\" - ahead, but not through any later month", got.Status, got.PaidThrough, DuesStatusPaidInAdvance)
	}
}

func TestDuesStatusForPeriodPaidThroughStopsWhereARateRiseLeavesAPeriodShort(t *testing.T) {
	t.Parallel()
	p := newPaidThroughFixture(t, duesMemberParams{}, "2026-10", "2026-11", "2026-12")
	createDuesRate(t, p.q, p.tierID, 30_000, "2026-12")

	got := p.october(t)
	if got.PaidThrough != "2026-11" {
		t.Errorf("PaidThrough = %q, want \"2026-11\" - December was paid at 25k against a 30k rate", got.PaidThrough)
	}
}

func TestDuesStatusForPeriodPaidThroughIgnoresAReversedPayment(t *testing.T) {
	t.Parallel()
	p := newPaidThroughFixture(t, duesMemberParams{}, "2026-10", "2026-11", "2026-12")
	ctx := context.Background()

	december := p.posted[2] // posted in Periods order
	if _, err := p.l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: p.f.fundID, TransactionID: december.ID}); err != nil {
		t.Fatalf("ReverseTransaction = %v, want no error", err)
	}

	got := p.october(t)
	if got.PaidThrough != "2026-11" {
		t.Errorf("PaidThrough = %q, want \"2026-11\" - December's payment was reversed", got.PaidThrough)
	}
}

func TestDuesStatusForPeriodPaidThroughEndsAtTheMonthTheMemberLeft(t *testing.T) {
	t.Parallel()
	inactive := "2026-11-15"
	p := newPaidThroughFixture(t, duesMemberParams{inactiveOn: &inactive}, "2026-10", "2026-11", "2026-12")

	got := p.october(t)
	if got.PaidThrough != "2026-11" {
		t.Errorf("PaidThrough = %q, want \"2026-11\" - nothing is owed after the month she left", got.PaidThrough)
	}
}
