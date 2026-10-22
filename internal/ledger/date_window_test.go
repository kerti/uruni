package ledger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// The plausible date window: a floor of 2000 and, per column, a ceiling read in
// the fund's zone from an injected clock. Both ends inclusive. No instant leans
// on the machine's zone or date (CI is UTC).
//
// edgeInstant is the instant the three zones disagree about: 22:30 WIB on 30
// September 2026, 23:30 WITA the same day, 00:30 WIT on 1 October. It sits on a
// month boundary, so every ceiling disagrees between a WIB and a WIT fund.
var edgeInstant = time.Date(2026, 9, 30, 22, 30, 0, 0, wib)

// bounds are the hand-worked limits at edgeInstant for one zone, so the test is
// not the code agreeing with itself. ok is the last accepted value of each
// ceiling and bad the first refused one.
type bounds struct {
	entryOK, entryBad   string // a date money moved on: today
	personOK, personBad string // a join or leaving date: today + 1 year
	duesOK, duesBad     string // a dues period: current month + 12
	rateOK, rateBad     string // a rate's start month: current month + 24
}

var edgeBounds = map[string]bounds{
	tz.Jakarta:  {"2026-09-30", "2026-10-01", "2027-09-30", "2027-10-01", "2027-09", "2027-10", "2028-09", "2028-10"},
	tz.Makassar: {"2026-09-30", "2026-10-01", "2027-09-30", "2027-10-01", "2027-09", "2027-10", "2028-09", "2028-10"},
	tz.Jayapura: {"2026-10-01", "2026-10-02", "2027-10-01", "2027-10-02", "2027-10", "2027-11", "2028-10", "2028-11"},
}

type pathKind int

const (
	kindEntry pathKind = iota
	kindPerson
	kindDues
	kindRate
)

// datePath is one write that takes a date, and how to attempt it with a given
// value and clock. try builds whatever the write needs around the date itself.
type datePath struct {
	name string
	kind pathKind
	try  func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error
}

// seedMain posts money so a purpose move or a roll has something to move, dated
// far enough back to be valid in every zone.
func seedMain(t *testing.T, l *Ledger, f fixture, amount money.Amount) {
	t.Helper()
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, amount, "2026-01-01")
}

var datePaths = []datePath{
	{"PostTransaction occurred_on", kindEntry, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.PostTransaction(context.Background(), PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 1000, OccurredOn: date, Now: now,
		})
		return err
	}},
	{"PostDuesPayments occurred_on", kindEntry, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: date, Now: now,
			Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 1000}},
		})
		return err
	}},
	{"TakeReconciliation fix occurred_on", kindEntry, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.TakeReconciliation(context.Background(), TakeReconciliationParams{
			FundID: f.fundID, Now: now,
			Counts: []AccountCount{
				{AccountID: f.cashID, ActualAmount: 1000, Resolution: "entry_added",
					Fix: &Fix{PurposeID: f.mainID, Direction: "in", Amount: 1000, OccurredOn: date}},
				f.bankZero(),
			},
		})
		return err
	}},
	{"PostPurposeMove occurred_on", kindEntry, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		seedMain(t, l, f, 10_000)
		env := openTestIncidental(t, l, f.fundID, "Kurban", "2026-01-02")
		_, err := l.PostPurposeMove(context.Background(), PostPurposeMoveParams{
			FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: env.PurposeID, AccountID: f.cashID, Amount: 1000, OccurredOn: date, Now: now,
		})
		return err
	}},
	{"PostTransferBetweenAccounts occurred_on", kindEntry, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		seedMain(t, l, f, 10_000)
		_, err := l.PostTransferBetweenAccounts(context.Background(), PostTransferBetweenAccountsParams{
			FundID: f.fundID, PurposeID: f.mainID, FromAccountID: f.cashID, ToAccountID: f.bankID, Amount: 1000, OccurredOn: date, Now: now,
		})
		return err
	}},
	{"OpenIncidental opened_on", kindEntry, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.OpenIncidental(context.Background(), OpenIncidentalParams{FundID: f.fundID, Occasion: "Kurban", OpenedOn: date, Now: now})
		return err
	}},
	{"CloseIncidentalAndRoll closed_on", kindEntry, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		env := openTestIncidental(t, l, f.fundID, "Kurban", "2026-01-02")
		_, err := l.CloseIncidentalAndRoll(context.Background(), CloseIncidentalAndRollParams{
			FundID: f.fundID, PurposeID: env.PurposeID, AccountID: f.cashID, ClosedOn: date, Now: now,
		})
		return err
	}},
	{"CreateReimbursement incurred_on", kindEntry, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.CreateReimbursement(context.Background(), CreateReimbursementParams{
			FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID, Amount: 1000, IncurredOn: date, Now: now,
		})
		return err
	}},
	{"UpdateReimbursement incurred_on", kindEntry, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		claim := createReimbursement(t, store.New(l.db), f, 1000, "2026-01-05", nil)
		_, err := l.UpdateReimbursement(context.Background(), UpdateReimbursementParams{
			FundID: f.fundID, ReimbursementID: claim.ID, IncurredOn: &date, Now: now,
		})
		return err
	}},
	{"UpdateReimbursement waived_on", kindEntry, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		claim := createReimbursement(t, store.New(l.db), f, 1000, "2026-01-05", nil)
		_, err := l.UpdateReimbursement(context.Background(), UpdateReimbursementParams{
			FundID: f.fundID, ReimbursementID: claim.ID, SetWaivedOn: true, WaivedOn: &date, Now: now,
		})
		return err
	}},
	{"SettleReimbursement occurred_on", kindEntry, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		claim := createReimbursement(t, store.New(l.db), f, 1000, "2026-01-05", nil)
		_, err := l.SettleReimbursement(context.Background(), SettleReimbursementParams{
			FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: date, Now: now,
		})
		return err
	}},
	{"CreateAccount opening balance", kindEntry, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.CreateAccount(context.Background(), CreateAccountParams{
			FundID: f.fundID, Kind: "cash", Name: "Laci", Now: now, OpeningBalance: &OpeningBalance{Amount: 1000, OccurredOn: date},
		})
		return err
	}},
	{"CreateMember joined_on", kindPerson, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Siti", JoinedOn: date, Now: now})
		return err
	}},
	{"UpdateMember joined_on", kindPerson, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: f.memberID, JoinedOn: &date, Now: now})
		return err
	}},
	{"UpdateMember inactive_on", kindPerson, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: f.memberID, SetInactiveOn: true, InactiveOn: &date, Now: now})
		return err
	}},
	{"UpdateAccount inactive_on", kindPerson, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.UpdateAccount(context.Background(), f.fundID, store.UpdateAccountParams{ID: f.bankID, SetInactiveOn: 1, InactiveOn: &date}, now)
		return err
	}},
	{"PostDuesPayments dues_period", kindDues, func(_ *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		_, err := l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-09-01", Now: now,
			Periods: []PeriodAmount{{DuesPeriod: date, Amount: 1000}},
		})
		return err
	}},
	{"CreateDuesRate effective_from", kindRate, func(t *testing.T, l *Ledger, f fixture, date string, now time.Time) error {
		tier := createDuesTier(t, store.New(l.db), f.fundID, "Warga")
		_, err := l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: f.fundID, TierID: tier, Amount: 1000, EffectiveFrom: date, Now: now})
		return err
	}},
}

// floorOf is the first accepted and the last refused value of a path's floor.
func floorOf(k pathKind) (ok, below string) {
	if k == kindDues || k == kindRate {
		return "2000-01", "1999-12"
	}
	return "2000-01-01", "1999-12-31"
}

func ceilingOf(b bounds, k pathKind) (ok, bad string) {
	switch k {
	case kindPerson:
		return b.personOK, b.personBad
	case kindDues:
		return b.duesOK, b.duesBad
	case kindRate:
		return b.rateOK, b.rateBad
	}
	return b.entryOK, b.entryBad
}

// Every write that takes a date accepts its ceiling and refuses one step past
// it, in each of the fund's three zones at the instant they disagree about, and
// accepts its floor and refuses one step below it. The refusal is the typed
// error, never a constraint message or the generic invalid argument.
func TestEveryDatedWriteHoldsItsWindowInTheFundsZone(t *testing.T) {
	t.Parallel()
	for _, path := range datePaths {
		for _, zone := range allZones {
			b := edgeBounds[zone]
			ceilOK, ceilBad := ceilingOf(b, path.kind)
			floorOK, floorBelow := floorOf(path.kind)
			for _, c := range []struct {
				name string
				date string
				want error // nil: accepted
			}{
				{"ceiling", ceilOK, nil},
				{"one past the ceiling", ceilBad, ErrDateOutOfRange},
				{"floor", floorOK, nil},
				{"one below the floor", floorBelow, ErrDateOutOfRange},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", path.name, zone, c.name), func(t *testing.T) {
					t.Parallel()
					l := newTestLedger(t)
					f := fundInZone(t, l, zone)
					err := path.try(t, l, f, c.date, edgeInstant)
					switch {
					case c.want == nil && err != nil:
						t.Errorf("%s = %q at 22:30 WIB 30 Sep in a %s fund: %v, want it accepted", path.name, c.date, zone, err)
					case c.want != nil && !errors.Is(err, c.want):
						t.Errorf("%s = %q at 22:30 WIB 30 Sep in a %s fund: %v, want %v", path.name, c.date, zone, err, c.want)
					case c.want != nil && errors.Is(err, ErrInvalidArgument):
						t.Errorf("%s = %q: %v wraps ErrInvalidArgument, want the typed error alone", path.name, c.date, err)
					}
				})
			}
		}
	}
}

// SetUpFund has no fund row to read a zone from, so its opening balance's
// ceiling reads the zone in its params, with the clock the params carry. The
// same instant is "today" in one zone and "tomorrow" in another; a refused
// opening leaves no fund behind.
func TestSetUpFundOpeningBalanceCeilingReadsTheParamsZone(t *testing.T) {
	t.Parallel()
	for _, zone := range allZones {
		b := edgeBounds[zone]
		for _, c := range []struct {
			name string
			date string
			want error
		}{
			{"ceiling", b.entryOK, nil},
			{"one past the ceiling", b.entryBad, ErrDateOutOfRange},
			{"floor", "2000-01-01", nil},
			{"one below the floor", "1999-12-31", ErrDateOutOfRange},
		} {
			t.Run(zone+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				l := newTestLedger(t)
				_, err := l.SetUpFund(context.Background(), SetUpFundParams{
					FundName: "Kas", TimeZone: zone, Now: edgeInstant,
					Accounts: []AccountInput{{Kind: "cash", Name: "Tunai", OpeningBalance: &OpeningBalance{Amount: 5000, OccurredOn: c.date}}},
				})
				if c.want == nil && err != nil {
					t.Errorf("SetUpFund(opening %s, %s) = %v, want it accepted", c.date, zone, err)
				}
				if c.want != nil {
					if !errors.Is(err, c.want) {
						t.Errorf("SetUpFund(opening %s, %s) = %v, want %v", c.date, zone, err, c.want)
					}
					if funds, _ := store.New(l.db).ListFunds(context.Background()); len(funds) != 0 {
						t.Errorf("a refused opening balance left %d fund(s) behind, want none", len(funds))
					}
				}
			})
		}
	}
}

// A zone SetUpFund cannot load cannot date an opening balance either: the
// ceiling is not skipped, the request is refused as invalid.
func TestSetUpFundRefusesAZoneItCannotReadTheCeilingIn(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	_, err := l.SetUpFund(context.Background(), SetUpFundParams{
		FundName: "Kas", TimeZone: "Asia/Tokyo", Accounts: []AccountInput{{Kind: "cash", Name: "Tunai"}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("SetUpFund(Asia/Tokyo) = %v, want ErrInvalidArgument", err)
	}
	if funds, _ := store.New(l.db).ListFunds(context.Background()); len(funds) != 0 {
		t.Errorf("a refused setup left %d fund(s) behind, want none", len(funds))
	}
}

// A zero opening balance posts no row, so a bogus date beside it is not checked
// (the existing rule), ceiling included.
func TestSetUpFundZeroOpeningBalanceDateIsNotChecked(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	_, err := l.SetUpFund(context.Background(), SetUpFundParams{
		FundName: "Kas", Accounts: []AccountInput{{Kind: "cash", Name: "Tunai", OpeningBalance: &OpeningBalance{Amount: 0, OccurredOn: "2099-01-01"}}},
	})
	if err != nil {
		t.Errorf("SetUpFund(zero opening dated 2099) = %v, want no error", err)
	}
}

// The same wall clock, a fund moved west: the ceiling follows the fund's zone as
// it is NOW, and never rewrites a row. A row dated a day ahead of the new today
// stays, and the report foots it (ADR-041); it just cannot be posted again.
func TestACeilingFollowsAZoneChangeButNeverRewritesARow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := fundInZone(t, l, tz.Jayapura)
	post := func(date string) error {
		_, err := l.PostTransaction(context.Background(), PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 1000, OccurredOn: date, Now: edgeInstant,
		})
		return err
	}
	if err := post("2026-10-01"); err != nil {
		t.Fatalf("1 Oct at 00:30 in a WIT fund = %v, want it accepted", err)
	}
	setFundZone(t, store.New(l.db), f.fundID, tz.Jakarta)
	if err := post("2026-10-01"); !errors.Is(err, ErrDateOutOfRange) {
		t.Errorf("1 Oct after the fund moved to WIB = %v, want ErrDateOutOfRange", err)
	}
	if bal, err := l.FundBalance(context.Background(), f.fundID); err != nil || bal != 1000 {
		t.Errorf("FundBalance = %v, %v, want the first row kept: 1000", bal, err)
	}
}

// A purpose correction takes the corrected row's own date and checks it against
// nothing: the ceiling lives in validateOccurredOn, an argument-shape check on a
// date a caller supplies, and not in postTransferPairTx (ADR-033, ADR-041). A
// row dated ahead of today (a legacy row, or one left by a zone change) must not
// make its own correction impossible.
func TestPurposeCorrectionOfARowDatedAheadIsNotRefused(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ahead, err := store.New(l.db).CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 5000,
		OccurredOn: "2099-01-01", Kind: "normal", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(dated 2099) = %v, want no error", err)
	}

	pair, err := l.PostPurposeCorrection(context.Background(), PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: ahead.ID, PurposeID: f.passID})
	if err != nil {
		t.Fatalf("PostPurposeCorrection(row dated 2099) = %v, want no error: the date is inherited, not supplied", err)
	}
	legs := transactionsForTransfer(t, store.New(l.db), f.fundID, pair.ID)
	if len(legs) != 2 {
		t.Fatalf("a correction posted %d legs, want 2", len(legs))
	}
	for _, leg := range legs {
		if leg.OccurredOn != "2099-01-01" {
			t.Errorf("a correction leg is dated %s, want the corrected row's own 2099-01-01", leg.OccurredOn)
		}
	}
}

// A claim's incurred_on is re-sent unchanged by the correction form, so an
// unchanged date is not defended against the ceiling; a changed one is. The
// floor is the schema's either way.
func TestUpdateReimbursementDoesNotDefendAnUnchangedDate(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	claim := createReimbursement(t, store.New(l.db), f, 1000, "2099-01-01", nil)
	ctx := context.Background()
	same := claim.IncurredOn
	amount := money.Amount(2000)

	if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{FundID: f.fundID, ReimbursementID: claim.ID, Amount: &amount, IncurredOn: &same, Now: edgeInstant}); err != nil {
		t.Errorf("fixing the amount of a claim dated ahead, re-sending its date = %v, want no error", err)
	}
	other := "2099-01-02"
	if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{FundID: f.fundID, ReimbursementID: claim.ID, IncurredOn: &other, Now: edgeInstant}); !errors.Is(err, ErrDateOutOfRange) {
		t.Errorf("moving the claim to another date past the ceiling = %v, want ErrDateOutOfRange", err)
	}
	below := "1999-12-31"
	if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{FundID: f.fundID, ReimbursementID: claim.ID, IncurredOn: &below, Now: edgeInstant}); !errors.Is(err, ErrDateOutOfRange) {
		t.Errorf("moving the claim below the floor = %v, want ErrDateOutOfRange", err)
	}
}

// A refused date writes nothing: a dues payment with one period past the
// ceiling posts none of its periods.
func TestADatedWriteRefusedByTheWindowLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := fundInZone(t, l, tz.Jakarta)
	_, err := l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-09-01", Now: edgeInstant,
		Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 1000}, {DuesPeriod: "2027-10", Amount: 1000}},
	})
	if !errors.Is(err, ErrDateOutOfRange) {
		t.Fatalf("PostDuesPayments(second period one month past the ceiling) = %v, want ErrDateOutOfRange", err)
	}
	if bal, _ := l.FundBalance(context.Background(), f.fundID); bal != 0 {
		t.Errorf("FundBalance = %d, want 0: the first period must not have posted", bal)
	}
}

// A malformed date is the generic invalid argument; only a real date outside
// the window is the typed error.
func TestMalformedAndOutOfRangeDatesAreDifferentErrors(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "2026-02-30", "2026-9-01", "tomorrow", "2026-09-30T00:00"} {
		err := validateOccurredOn("occurred_on", s, "2026-12-31")
		if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrDateOutOfRange) {
			t.Errorf("validateOccurredOn(%q) = %v, want ErrInvalidArgument only", s, err)
		}
	}
	for _, s := range []string{"", "2026-13", "2026-1", "2026-09-01"} {
		err := validateDuesPeriod("dues_period", s, "2026-12")
		if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrDateOutOfRange) {
			t.Errorf("validateDuesPeriod(%q) = %v, want ErrInvalidArgument only", s, err)
		}
	}
	if err := validateOccurredOn("occurred_on", "2026-12-31", "2026-12-31"); err != nil {
		t.Errorf("validateOccurredOn(the ceiling itself) = %v, want it inclusive", err)
	}
	if err := validateOccurredOn("occurred_on", "2026-12-31", "2026-12-30"); !errors.Is(err, ErrDateOutOfRange) {
		t.Errorf("validateOccurredOn(one past the ceiling) = %v, want ErrDateOutOfRange", err)
	}
}

// The year-ahead ceiling of a join date, worked by hand, including the one day
// it does not exist: 29 February reads as 28 February of the year after, so the
// ceiling never lands on 1 March (that would be a day of grace) and always
// names a real date the picker can offer.
func TestYearAheadCeilingIsTheSameDayNextYearClampedToTheMonthEnd(t *testing.T) {
	t.Parallel()
	for today, want := range map[string]string{
		"2026-09-30": "2027-09-30",
		"2026-12-31": "2027-12-31",
		"2027-02-28": "2028-02-28",
		"2028-02-28": "2029-02-28",
		"2028-02-29": "2029-02-28", // the leap day
		"2028-03-01": "2029-03-01",
		"2024-02-29": "2025-02-28",
		"2023-01-01": "2024-01-01",
	} {
		if got := (dateWindow{today: today}).yearAhead(); got != want {
			t.Errorf("year-ahead ceiling on %s = %s, want %s", today, got, want)
		}
	}
}

// The month ceilings cross a year end by calendar arithmetic.
func TestMonthCeilingsCrossYearEnds(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ today, dues, rate string }{
		{"2026-01-31", "2027-01", "2028-01"},
		{"2026-12-15", "2027-12", "2028-12"},
		{"2026-06-01", "2027-06", "2028-06"},
	} {
		w := dateWindow{today: c.today}
		if w.dues() != c.dues || w.rate() != c.rate {
			t.Errorf("on %s: dues %s rate %s, want %s and %s", c.today, w.dues(), w.rate(), c.dues, c.rate)
		}
	}
}

// A correction or a deletion restates a month that already stands: it chooses no
// date, so it checks none (ADR-040). A rate starting past today's ceiling (left
// by a zone change west, or by time itself running the other way in a test)
// can still be corrected and withdrawn.
func TestRateCorrectionsInheritTheirMonthAndCheckNoCeiling(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	tier := createDuesTier(t, q, f.fundID, "Warga")
	rate, err := q.CreateDuesRate(context.Background(), store.CreateDuesRateParams{TierID: tier, Amount: 1000, EffectiveFrom: "2099-01", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateDuesRate(2099-01) = %v, want no error", err)
	}
	corrected, err := l.CorrectDuesRate(context.Background(), f.fundID, rate.ID, 2000)
	if err != nil {
		t.Fatalf("CorrectDuesRate(a rate starting 2099-01) = %v, want no error: the month is inherited", err)
	}
	if err := l.DeleteDuesRate(context.Background(), f.fundID, corrected.ID); err != nil {
		t.Errorf("DeleteDuesRate(a rate starting 2099-01) = %v, want no error: the month is inherited", err)
	}
}
