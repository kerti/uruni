package ledger

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ADR-044: the twelve-month grid is the one-month roster read twelve times,
// computed in one pass. These tests pin that equivalence over a fixture that
// reaches every rule, the shape of the window (leading months dropped, the
// current month the ceiling), and that the reads do not grow with the fund.

// gridCellFromStatus is the reference reading of one DuesStatusForPeriod
// answer: no row is no obligation, and paying ahead draws as paid.
func gridCellFromStatus(rows []MemberDuesStatus, memberID int64) DuesGridCell {
	for _, r := range rows {
		if r.Member.ID != memberID {
			continue
		}
		switch r.Status {
		case DuesStatusUnpaid:
			return DuesCellUnpaid
		case DuesStatusPartial:
			return DuesCellPartial
		default:
			return DuesCellPaid
		}
	}
	return DuesCellNone
}

func TestDuesGridEqualsTwelveCallsToDuesStatusForPeriod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRichFund(t)
	q := store.New(r.l.db)

	// A free tier owes Rp 0, which DuesStatusForPeriod reads as paid.
	free := createDuesTier(t, q, r.f.fundID, "Free")
	createDuesRate(t, q, free, 0, "2025-01")
	joined := "2025-01-01"
	createDuesMember(t, q, r.f.fundID, duesMemberParams{name: "Zulaikha", tierID: &free, joinedOn: &joined})

	members, err := r.l.q.ListMembersByFund(ctx, r.f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	// Far enough ahead that the current-month cap never bites here.
	farNow := time.Date(2030, time.January, 15, 12, 0, 0, 0, time.UTC)

	seen := map[DuesGridCell]bool{}
	advance := false
	// Every end month from before the fixture's history to well after it.
	for _, end := range span("2024-06", 38) {
		endT, _ := time.Parse(duesPeriodLayout, end)
		var months [duesGridMonths]string
		reference := make([][]MemberDuesStatus, duesGridMonths)
		for i := range months {
			months[i] = endT.AddDate(0, i-(duesGridMonths-1), 0).Format(duesPeriodLayout)
			reference[i], err = r.l.DuesStatusForPeriod(ctx, r.f.fundID, months[i])
			if err != nil {
				t.Fatalf("DuesStatusForPeriod(%s) = %v", months[i], err)
			}
			for _, row := range reference[i] {
				advance = advance || row.Status == DuesStatusPaidInAdvance
			}
		}

		got, err := r.l.DuesGrid(ctx, r.f.fundID, end, farNow)
		if err != nil {
			t.Fatalf("DuesGrid(%s) = %v", end, err)
		}

		// The columns are a suffix of the twelve months: the dropped ones owe
		// nothing to anyone, the first kept one owes something to someone.
		dropped := duesGridMonths - len(got.Periods)
		if dropped < 0 || !reflect.DeepEqual(got.Periods, months[dropped:]) {
			t.Fatalf("DuesGrid(%s).Periods = %v, want a suffix of %v", end, got.Periods, months)
		}
		for i := range months {
			anyone := false
			for _, m := range members {
				anyone = anyone || gridCellFromStatus(reference[i], m.ID) != DuesCellNone
			}
			if i < dropped && anyone {
				t.Errorf("DuesGrid(%s) dropped %s, in which someone owes", end, months[i])
			}
			if i == dropped && !anyone {
				t.Errorf("DuesGrid(%s) starts at %s, in which nobody owes", end, months[i])
			}
		}

		// Cell by cell, rows in roster order, a row only for a member who owes in
		// a shown month.
		var want []DuesGridRow
		for _, m := range members {
			cells := make([]DuesGridCell, 0, len(got.Periods))
			owes := false
			for i := dropped; i < duesGridMonths; i++ {
				c := gridCellFromStatus(reference[i], m.ID)
				cells = append(cells, c)
				owes = owes || c != DuesCellNone
				seen[c] = true
			}
			if owes {
				want = append(want, DuesGridRow{MemberID: m.ID, Name: m.Name, Cells: cells})
			}
		}
		if len(got.Periods) == 0 {
			want = nil
		}
		if len(got.Rows) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got.Rows, want)) {
			t.Errorf("DuesGrid(%s).Rows:\n got  %+v\n want %+v", end, got.Rows, want)
		}
	}

	for _, c := range []DuesGridCell{DuesCellPaid, DuesCellPartial, DuesCellUnpaid, DuesCellNone} {
		if !seen[c] {
			t.Errorf("no cell read %q in any window: the fixture does not reach that state", c)
		}
	}
	if !advance {
		t.Error("no month read paid in advance: the fixture does not reach the collapse into paid")
	}
}

// The rules the equivalence rests on, spelled out on named members, so a
// change to one reads as one failing line and not a diff of whole rows.
func TestDuesGridNamedCells(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRichFund(t)
	q := store.New(r.l.db)
	free := createDuesTier(t, q, r.f.fundID, "Free")
	createDuesRate(t, q, free, 0, "2025-01")
	joined := "2025-01-01"
	createDuesMember(t, q, r.f.fundID, duesMemberParams{name: "Zulaikha", tierID: &free, joinedOn: &joined})

	grid := func(end string) DuesGrid {
		t.Helper()
		g, err := r.l.DuesGrid(ctx, r.f.fundID, end, richNow)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	got := grid("2026-10")
	if got.Periods[0] != "2025-11" || got.Periods[len(got.Periods)-1] != "2026-10" || len(got.Periods) != 12 {
		t.Fatalf("Periods = %v, want 2025-11..2026-10", got.Periods)
	}
	earlier := grid("2025-09")
	cell := func(g DuesGrid, name, period string) DuesGridCell {
		t.Helper()
		col := -1
		for i, p := range g.Periods {
			if p == period {
				col = i
			}
		}
		if col < 0 {
			t.Fatalf("%s is not a column of %v", period, g.Periods)
		}
		for _, row := range g.Rows {
			if row.Name == name {
				return row.Cells[col]
			}
		}
		return "(no row)"
	}
	for _, c := range []struct {
		g            DuesGrid
		name, period string
		want         DuesGridCell
	}{
		{got, "Ani", "2026-10", DuesCellPaid},      // paid ahead of October is still paid
		{got, "Budi", "2025-12", DuesCellPaid},     // at the old tier and rate
		{got, "Budi", "2026-03", DuesCellUnpaid},   // promoted to B in March: owes 30000, paid nothing
		{got, "Citra", "2026-04", DuesCellNone},    // joined in May
		{got, "Citra", "2026-05", DuesCellUnpaid},  // joined mid-month: owes the month in full
		{got, "Dewi", "2026-02", "(no row)"},       // never had a tier; her payment is not drawn
		{got, "Eka", "2026-07", DuesCellUnpaid},    // the month before a run, skipped
		{got, "Eka", "2026-09", DuesCellPaid},      // paid ahead (December is paid)
		{got, "Fajar", "2026-06", DuesCellPaid},    //
		{got, "Gita", "2026-04", DuesCellUnpaid},   // inactive_on in April: owes April in full
		{got, "Gita", "2026-05", DuesCellNone},     // gone
		{got, "Hari", "2026-01", DuesCellPaid},     //
		{got, "Hari", "2026-02", DuesCellUnpaid},   // its payment was reversed
		{got, "Indra", "2026-06", "(no row)"},      // a tier with no rate at all
		{got, "Lina", "2025-12", DuesCellPaid},     //
		{got, "Lina", "2026-05", DuesCellUnpaid},   //
		{got, "Lina", "2026-06", DuesCellNone},     // dropped to no tier in June
		{got, "Zulaikha", "2026-10", DuesCellPaid}, // owes Rp 0: Lunas, as the one-month table says
		{earlier, "Joko", "2025-05", DuesCellPartial},
		{earlier, "Joko", "2025-08", DuesCellPaid},
		{earlier, "Eka", "2025-05", DuesCellNone}, // joined in June
		{earlier, "Fajar", "2025-05", "(no row)"}, // tier C has no rate until 2026-06: no obligation anywhere in the window
	} {
		if g := cell(c.g, c.name, c.period); g != c.want {
			t.Errorf("%s in %s = %q, want %q", c.name, c.period, g, c.want)
		}
	}
}

func TestDuesGridLeadingMonthsAndTheCurrentMonthCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	now := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)

	empty, err := l.DuesGrid(ctx, f.fundID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	// newFixture's own member has no tier, so nobody owes anything yet.
	if len(empty.Periods) != 0 || len(empty.Rows) != 0 {
		t.Errorf("DuesGrid of a fund where nobody owes = %+v, want no columns and no rows", empty)
	}

	tier := createDuesTier(t, q, f.fundID, "Wajib")
	createDuesRate(t, q, tier, 10_000, "2000-01")
	late := createDuesTier(t, q, f.fundID, "Baru")
	createDuesRate(t, q, late, 10_000, "2026-07")
	august, may := "2026-08-31", "2026-05-02"
	createDuesMember(t, q, f.fundID, duesMemberParams{name: "Agus", tierID: &tier, joinedOn: &august})
	// Joined in May, but the tier has no rate before July: May and June are no
	// obligation, so the window starts in July (the first month anyone owes).
	createDuesMember(t, q, f.fundID, duesMemberParams{name: "Bayu", tierID: &late, joinedOn: &may})

	for _, tt := range []struct {
		name, end string
		now       time.Time
		first     string
		last      string
		count     int
	}{
		{"empty end is the fund's current month", "", now, "2026-07", "2026-10", 4},
		{"a later end is capped to the current month", "2027-05", now, "2026-07", "2026-10", 4},
		{"an earlier end stops there", "2026-08", now, "2026-07", "2026-08", 2},
		{"a long-ago end has no one owing", "2025-01", now, "", "", 0},
		// 18:00 UTC on 30 September is 01:00 on 1 October in Jakarta.
		{"the current month is read in the fund's zone", "", time.Date(2026, time.September, 30, 18, 0, 0, 0, time.UTC), "2026-07", "2026-10", 4},
		{"and the host's month does not leak in", "", time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC), "2026-07", "2026-09", 3},
	} {
		got, err := l.DuesGrid(ctx, f.fundID, tt.end, tt.now)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if len(got.Periods) != tt.count {
			t.Errorf("%s: Periods = %v, want %d of them", tt.name, got.Periods, tt.count)
			continue
		}
		if tt.count == 0 {
			if len(got.Rows) != 0 {
				t.Errorf("%s: Rows = %+v, want none", tt.name, got.Rows)
			}
			continue
		}
		if got.Periods[0] != tt.first || got.Periods[tt.count-1] != tt.last {
			t.Errorf("%s: Periods = %v, want %s..%s", tt.name, got.Periods, tt.first, tt.last)
		}
		for _, row := range got.Rows {
			if len(row.Cells) != tt.count {
				t.Errorf("%s: %s has %d cells for %d columns", tt.name, row.Name, len(row.Cells), tt.count)
			}
		}
	}

	// The window is at most twelve months however old the fund: Agus joined long
	// ago in this variant.
	old := "2020-01-01"
	createDuesMember(t, q, f.fundID, duesMemberParams{name: "Citra", tierID: &tier, joinedOn: &old})
	got, err := l.DuesGrid(ctx, f.fundID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Periods) != 12 || got.Periods[0] != "2025-11" {
		t.Errorf("Periods = %v, want twelve months from 2025-11", got.Periods)
	}
	if len(got.Rows) != 3 || got.Rows[0].Name != "Agus" || got.Rows[1].Name != "Bayu" || got.Rows[2].Name != "Citra" {
		t.Errorf("Rows = %+v, want Agus, Bayu, Citra by name", got.Rows)
	}
	// A gap in the middle stays: Agus has none before August, Bayu none before July.
	if c := got.Rows[0].Cells; c[0] != DuesCellNone || c[len(c)-1] != DuesCellUnpaid {
		t.Errorf("Agus cells = %v, want none first and unpaid last", c)
	}

	for _, bad := range []string{"2026-13", "2026", "junk", "2026-1"} {
		if _, err := l.DuesGrid(ctx, f.fundID, bad, now); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("DuesGrid(end %q) = %v, want ErrInvalidArgument", bad, err)
		}
	}
	if _, err := l.DuesGrid(ctx, 999, "", now); err == nil {
		t.Error("DuesGrid(missing fund) = nil error, want an error")
	}
}

// A payment does not change who owes: it moves a cell between unpaid, partial
// and paid, and a reversal moves it back, by adjusting entry not by edit.
func TestDuesGridFollowsPaymentsAndTheirReversal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	now := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	tier := createDuesTier(t, q, f.fundID, "Wajib")
	createDuesRate(t, q, tier, 10_000, "2000-01")
	joined := "2026-08-01"
	id := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Agus", tierID: &tier, joinedOn: &joined})

	cells := func() []DuesGridCell {
		t.Helper()
		g, err := l.DuesGrid(ctx, f.fundID, "", now)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Rows) != 1 {
			t.Fatalf("Rows = %+v, want Agus alone", g.Rows)
		}
		return g.Rows[0].Cells
	}
	want := func(label string, expected ...DuesGridCell) {
		t.Helper()
		if got := cells(); !reflect.DeepEqual(got, expected) {
			t.Errorf("%s: cells = %v, want %v", label, got, expected)
		}
	}
	want("nothing paid", DuesCellUnpaid, DuesCellUnpaid, DuesCellUnpaid)

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: id, OccurredOn: "2026-10-10", Now: now,
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: money.Amount(10_000)}, {DuesPeriod: "2026-09", Amount: money.Amount(4_000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want("one paid, one part-paid", DuesCellPaid, DuesCellPartial, DuesCellUnpaid)

	if _, err := l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: posted[0].ID, Now: now}); err != nil {
		t.Fatal(err)
	}
	want("the paid one reversed", DuesCellUnpaid, DuesCellPartial, DuesCellUnpaid)

	// Paid ahead of the current month still draws paid in every month it covers.
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: id, OccurredOn: "2026-10-11", Now: now,
		Periods: []PeriodAmount{{DuesPeriod: "2026-10", Amount: money.Amount(10_000)}, {DuesPeriod: "2026-11", Amount: money.Amount(10_000)}},
	}); err != nil {
		t.Fatal(err)
	}
	want("paid ahead", DuesCellUnpaid, DuesCellPartial, DuesCellPaid)
}

func TestDuesGridReadsDoNotGrowWithTheFund(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	measure := func(members, months int) int64 {
		l, n := newCountingLedger(t)
		sf := newScaleFund(t, l, members, months)
		var grid DuesGrid
		cost := queriesRun(n, func() {
			var err error
			if grid, err = l.DuesGrid(ctx, sf.f.fundID, scalePeriod, scaleNow); err != nil {
				t.Fatal(err)
			}
		})
		if len(grid.Rows) != members {
			t.Fatalf("grid has %d rows for %d members", len(grid.Rows), members)
		}
		return cost
	}

	small := measure(4, 6)
	large := measure(24, 36)
	if small == 0 {
		t.Fatal("no queries counted: the counting driver saw nothing")
	}
	if small != large {
		t.Errorf("queries for 4 members over 6 months = %d; for 24 over 36 = %d; want the same", small, large)
	}
}
