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

// ADR-050: a waiver is the group's decision that a month stops being owed. These
// tests hold the ledger half - who may be waived, what a waived month reads as on
// every dues surface, and that no money ever moves.

const waiverRate money.Amount = 10_000

// waiverWorld is a fund whose clock reads 15 August 2026 and one member, Siti,
// joined 1 January 2026 on a tier of Rp10.000 a month.
type waiverWorld struct {
	l      *Ledger // the ledger standing on 15 August 2026
	f      fixture
	q      *store.Queries
	member int64
	tier   int64
}

func newWaiverWorld(t *testing.T) waiverWorld {
	t.Helper()
	base := newTestLedger(t)
	f := newFixture(t, base)
	q := store.New(base.db)
	tier := createDuesTier(t, q, f.fundID, "Reguler")
	createDuesRate(t, q, tier, waiverRate, "2025-01")
	joined := "2026-01-01"
	member := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Siti", tierID: &tier, joinedOn: &joined})
	return waiverWorld{l: base.at(onTheDay("2026-08-15")), f: f, q: q, member: member, tier: tier}
}

// pay records what the member paid toward each period, one payment per call.
func (w waiverWorld) pay(t *testing.T, amount money.Amount, periods ...string) []store.Transaction {
	t.Helper()
	rows := make([]PeriodAmount, len(periods))
	for i, p := range periods {
		rows[i] = PeriodAmount{DuesPeriod: p, Amount: amount}
	}
	posted, err := w.l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: w.f.fundID, AccountID: w.f.cashID, MemberID: w.member, OccurredOn: "2026-08-10", Periods: rows,
	})
	if err != nil {
		t.Fatalf("PostDuesPayments(%v) = %v, want no error", periods, err)
	}
	return posted
}

func (w waiverWorld) waive(t *testing.T, note *string, periods ...string) []store.DuesWaiver {
	t.Helper()
	rows, err := w.l.WaiveDues(context.Background(), WaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Periods: periods, Note: note})
	if err != nil {
		t.Fatalf("WaiveDues(%v) = %v, want no error", periods, err)
	}
	return rows
}

func (w waiverWorld) waiveErr(periods ...string) error {
	_, err := w.l.WaiveDues(context.Background(), WaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Periods: periods})
	return err
}

func (w waiverWorld) status(t *testing.T, period string) MemberDuesStatus {
	t.Helper()
	rows, err := w.l.DuesStatusForPeriod(context.Background(), w.f.fundID, period)
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(%s) = %v, want no error", period, err)
	}
	s, ok := statusFor(t, rows, w.member)
	if !ok {
		t.Fatalf("DuesStatusForPeriod(%s) has no row for the member", period)
	}
	return s
}

func (w waiverWorld) outstanding(t *testing.T, through string) []string {
	t.Helper()
	rows, err := w.l.OutstandingDuesForMember(context.Background(), w.f.fundID, w.member, through)
	if err != nil {
		t.Fatalf("OutstandingDuesForMember(%s) = %v, want no error", through, err)
	}
	var periods []string
	for _, r := range rows {
		periods = append(periods, r.Period)
	}
	return periods
}

func (w waiverWorld) cell(t *testing.T, period string) DuesGridCell {
	t.Helper()
	grid, err := w.l.DuesGrid(context.Background(), w.f.fundID, "")
	if err != nil {
		t.Fatalf("DuesGrid() = %v, want no error", err)
	}
	for i, p := range grid.Periods {
		if p != period {
			continue
		}
		for _, row := range grid.Rows {
			if row.MemberID == w.member {
				return row.Cells[i]
			}
		}
	}
	t.Fatalf("DuesGrid() has no cell for %s", period)
	return ""
}

// --- who may be waived ----------------------------------------------------------

func TestWaiveDuesEligibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, w waiverWorld)
		periods []string
		want    error // nil: waived
	}{
		{"an unpaid month in arrears", nil, []string{"2026-03"}, nil},
		{"the current month", nil, []string{"2026-08"}, nil},
		{"the member's first month", nil, []string{"2026-01"}, nil},
		{"a month ahead", nil, []string{"2026-09"}, nil},
		{"the twelfth month ahead, as the payment form offers", nil, []string{"2027-08"}, nil},
		{"the thirteenth month ahead", nil, []string{"2027-09"}, ErrDateOutOfRange},
		{"a partly paid month", func(t *testing.T, w waiverWorld) { w.pay(t, 4_000, "2026-04") }, []string{"2026-04"}, nil},
		{"a fully paid month", func(t *testing.T, w waiverWorld) { w.pay(t, waiverRate, "2026-04") }, []string{"2026-04"}, ErrDuesNotOwed},
		{"an overpaid month", func(t *testing.T, w waiverWorld) { w.pay(t, 25_000, "2026-04") }, []string{"2026-04"}, ErrDuesNotOwed},
		{"a month paid in advance", func(t *testing.T, w waiverWorld) { w.pay(t, waiverRate, "2026-09") }, []string{"2026-09"}, ErrDuesNotOwed},
		{"a month before the member joined", nil, []string{"2025-12"}, ErrDuesNotOwed},
		{"a month already waived", func(t *testing.T, w waiverWorld) { w.waive(t, nil, "2026-04") }, []string{"2026-04"}, ErrDuesAlreadyWaived},
		{"a malformed month", nil, []string{"2026-13"}, ErrInvalidArgument},
		{"a month before the floor", nil, []string{"1999-12"}, ErrDateOutOfRange},
		{"no month", nil, nil, ErrInvalidArgument},
		{"the same month twice", nil, []string{"2026-04", "2026-04"}, ErrInvalidArgument},
		{"several months owed", nil, []string{"2026-03", "2026-04", "2026-05"}, nil},
		{"one owed and one paid: all or nothing", func(t *testing.T, w waiverWorld) { w.pay(t, waiverRate, "2026-05") },
			[]string{"2026-03", "2026-05"}, ErrDuesNotOwed},
		{"one owed and one already waived: all or nothing", func(t *testing.T, w waiverWorld) { w.waive(t, nil, "2026-05") },
			[]string{"2026-03", "2026-05"}, ErrDuesAlreadyWaived},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWaiverWorld(t)
			if tc.setup != nil {
				tc.setup(t, w)
			}
			before, err := w.q.ListDuesWaiversByMember(context.Background(), store.ListDuesWaiversByMemberParams{FundID: w.f.fundID, MemberID: w.member})
			if err != nil {
				t.Fatal(err)
			}

			err = w.waiveErr(tc.periods...)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("WaiveDues(%v) = %v, want %v", tc.periods, err, tc.want)
			}

			after, err := w.q.ListDuesWaiversByMember(context.Background(), store.ListDuesWaiversByMemberParams{FundID: w.f.fundID, MemberID: w.member})
			if err != nil {
				t.Fatal(err)
			}
			wantRows := len(before)
			if tc.want == nil {
				wantRows += len(tc.periods)
			}
			if len(after) != wantRows {
				t.Errorf("waivers after = %d, want %d (a refusal leaves none of a batch behind)", len(after), wantRows)
			}
		})
	}
}

func TestWaiveDuesRefusesAMonthOutsideTheActiveWindow(t *testing.T) {
	t.Parallel()
	w := newWaiverWorld(t)
	left := "2026-05-20"
	gone := createDuesMember(t, w.q, w.f.fundID, duesMemberParams{name: "Rina", tierID: &w.tier, joinedOn: strp("2026-01-01"), inactiveOn: &left})

	for _, period := range []string{"2026-06", "2026-08"} {
		_, err := w.l.WaiveDues(context.Background(), WaiveDuesParams{FundID: w.f.fundID, MemberID: gone, Periods: []string{period}})
		if !errors.Is(err, ErrDuesNotOwed) {
			t.Errorf("WaiveDues(%s) after the member left = %v, want ErrDuesNotOwed", period, err)
		}
	}
	// The month holding inactive_on is still theirs.
	if _, err := w.l.WaiveDues(context.Background(), WaiveDuesParams{FundID: w.f.fundID, MemberID: gone, Periods: []string{"2026-05"}}); err != nil {
		t.Errorf("WaiveDues(the month they left) = %v, want no error", err)
	}
}

func TestWaiveDuesRefusesAMemberWithNoDuesToWaive(t *testing.T) {
	t.Parallel()
	w := newWaiverWorld(t)
	free := createDuesMember(t, w.q, w.f.fundID, duesMemberParams{name: "Tanpa kategori", joinedOn: strp("2026-01-01")})
	undecided := createDuesTier(t, w.q, w.f.fundID, "Belum ada tarif")
	undecidedMember := createDuesMember(t, w.q, w.f.fundID, duesMemberParams{name: "Tarif belum ada", tierID: &undecided, joinedOn: strp("2026-01-01")})

	for name, id := range map[string]int64{"no tier": free, "a tier with no rate": undecidedMember} {
		_, err := w.l.WaiveDues(context.Background(), WaiveDuesParams{FundID: w.f.fundID, MemberID: id, Periods: []string{"2026-03"}})
		if !errors.Is(err, ErrDuesNotOwed) {
			t.Errorf("WaiveDues for a member with %s = %v, want ErrDuesNotOwed", name, err)
		}
	}
}

func TestWaiveDuesIsFundScoped(t *testing.T) {
	t.Parallel()
	w := newWaiverWorld(t)
	other := newFixture2(t, w.l)

	for name, p := range map[string]WaiveDuesParams{
		"another fund's id":     {FundID: other, MemberID: w.member, Periods: []string{"2026-03"}},
		"a member that is none": {FundID: w.f.fundID, MemberID: 9999, Periods: []string{"2026-03"}},
	} {
		if _, err := w.l.WaiveDues(context.Background(), p); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("WaiveDues(%s) = %v, want sql.ErrNoRows (a 404)", name, err)
		}
	}
	err := w.l.UnwaiveDues(context.Background(), UnwaiveDuesParams{FundID: other, MemberID: w.member, Period: "2026-03"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UnwaiveDues(another fund's member) = %v, want sql.ErrNoRows", err)
	}
}

func TestWaiveDuesKeepsTheNoteTrimmedAndBlankAsNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		note *string
		want *string
	}{
		{"a note", strp("  Rumah kebakaran \n"), strp("Rumah kebakaran")},
		{"a blank note", strp("   "), nil},
		{"an empty note", strp(""), nil},
		{"no note", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWaiverWorld(t)
			rows := w.waive(t, tc.note, "2026-03", "2026-04")
			if len(rows) != 2 {
				t.Fatalf("WaiveDues created %d rows, want one per month", len(rows))
			}
			for _, r := range rows {
				if !reflect.DeepEqual(r.Note, tc.want) {
					t.Errorf("waiver %s note = %v, want %v", r.DuesPeriod, deref(r.Note), deref(tc.want))
				}
			}
			if got := w.status(t, "2026-03").WaiverNote; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("status note = %v, want %v", deref(got), deref(tc.want))
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// --- what a waived month reads as -----------------------------------------------

func TestAWaivedMonthReadsWaivedOnEveryDuesSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	w.pay(t, 4_000, "2026-04") // partly paid, then waived
	w.waive(t, strp("Kehilangan pekerjaan"), "2026-03", "2026-04", "2026-09")

	// The status roster: waived, with what was paid, never paid or unpaid.
	for period, wantPaid := range map[string]money.Amount{"2026-03": 0, "2026-04": 4_000, "2026-09": 0} {
		s := w.status(t, period)
		if s.Status != DuesStatusWaived || s.PaidAmount != wantPaid || s.OwedAmount != waiverRate {
			t.Errorf("status %s = %s owed %d paid %d, want waived, owed %d, paid %d", period, s.Status, s.OwedAmount, s.PaidAmount, waiverRate, wantPaid)
		}
		if s.WaiverNote == nil || *s.WaiverNote != "Kehilangan pekerjaan" {
			t.Errorf("status %s note = %v, want the waiver's note", period, deref(s.WaiverNote))
		}
	}
	if s := w.status(t, "2026-05"); s.Status != DuesStatusUnpaid || s.WaiverNote != nil {
		t.Errorf("status of an unwaived month = %s, note %v, want unpaid and no note", s.Status, deref(s.WaiverNote))
	}

	// The payment form's months: the waived ones are not offered.
	got := w.outstanding(t, "2026-09")
	want := []string{"2026-01", "2026-02", "2026-05", "2026-06", "2026-07", "2026-08"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OutstandingDuesForMember = %v, want %v", got, want)
	}

	// The roster badge: arrears count the months before August that are still owed.
	arrears, err := w.l.ArrearsMonthsForMember(ctx, w.f.fundID, w.member, "2026-08")
	if err != nil {
		t.Fatal(err)
	}
	if arrears != 5 {
		t.Errorf("ArrearsMonthsForMember = %d, want 5: January, February, May, June, July", arrears)
	}

	// The twelve-month grid.
	if c := w.cell(t, "2026-04"); c != DuesCellWaived {
		t.Errorf("grid cell of the waived, partly paid April = %q, want waived", c)
	}
	if c := w.cell(t, "2026-05"); c != DuesCellUnpaid {
		t.Errorf("grid cell of May = %q, want unpaid", c)
	}
}

func TestArrearsAndTheRosterBatchLeaveWaivedMonthsOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	members, err := w.l.q.ListMembersByFund(ctx, w.f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	count := func() (one, batch int) {
		t.Helper()
		var err error
		if one, err = w.l.ArrearsMonthsForMember(ctx, w.f.fundID, w.member, "2026-08"); err != nil {
			t.Fatal(err)
		}
		m, err := w.l.ArrearsMonthsForMembers(ctx, w.f.fundID, members, "2026-08")
		if err != nil {
			t.Fatal(err)
		}
		return one, m[w.member]
	}

	if one, batch := count(); one != 7 || batch != 7 { // January through July
		t.Fatalf("arrears before any waiver = %d (batch %d), want 7", one, batch)
	}
	w.waive(t, nil, "2026-02", "2026-05", "2026-08") // August is not before August: no effect on the count
	if one, batch := count(); one != 5 || batch != 5 {
		t.Errorf("arrears with two past months waived = %d (batch %d), want 5", one, batch)
	}
}

func TestAWaivedMonthEndsThePaidThroughRun(t *testing.T) {
	t.Parallel()
	w := newWaiverWorld(t)
	w.pay(t, waiverRate, "2026-06", "2026-07", "2026-09") // Lunas, Lunas, [August], Lunas
	if got := w.status(t, "2026-06"); got.Status != DuesStatusPaidInAdvance || got.PaidThrough != "2026-07" {
		t.Fatalf("June with August unpaid = %s through %q, want paid in advance through 2026-07", got.Status, got.PaidThrough)
	}
	w.pay(t, waiverRate, "2026-08")
	if got := w.status(t, "2026-06"); got.PaidThrough != "2026-09" {
		t.Fatalf("June with August paid = through %q, want 2026-09", got.PaidThrough)
	}

	// Waiving needs an owed month, so start again with August the gap.
	w = newWaiverWorld(t)
	w.pay(t, waiverRate, "2026-06", "2026-07", "2026-09")
	w.waive(t, nil, "2026-08")
	got := w.status(t, "2026-06")
	if got.Status != DuesStatusPaidInAdvance || got.PaidThrough != "2026-07" {
		t.Errorf("June, July Lunas, August Diputihkan, September Lunas = %s through %q, want paid in advance through 2026-07: nobody paid August", got.Status, got.PaidThrough)
	}
	if got := w.status(t, "2026-08"); got.Status != DuesStatusWaived {
		t.Errorf("August = %s, want waived (never paid, whatever came after)", got.Status)
	}
}

func TestAWaivedMonthStaysWaivedWhenItsPaymentIsReversed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	posted := w.pay(t, 4_000, "2026-04")
	w.waive(t, nil, "2026-04")

	if _, err := w.l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: w.f.fundID, TransactionID: posted[0].ID}); err != nil {
		t.Fatalf("ReverseTransaction() = %v, want no error", err)
	}
	if s := w.status(t, "2026-04"); s.Status != DuesStatusWaived || s.PaidAmount != 0 {
		t.Errorf("April after its payment was reversed = %s paid %d, want waived and nothing paid", s.Status, s.PaidAmount)
	}
	if got := w.outstanding(t, "2026-04"); containsPeriod(got, "2026-04") {
		t.Errorf("outstanding = %v, want April still not offered", got)
	}
}

func containsPeriod(periods []string, p string) bool {
	for _, x := range periods {
		if x == p {
			return true
		}
	}
	return false
}

func TestARateOrTierChangeLeavesAWaivedMonthWaived(t *testing.T) {
	t.Parallel()
	w := newWaiverWorld(t)
	w.waive(t, nil, "2026-03", "2026-04")

	// A cheaper rate from March, and a move to no tier from May.
	createDuesRate(t, w.q, w.tier, 5_000, "2026-03")
	setMemberTier(t, w.q, w.f.fundID, w.member, nil, "2026-05", 0)

	if s := w.status(t, "2026-03"); s.Status != DuesStatusWaived || s.OwedAmount != 5_000 {
		t.Errorf("March after a rate change = %s owed %d, want waived at the new rate", s.Status, s.OwedAmount)
	}
	if s := w.status(t, "2026-04"); s.Status != DuesStatusWaived {
		t.Errorf("April = %s, want waived", s.Status)
	}
	// Fully paid at the new rate would have read paid; the waiver outranks it.
	w.waive(t, nil, "2026-02")
	createDuesRate(t, w.q, w.tier, 0, "2026-02")
	if s := w.status(t, "2026-02"); s.Status != DuesStatusWaived {
		t.Errorf("a waived month whose rate fell to Rp0 = %s, want waived, not paid", s.Status)
	}
}

func TestAWaiverOutsideTheWindowHasNoEffectAndComesBackWithTheMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	w.waive(t, nil, "2026-07", "2026-08")

	left := "2026-05-31"
	if _, err := w.l.UpdateMember(ctx, UpdateMemberParams{FundID: w.f.fundID, MemberID: w.member, SetInactiveOn: true, InactiveOn: &left}); err != nil {
		t.Fatalf("UpdateMember(inactive) = %v, want no error", err)
	}
	rows, err := w.l.DuesStatusForPeriod(ctx, w.f.fundID, "2026-07")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := statusFor(t, rows, w.member); ok {
		t.Error("the member left in May and still has a July row")
	}
	if got := w.outstanding(t, "2026-09"); containsPeriod(got, "2026-07") || containsPeriod(got, "2026-08") {
		t.Errorf("outstanding = %v, want nothing after they left", got)
	}

	if _, err := w.l.UpdateMember(ctx, UpdateMemberParams{FundID: w.f.fundID, MemberID: w.member, SetInactiveOn: true, InactiveOn: nil}); err != nil {
		t.Fatalf("UpdateMember(reactivate) = %v, want no error", err)
	}
	if s := w.status(t, "2026-07"); s.Status != DuesStatusWaived {
		t.Errorf("July after they came back = %s, want waived again", s.Status)
	}
}

// --- taking money for a waived month --------------------------------------------

func TestPostDuesPaymentsRefusesAWaivedMonthByName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	w.waive(t, nil, "2026-04")

	_, err := w.l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: w.f.fundID, AccountID: w.f.cashID, MemberID: w.member, OccurredOn: "2026-08-10",
		Periods: []PeriodAmount{{DuesPeriod: "2026-03", Amount: waiverRate}, {DuesPeriod: "2026-04", Amount: waiverRate}},
	})
	if !errors.Is(err, ErrDuesPeriodWaived) {
		t.Fatalf("PostDuesPayments(an owed month and a waived one) = %v, want ErrDuesPeriodWaived", err)
	}
	if n := transactionCount(t, w.l, w.f.fundID); n != 0 {
		t.Errorf("rows after the refusal = %d, want none (the owed month is not posted either)", n)
	}
	if n := duesPaymentCount(t, w.l, w.f.fundID); n != 0 {
		t.Errorf("payments after the refusal = %d, want none", n)
	}

	// Another member's April is not the waived one.
	other := createDuesMember(t, w.q, w.f.fundID, duesMemberParams{name: "Budi", tierID: &w.tier, joinedOn: strp("2026-01-01")})
	if _, err := w.l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: w.f.fundID, AccountID: w.f.cashID, MemberID: other, OccurredOn: "2026-08-10",
		Periods: []PeriodAmount{{DuesPeriod: "2026-04", Amount: waiverRate}},
	}); err != nil {
		t.Errorf("PostDuesPayments(another member's April) = %v, want no error", err)
	}

	// Un-waive first, then the month takes money like any other.
	if err := w.l.UnwaiveDues(ctx, UnwaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Period: "2026-04"}); err != nil {
		t.Fatal(err)
	}
	w.pay(t, waiverRate, "2026-04")
	if s := w.status(t, "2026-04"); s.Status != DuesStatusPaid {
		t.Errorf("April after un-waiving and paying = %s, want paid", s.Status)
	}
}

// --- taking a waiver back --------------------------------------------------------

func TestUnwaiveDuesRestoresWhatWasOwed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	w.pay(t, 4_000, "2026-04")
	w.waive(t, strp("sementara"), "2026-03", "2026-04")

	if err := w.l.UnwaiveDues(ctx, UnwaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Period: "2026-04"}); err != nil {
		t.Fatalf("UnwaiveDues() = %v, want no error", err)
	}
	if s := w.status(t, "2026-04"); s.Status != DuesStatusPartial || s.PaidAmount != 4_000 || s.WaiverNote != nil {
		t.Errorf("April after un-waiving = %s paid %d note %v, want partial, 4000 paid, no note", s.Status, s.PaidAmount, deref(s.WaiverNote))
	}
	if s := w.status(t, "2026-03"); s.Status != DuesStatusWaived {
		t.Errorf("March = %s, want it still waived: one month at a time", s.Status)
	}
	if got := w.outstanding(t, "2026-04"); !containsPeriod(got, "2026-04") || containsPeriod(got, "2026-03") {
		t.Errorf("outstanding = %v, want April back and March not", got)
	}

	// A month that is not waived (never was, or already undone) is refused by name.
	for _, period := range []string{"2026-04", "2026-05"} {
		err := w.l.UnwaiveDues(ctx, UnwaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Period: period})
		if !errors.Is(err, ErrDuesNotWaived) {
			t.Errorf("UnwaiveDues(%s) = %v, want ErrDuesNotWaived", period, err)
		}
	}
	if err := w.l.UnwaiveDues(ctx, UnwaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Period: "2026-4"}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("UnwaiveDues(malformed) = %v, want ErrInvalidArgument", err)
	}

	// And the month can be waived again.
	w.waive(t, nil, "2026-04")
}

// --- no money moves ---------------------------------------------------------------

func TestWaivingAndUnwaivingMoveNoMoney(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWaiverWorld(t)
	w.pay(t, 4_000, "2026-04")
	w.pay(t, waiverRate, "2026-05", "2026-06")

	type snapshot struct {
		fund     money.Amount
		balances HomeBalances
		rows     int
	}
	take := func() snapshot {
		t.Helper()
		fund, err := w.l.FundBalance(ctx, w.f.fundID)
		if err != nil {
			t.Fatal(err)
		}
		balances, err := w.l.Balances(ctx, w.f.fundID)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot{fund, balances, transactionCount(t, w.l, w.f.fundID)}
	}
	before := take()
	if before.fund != 4_000+2*waiverRate {
		t.Fatalf("FundBalance before = %d, want %d: the payments are the whole fund", before.fund, 4_000+2*waiverRate)
	}

	w.waive(t, strp("catatan"), "2026-03", "2026-04", "2026-09")
	if after := take(); !reflect.DeepEqual(after, before) {
		t.Errorf("after waiving: %+v, want %+v unchanged", after, before)
	}
	if err := w.l.UnwaiveDues(ctx, UnwaiveDuesParams{FundID: w.f.fundID, MemberID: w.member, Period: "2026-04"}); err != nil {
		t.Fatal(err)
	}
	if after := take(); !reflect.DeepEqual(after, before) {
		t.Errorf("after un-waiving: %+v, want %+v unchanged", after, before)
	}
}
