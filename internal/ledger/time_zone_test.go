package ledger

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// ADR-041: a day or a month is an injected instant read in the fund's zone.
// Every test here names its instants by an explicit wall clock and never leans
// on the machine's zone (CI is UTC). The instant the three zones disagree about
// is 22:30 WIB: 23:30 WITA, 00:30 WIT the next day.

// fundInZone is the fixture fund, moved to zone.
func fundInZone(t *testing.T, l *Ledger, zone string) fixture {
	t.Helper()
	f := newFixture(t, l)
	setFundZone(t, store.New(l.db), f.fundID, zone)
	return f
}

var allZones = []string{tz.Jakarta, tz.Makassar, tz.Jayapura}

func TestMonthlyReportsRunningMonthAndTodayFollowTheFundsZone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		zone      string
		now       time.Time
		wantMonth string
		wantAsOf  string
	}{
		{"22:30 WIB, WIB fund", tz.Jakarta, time.Date(2026, 9, 30, 22, 30, 0, 0, wib), "2026-09", "2026-09-30"},
		{"22:30 WIB, WITA fund (23:30)", tz.Makassar, time.Date(2026, 9, 30, 22, 30, 0, 0, wib), "2026-09", "2026-09-30"},
		{"22:30 WIB, WIT fund (00:30 next day)", tz.Jayapura, time.Date(2026, 9, 30, 22, 30, 0, 0, wib), "2026-10", "2026-10-01"},
		{"23:30 WIB, WIB fund", tz.Jakarta, time.Date(2026, 9, 30, 23, 30, 0, 0, wib), "2026-09", "2026-09-30"},
		{"23:30 WIB, WITA fund (00:30 next day)", tz.Makassar, time.Date(2026, 9, 30, 23, 30, 0, 0, wib), "2026-10", "2026-10-01"},
		{"23:30 WIB, WIT fund", tz.Jayapura, time.Date(2026, 9, 30, 23, 30, 0, 0, wib), "2026-10", "2026-10-01"},
		{"the same instant written in UTC", tz.Jayapura, time.Date(2026, 9, 30, 15, 30, 0, 0, time.UTC), "2026-10", "2026-10-01"},
		{"year boundary, WIT fund", tz.Jayapura, time.Date(2026, 12, 31, 22, 30, 0, 0, wib), "2027-01", "2027-01-01"},
		{"year boundary, WIB fund", tz.Jakarta, time.Date(2026, 12, 31, 22, 30, 0, 0, wib), "2026-12", "2026-12-31"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := fundInZone(t, l, tc.zone)
			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, "2026-07-15", nil)

			r := monthlyReportAt(t, l, tc.now, ReportParams{FundID: f.fundID})
			if r.Month != tc.wantMonth || r.AsOf != tc.wantAsOf || !r.Running {
				t.Errorf("Month/AsOf/Running = %q/%q/%v, want %q/%q/true", r.Month, r.AsOf, r.Running, tc.wantMonth, tc.wantAsOf)
			}
			// The list of months runs to the fund's current month, no further.
			if last := r.Months[len(r.Months)-1]; last != tc.wantMonth {
				t.Errorf("Months ends at %q, want the current month %q", last, tc.wantMonth)
			}
			if r.Months[0] != "2026-07" {
				t.Errorf("Months starts at %q, want the first transaction's month 2026-07", r.Months[0])
			}
		})
	}
}

// A past month's "latest cek kas" is bounded by the first instant of the next
// month in the fund's zone (ADR-037, ADR-041). A count at 22:30 WIB on 30
// September is still September for a WIB or WITA fund and already October for a
// WIT one, so the September report of a WIT fund no longer shows it; the next
// month's report does, dated in the fund's own zone. No ledger sum moves.
func TestMonthlyReportPastMonthCekKasBoundFollowsTheFundsZone(t *testing.T) {
	t.Parallel()
	count := time.Date(2026, 9, 30, 22, 30, 0, 0, wib).Unix()
	cases := []struct {
		zone        string
		inSeptember bool
		runningDate string
	}{
		{tz.Jakarta, true, "2026-09-30"},
		{tz.Makassar, true, "2026-09-30"},
		{tz.Jayapura, false, "2026-10-01"},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			l := newTestLedger(t)
			q := store.New(l.db)
			f := fundInZone(t, l, tc.zone)
			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-03", nil)
			rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: f.fundID, PerformedAt: count, CreatedAt: count})
			if err != nil {
				t.Fatalf("CreateReconciliation() = %v", err)
			}
			if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
				FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.cashID,
				RecordedAmount: 100_000, ActualAmount: 100_000, DifferenceAmount: 0, Resolution: "matched",
			}); err != nil {
				t.Fatalf("CreateReconciliationLine() = %v", err)
			}
			now := time.Date(2026, 10, 5, 12, 0, 0, 0, wib)

			sept := monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID, Month: "2026-09"})
			switch {
			case tc.inSeptember && (sept.Reconciliation == nil || sept.Reconciliation.Date != "2026-09-30"):
				t.Errorf("September report Reconciliation = %+v, want the count dated 2026-09-30", sept.Reconciliation)
			case !tc.inSeptember && sept.Reconciliation != nil:
				t.Errorf("September report Reconciliation = %+v, want none: the count is October for this fund", sept.Reconciliation)
			}
			if sept.Balance != 100_000 {
				t.Errorf("September balance = %d, want 100000: no ledger sum depends on the zone", sept.Balance)
			}

			oct := monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID})
			if oct.Reconciliation == nil || oct.Reconciliation.Date != tc.runningDate {
				t.Errorf("running report Reconciliation = %+v, want the count dated %s in the fund's zone", oct.Reconciliation, tc.runningDate)
			}
		})
	}
}

// Beranda's "Bulan ini" is the report's running month (ADR-038), so it moves
// with the fund's zone too: 22:30 WIB on 30 September is still September for a
// WIB fund, which counts a row dated 1 October (the running month has no upper
// bound) and September's own, and already October for a WIT one, which counts
// only October's.
func TestMonthInOutReadsTheRunningMonthInTheFundsZone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 22, 30, 0, 0, wib)
	cases := []struct {
		zone   string
		wantIn int64
	}{
		{tz.Jakarta, 57_000},
		{tz.Makassar, 57_000},
		{tz.Jayapura, 7_000},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := fundInZone(t, l, tc.zone)
			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 50_000, "2026-09-15", nil)
			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 7_000, "2026-10-01", nil)

			in, out, err := l.at(now).MonthInOut(context.Background(), f.fundID)
			if err != nil {
				t.Fatalf("MonthInOut() = %v, want no error", err)
			}
			if int64(in) != tc.wantIn || out != 0 {
				t.Errorf("MonthInOut() = %d/%d, want %d/0", in, out, tc.wantIn)
			}
		})
	}
}

// A tier stated without a start month starts from the fund's current month
// (ADR-040), which is the seam CurrentDuesPeriod owns: 22:30 WIB on 30
// September is September to a WIB fund and October to a WIT one.
func TestUpdateMemberTierDefaultsToTheFundsCurrentMonth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 22, 30, 0, 0, wib)
	for zone, wantFrom := range map[string]string{tz.Jakarta: "2026-09", tz.Makassar: "2026-09", tz.Jayapura: "2026-10"} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := fundInZone(t, l, zone)
			q := store.New(l.db)
			a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
			m, err := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-01-10"})
			if err != nil {
				t.Fatalf("CreateMember() = %v", err)
			}
			if _, err := l.at(now).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b}); err != nil {
				t.Fatalf("UpdateMember() = %v, want no error", err)
			}
			wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), wantFrom+"#0="+strconv.FormatInt(b, 10))
		})
	}
}

// A zone the schema's CHECK cannot have let in is refused rather than dated in
// UTC or WIB: the ledger fails the read.
func TestFundLocationRefusesAZoneOutsideTheThree(t *testing.T) {
	t.Parallel()
	for _, zone := range allZones {
		loc, err := FundLocation(store.Fund{ID: 1, TimeZone: zone})
		if err != nil || loc.String() != zone {
			t.Errorf("FundLocation(%q) = %v, %v", zone, loc, err)
		}
	}
	for _, zone := range []string{"", "UTC", "Asia/Tokyo"} {
		if loc, err := FundLocation(store.Fund{ID: 1, TimeZone: zone}); err == nil {
			t.Errorf("FundLocation(%q) = %v, want an error", zone, loc)
		}
	}
}

// SetUpFund takes the zone as a parameter: absent is WIB, a named zone is kept,
// and the schema's CHECK refuses anything else with no fund left behind.
func TestSetUpFundTakesTheTimeZone(t *testing.T) {
	t.Parallel()
	accounts := []AccountInput{{Kind: "cash", Name: "Tunai"}}
	for _, tc := range []struct{ given, want string }{
		{"", tz.Jakarta}, {tz.Jakarta, tz.Jakarta}, {tz.Makassar, tz.Makassar}, {tz.Jayapura, tz.Jayapura},
	} {
		l := newTestLedger(t)
		res, err := l.SetUpFund(context.Background(), SetUpFundParams{FundName: "Dana", TimeZone: tc.given, Accounts: accounts})
		if err != nil {
			t.Fatalf("SetUpFund(time_zone=%q) = %v, want no error", tc.given, err)
		}
		if res.Fund.TimeZone != tc.want {
			t.Errorf("SetUpFund(time_zone=%q).Fund.TimeZone = %q, want %q", tc.given, res.Fund.TimeZone, tc.want)
		}
	}

	for _, bad := range []string{"UTC", "Asia/Tokyo", "wib", "asia/jakarta"} {
		l := newTestLedger(t)
		if _, err := l.SetUpFund(context.Background(), SetUpFundParams{FundName: "Dana", TimeZone: bad, Accounts: accounts}); err == nil {
			t.Errorf("SetUpFund(time_zone=%q) = nil error, want the CHECK to refuse it", bad)
		}
		funds, err := store.New(l.db).ListFunds(context.Background())
		if err != nil || len(funds) != 0 {
			t.Errorf("after SetUpFund(time_zone=%q) refused: %d funds (%v), want none", bad, len(funds), err)
		}
	}
}

// A zone change re-reads and never rewrites (ADR-041): a past month's report is
// the same figures in any zone, because nothing it sums is derived from the zone,
// and setting the old zone back restores every reading exactly. A row dated
// ahead of the fund's new today (WIT to WIB, in the two hours between) is not
// invalid: the running month has no upper bound.
func TestChangingTheZoneNeverInvalidatesARowAndIsExactlyReversible(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := fundInZone(t, l, tz.Jayapura)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-03", nil)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 30_000, "2026-09-20", nil)
	// 22:30 WIB on 30 September is already 1 October to a WIT fund: she records a
	// row for "today", then goes back to WIB, for which it is dated a day ahead.
	now := time.Date(2026, 9, 30, 22, 30, 0, 0, wib)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 5_000, "2026-10-01", nil)

	// September is past in every zone by the 5th, so its figures are comparable
	// across zones; on the 30th it is the running month for WIB and past for WIT.
	later := time.Date(2026, 10, 5, 12, 0, 0, 0, wib)
	readAll := func() (Report, Report) {
		return monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID}),
			monthlyReportAt(t, l, later, ReportParams{FundID: f.fundID, Month: "2026-09"})
	}
	witRunning, witSept := readAll()
	if witRunning.Month != "2026-10" {
		t.Fatalf("WIT running month = %q, want 2026-10", witRunning.Month)
	}

	setFundZone(t, q, f.fundID, tz.Jakarta)
	wibRunning, wibSept := readAll()
	if wibRunning.Month != "2026-09" {
		t.Errorf("WIB running month = %q, want 2026-09", wibRunning.Month)
	}
	// The figure behind "now" is the same: every row is still counted, the
	// ahead-dated one included (the running month has no upper bound).
	if wibRunning.Balance != witRunning.Balance || wibRunning.Balance != 75_000 {
		t.Errorf("balance WIB/WIT = %d/%d, want 75000 in both", wibRunning.Balance, witRunning.Balance)
	}
	if !reflect.DeepEqual(wibSept.Walk, witSept.Walk) || wibSept.Balance != witSept.Balance {
		t.Errorf("September's walk differs between zones: %+v vs %+v", wibSept.Walk, witSept.Walk)
	}
	for _, r := range wibRunning.Rows {
		if r.Entry != nil && r.Date == "2026-10-01" {
			return // the ahead-dated row is shown, not hidden or refused
		}
	}
	t.Error("the row dated 2026-10-01 is missing from the WIB running month, want it footed")
}

func TestSettingTheOldZoneBackRestoresEveryReading(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := fundInZone(t, l, tz.Makassar)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-03", nil)
	now := time.Date(2026, 9, 30, 23, 30, 0, 0, wib)

	before := monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID})
	setFundZone(t, q, f.fundID, tz.Jakarta)
	if mid := monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID}); mid.Month == before.Month {
		t.Fatalf("WIB and WITA agree on the month at 23:30 WIB (%q): the fixture proves nothing", mid.Month)
	}
	setFundZone(t, q, f.fundID, tz.Makassar)
	after := monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID})
	if !reflect.DeepEqual(before, after) {
		t.Errorf("report after changing the zone and back differs:\nbefore %+v\nafter  %+v", before, after)
	}
}

// Nothing reads a zone it was not handed: a fund the ledger cannot find is an
// error, not a WIB default.
func TestMonthInOutOfAMissingFundIsAnError(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	if _, _, err := l.MonthInOut(context.Background(), 999); err == nil || !strings.Contains(err.Error(), "fund") {
		t.Errorf("MonthInOut(missing fund) = %v, want an error naming the fund", err)
	}
}
