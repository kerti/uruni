package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// The backfill is neutral (ADR-040): for every member and every period, the new
// engine - reading the member_tier rows a v1 file is upgraded into - answers
// exactly what the engine before it answered from member.tier_id. legacy* below
// reproduces that old rule, in plain Go over the v1 document, with none of the
// production code (no pricing fold, no SQL), so the two can disagree.
//
// The v0.9.0 golden is two members, one tier and one rate, which proves little,
// so this fixture is built to hold every shape the rule has branches for: a
// rateless tier, a tier-less member, an inactive member, a late-starting rate, a
// backdated rate, a rate raise, a prepaid run with a hole, an overpayment, a
// reversed payment, a null joined_on from a pre-#471 file, and a member who joins
// after the period range's first years. "A tier change" is each member's tier:
// a v1 file holds only the current one, and the backfill (the current tier from
// the join month) must price every past period as the old rule did.

const (
	neutralityFrom = "2023-06" // before every join date and the fund
	neutralityTo   = "2028-02"
)

func richV1Fixture() documentV1 {
	t1, t2, t3, t4 := int64(1), int64(2), int64(3), int64(4)
	// 2024-01-01 00:00 in Asia/Jakarta is 2023-12-31 17:00Z: the fund's creation day
	// in Jakarta is the opening's day, so both adoption rules agree on 2024-01-01.
	created := time.Date(2023, 12, 31, 17, 0, 0, 0, time.UTC).Unix()

	v1 := documentV1{
		FormatVersion: 1,
		Funds:         []Fund{{ID: 1, Name: "Dana", Currency: "IDR", ReportSlug: "0000000000000000000slug01", CreatedAt: created}},
		Accounts:      []Account{{ID: 1, FundID: 1, Kind: "cash", Name: "Kas", CreatedAt: created}},
		Purposes:      []Purpose{{ID: 1, FundID: 1, Kind: "main", Name: "Kas Utama", CreatedAt: created}},
		DuesTiers: []DuesTier{
			{ID: 1, FundID: 1, Name: "Pelaksana", CreatedAt: created},
			{ID: 2, FundID: 1, Name: "Madya", CreatedAt: created}, // no rate: "madya TBD"
			{ID: 3, FundID: 1, Name: "Sepuh", CreatedAt: created},
			{ID: 4, FundID: 1, Name: "Kolam", CreatedAt: created},
		},
		DuesRates: []DuesRate{
			{ID: 1, TierID: 1, Amount: 50_000, EffectiveFrom: "2024-01", CreatedAt: created},
			{ID: 2, TierID: 1, Amount: 60_000, EffectiveFrom: "2025-01", CreatedAt: created}, // a raise
			{ID: 3, TierID: 3, Amount: 30_000, EffectiveFrom: "2025-06", CreatedAt: created}, // starts late
			{ID: 4, TierID: 4, Amount: 40_000, EffectiveFrom: "2022-01", CreatedAt: created}, // backdated, before the fund
			{ID: 5, TierID: 4, Amount: 45_000, EffectiveFrom: "2026-01", CreatedAt: created},
		},
		Members: []memberV1{
			{ID: 1, FundID: 1, Name: "Andi", TierID: &t1, JoinedOn: strPtr("2024-01-15"), CreatedAt: created},
			{ID: 2, FundID: 1, Name: "Budi", TierID: &t2, JoinedOn: strPtr("2024-01-01"), CreatedAt: created},
			{ID: 3, FundID: 1, Name: "Citra", JoinedOn: strPtr("2024-03-01"), CreatedAt: created},
			{ID: 4, FundID: 1, Name: "Dewi", TierID: &t1, JoinedOn: strPtr("2024-02-01"), InactiveOn: strPtr("2024-09-30"), CreatedAt: created},
			{ID: 5, FundID: 1, Name: "Eko", TierID: &t3, JoinedOn: nil, CreatedAt: created}, // pre-#471: the adoption date
			{ID: 6, FundID: 1, Name: "Fani", TierID: &t4, JoinedOn: strPtr("2025-05-01"), CreatedAt: created},
			{ID: 7, FundID: 1, Name: "Gita", TierID: &t1, JoinedOn: strPtr("2025-03-01"), CreatedAt: created},
			{ID: 8, FundID: 1, Name: "Hadi", TierID: &t1, JoinedOn: strPtr("2025-10-01"), CreatedAt: created},
			{ID: 9, FundID: 1, Name: "Indra", TierID: &t3, JoinedOn: strPtr("2027-06-01"), CreatedAt: created},
		},
	}

	id := int64(0)
	post := func(kind, direction string, amount int64, memberID int64, period, on string, reverses *int64) int64 {
		id++
		row := Transaction{
			ID: id, FundID: 1, AccountID: 1, PurposeID: 1, Direction: direction, Amount: amount,
			OccurredOn: on, Kind: kind, CreatedAt: created + id, ReversesTransactionID: reverses,
		}
		if memberID != 0 {
			row.MemberID = &memberID
			row.DuesPeriod = &period
		}
		v1.Transactions = append(v1.Transactions, row)
		return id
	}
	pay := func(memberID int64, amount int64, periods ...string) {
		for _, p := range periods {
			post("dues", "in", amount, memberID, p, p+"-05", nil)
		}
	}

	post("opening", "in", 1_000_000, 0, "", "2024-01-01", nil)
	pay(1, 50_000, "2024-01", "2024-02", "2024-03", "2024-04", "2024-05", "2024-06")
	pay(1, 25_000, "2024-07")                                             // partial
	pay(1, 50_000, "2024-08", "2024-09", "2024-11", "2024-12")            // a hole in October, then a run
	pay(1, 50_000, "2025-01")                                             // short against the raised rate
	pay(1, 60_000, "2025-02", "2025-03", "2025-04", "2025-05", "2025-06") // prepaid
	pay(2, 10_000, "2024-02")                                             // paid toward a rateless tier
	pay(4, 50_000, "2024-02", "2024-03", "2024-04")
	pay(4, 20_000, "2024-05")
	pay(5, 30_000, "2025-06")
	pay(5, 50_000, "2025-07") // overpayment
	pay(6, 45_000, "2026-01", "2026-02")
	reversed := post("dues", "in", 45_000, 6, "2026-03", "2026-03-04", nil)
	post("adjustment", "out", 45_000, 6, "2026-03", "2026-03-06", &reversed)
	pay(7, 60_000, "2025-03", "2025-04", "2025-05", "2025-06", "2025-07", "2025-08", "2025-09", "2025-10", "2025-11", "2025-12")
	pay(8, 60_000, "2025-10", "2025-12")
	pay(9, 30_000, "2027-06", "2027-07")

	doc, err := upgradeV1(v1)
	if err != nil {
		panic(err) // a fixture bug
	}
	totals, err := totalsFromRows(doc)
	if err != nil {
		panic(err)
	}
	v1.Totals = totals
	return v1
}

// --- the old rule, as a test oracle ---------------------------------------------

type legacyRow struct {
	owed, paid  int64
	status      string
	paidThrough string
}

type legacyWorld struct {
	v1       documentV1
	adoption string
}

func (w legacyWorld) joinedMonth(m memberV1) string {
	if m.JoinedOn != nil {
		return (*m.JoinedOn)[:7]
	}
	return w.adoption[:7]
}

func (w legacyWorld) owes(m memberV1, period string) bool {
	if period < w.joinedMonth(m) {
		return false
	}
	return m.InactiveOn == nil || period <= (*m.InactiveOn)[:7]
}

// rate is the latest rate of the tier at or before the period.
func (w legacyWorld) rate(tierID int64, period string) (int64, bool) {
	best, found := "", false
	var amount int64
	for _, r := range w.v1.DuesRates {
		if r.TierID == tierID && r.EffectiveFrom <= period && (!found || r.EffectiveFrom > best) {
			best, amount, found = r.EffectiveFrom, r.Amount, true
		}
	}
	return amount, found
}

// paid is what the member has paid toward a period, a reversed payment dropped.
func (w legacyWorld) paid(memberID int64, period string) int64 {
	var total int64
	for _, t := range w.liveDues(memberID) {
		if *t.DuesPeriod == period {
			total += t.Amount
		}
	}
	return total
}

func (w legacyWorld) liveDues(memberID int64) []Transaction {
	reversed := map[int64]bool{}
	for _, t := range w.v1.Transactions {
		if t.ReversesTransactionID != nil {
			reversed[*t.ReversesTransactionID] = true
		}
	}
	var out []Transaction
	for _, t := range w.v1.Transactions {
		if t.Kind == "dues" && t.MemberID != nil && *t.MemberID == memberID && !reversed[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

func (w legacyWorld) latestPaid(memberID int64) string {
	latest := ""
	for _, t := range w.liveDues(memberID) {
		if *t.DuesPeriod > latest {
			latest = *t.DuesPeriod
		}
	}
	return latest
}

func classify(owed, paid int64) string {
	switch {
	case paid >= owed:
		return "paid"
	case paid > 0:
		return "partial"
	default:
		return "unpaid"
	}
}

func nextMonth(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, 1, 0).Format("2006-01")
}

// row is the old DuesStatusForPeriod for one member: the CURRENT tier everywhere.
func (w legacyWorld) row(m memberV1, period string) (legacyRow, bool) {
	if m.TierID == nil || !w.owes(m, period) {
		return legacyRow{}, false
	}
	owed, ok := w.rate(*m.TierID, period)
	if !ok {
		return legacyRow{}, false
	}
	r := legacyRow{owed: owed, paid: w.paid(m.ID, period)}
	r.status = classify(owed, r.paid)
	if r.status == "paid" {
		if latest := w.latestPaid(m.ID); latest > period {
			r.status = "paid_in_advance"
			for next := nextMonth(period); next <= latest && w.owes(m, next); next = nextMonth(next) {
				rate, ok := w.rate(*m.TierID, next)
				if !ok {
					continue
				}
				if classify(rate, w.paid(m.ID, next)) != "paid" {
					break
				}
				r.paidThrough = next
			}
		}
	}
	return r, true
}

// outstanding is the old OutstandingDuesForMember: every owed period from the join
// month through `through` that is not paid in full.
func (w legacyWorld) outstanding(m memberV1, through string) []string {
	var out []string
	for p := w.joinedMonth(m); p <= through; p = nextMonth(p) {
		r, ok := w.row(m, p)
		if !ok || r.status == "paid" || r.status == "paid_in_advance" {
			continue
		}
		out = append(out, fmt.Sprintf("%s owed %d paid %d %s", p, r.owed, r.paid, r.status))
	}
	return out
}

// --- the comparison -------------------------------------------------------------

func TestBackfillOfAVersion1FileIsNeutralForEveryMemberAndPeriod(t *testing.T) {
	t.Parallel()
	v1 := richV1Fixture()
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseUploadBytes(t, zipOf(t, raw, nil))
	if err != nil {
		t.Fatalf("ParseUpload(rich v1) = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)
	l := ledger.New(destDB)
	ctx := context.Background()
	world := legacyWorld{v1: v1, adoption: "2024-01-01"}

	// The null joined_on really did take the adoption date.
	for _, m := range parsed.Document.Members {
		if m.Name == "Eko" && m.JoinedOn != "2024-01-01" {
			t.Fatalf("Eko's joined_on = %q, want the adoption date 2024-01-01", m.JoinedOn)
		}
	}

	compared := 0
	seen := map[string]bool{}
	for period := neutralityFrom; period <= neutralityTo; period = nextMonth(period) {
		rows, err := l.DuesStatusForPeriod(ctx, 1, period)
		if err != nil {
			t.Fatalf("DuesStatusForPeriod(%s) = %v", period, err)
		}
		got := map[int64]ledger.MemberDuesStatus{}
		for _, r := range rows {
			got[r.Member.ID] = r
		}
		for _, m := range v1.Members {
			want, wantListed := world.row(m, period)
			g, gotListed := got[m.ID]
			if wantListed != gotListed {
				t.Errorf("%s %s: listed = %v, the old rule listed = %v", period, m.Name, gotListed, wantListed)
				continue
			}
			if !wantListed {
				continue
			}
			compared++
			seen[want.status] = true
			if want.paidThrough != "" {
				seen["paid through"] = true
			}
			if g.OwedAmount != money.Amount(want.owed) || g.PaidAmount != money.Amount(want.paid) ||
				string(g.Status) != want.status || g.PaidThrough != want.paidThrough {
				t.Errorf("%s %s: owed %d paid %d %s through %q; the old rule: owed %d paid %d %s through %q",
					period, m.Name, g.OwedAmount, g.PaidAmount, g.Status, g.PaidThrough, want.owed, want.paid, want.status, want.paidThrough)
			}
			// The row carries the tier that priced it: the backfilled one.
			if g.Member.TierID == nil || *g.Member.TierID != *m.TierID {
				t.Errorf("%s %s: row tier = %v, want %d", period, m.Name, g.Member.TierID, *m.TierID)
			}
		}
	}
	if compared < 150 {
		t.Fatalf("only %d (member, period) rows compared; the fixture is not exercising the rule", compared)
	}
	for _, branch := range []string{"unpaid", "partial", "paid", "paid_in_advance", "paid through"} {
		if !seen[branch] {
			t.Errorf("the fixture never produced a %q row, so the comparison does not cover it", branch)
		}
	}

	// Arrears: the walk and the badge, per member.
	for _, m := range v1.Members {
		through := "2028-02"
		wantOut := world.outstanding(m, through)
		gotOut, err := l.OutstandingDuesForMember(ctx, 1, m.ID, through, time.Time{})
		if err != nil {
			t.Fatalf("OutstandingDuesForMember(%s) = %v", m.Name, err)
		}
		var gotLines []string
		for _, o := range gotOut {
			gotLines = append(gotLines, fmt.Sprintf("%s owed %d paid %d %s", o.Period, o.OwedAmount, o.PaidAmount, o.Status))
		}
		if fmt.Sprint(gotLines) != fmt.Sprint(wantOut) {
			t.Errorf("%s outstanding:\n got %v\nwant %v", m.Name, gotLines, wantOut)
		}

		const current = "2026-08"
		n, err := l.ArrearsMonthsForMember(ctx, 1, m.ID, current)
		if err != nil {
			t.Fatalf("ArrearsMonthsForMember(%s) = %v", m.Name, err)
		}
		if want := len(world.outstanding(m, "2026-07")); n != want {
			t.Errorf("%s arrears at %s = %d, the old rule says %d", m.Name, current, n, want)
		}
	}
}
