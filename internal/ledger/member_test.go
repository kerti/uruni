package ledger

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// --- member writes (ADR-040) -------------------------------------------------
//
// The clock is fixed at mid-August 2026 so "the current month" is 2026-08.

var augustNow = time.Date(2026, 8, 15, 12, 0, 0, 0, wib)

func strp(s string) *string { return &s }

// tierRows reads every member_tier row of a member straight from the table,
// superseded ones included, as "YYYY-MM#correction=tier" strings (tier 0 is NULL).
func tierRows(t *testing.T, l *Ledger, memberID int64) []string {
	t.Helper()
	rows, err := l.db.QueryContext(context.Background(),
		`SELECT effective_from, correction, COALESCE(tier_id, 0) FROM member_tier WHERE member_id = ? ORDER BY effective_from, correction`, memberID)
	if err != nil {
		t.Fatalf("reading member_tier: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var from string
		var correction, tier int64
		if err := rows.Scan(&from, &correction, &tier); err != nil {
			t.Fatal(err)
		}
		out = append(out, from+"#"+strconv.FormatInt(correction, 10)+"="+strconv.FormatInt(tier, 10))
	}
	return out
}

func wantRows(t *testing.T, l *Ledger, memberID int64, want ...string) {
	t.Helper()
	if got := tierRows(t, l, memberID); !slices.Equal(got, want) {
		t.Errorf("member_tier rows = %v, want %v", got, want)
	}
}

func tierIDOf(m Member) int64 {
	if m.TierID == nil {
		return 0
	}
	return *m.TierID
}

func TestCreateMemberWritesTheFirstTierRowAtTheJoinMonth(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	tier := createDuesTier(t, q, f.fundID, "Reguler")

	m, err := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &tier, JoinedOn: "2026-03-17"})
	if err != nil {
		t.Fatalf("CreateMember() = %v, want no error", err)
	}
	if tierIDOf(m) != tier {
		t.Errorf("CreateMember().TierID = %v, want %d", m.TierID, tier)
	}
	wantRows(t, l, m.ID, "2026-03#0="+strconv.FormatInt(tier, 10))
}

func TestCreateMemberWithoutATierWritesNoRow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	m, err := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Citra", JoinedOn: "2026-03-17"})
	if err != nil {
		t.Fatalf("CreateMember() = %v, want no error", err)
	}
	if m.TierID != nil {
		t.Errorf("CreateMember().TierID = %v, want nil", m.TierID)
	}
	wantRows(t, l, m.ID)
}

func TestCreateMemberRefusesAnotherFundsTierAndLeavesNoMember(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	other, err := q.CreateFund(context.Background(), store.CreateFundParams{Name: "Other", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "bcdefghijklmnopqrstuvw", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	foreign := createDuesTier(t, q, other.ID, "Asing")

	_, err = l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &foreign, JoinedOn: "2026-03-17"})
	if err == nil {
		t.Fatal("CreateMember(another fund's tier) = nil error, want the composite FK to refuse")
	}
	members, err := q.ListMembersByFund(context.Background(), f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 { // the fixture's Jane only
		t.Errorf("fund holds %d members after the refused create, want 1: the member row must roll back with its tier row", len(members))
	}
}

func TestUpdateMemberAStatementOnAMonthWithoutARowIsANewRow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-01-10"})

	got, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{
		FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b, TierFrom: "2026-06",
	})
	if err != nil {
		t.Fatalf("UpdateMember() = %v, want no error", err)
	}
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), "2026-06#0="+strconv.FormatInt(b, 10))
	if tierIDOf(got) != b {
		t.Errorf("UpdateMember().TierID = %v, want %d (the tier in force in August)", got.TierID, b)
	}
}

func TestUpdateMemberAStatementOnAMonthThatHasARowIsTheNextCorrection(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b, c := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B"), createDuesTier(t, q, f.fundID, "C")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-01-10"})
	state := func(tier int64) {
		t.Helper()
		if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{
			FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &tier, TierFrom: "2026-06",
		}); err != nil {
			t.Fatalf("UpdateMember(%d from June) = %v, want no error", tier, err)
		}
	}

	state(b) // June: B
	state(c) // wrong tier: state the right one, no extra control
	// Superseded rows are kept; the highest correction stands.
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), "2026-06#0="+strconv.FormatInt(b, 10), "2026-06#1="+strconv.FormatInt(c, 10))

	rows, _ := q.ListMemberTiersForMember(context.Background(), store.ListMemberTiersForMemberParams{FundID: f.fundID, MemberID: m.ID})
	if len(rows) != 2 || rows[1].TierID == nil || *rows[1].TierID != c {
		t.Errorf("standing rows = %+v, want January's A and June's C", rows)
	}
}

func TestUpdateMemberRestatingTheTierInForceWritesNothing(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-01-10"})

	for _, from := range []string{"", "2026-01", "2026-05"} { // re-saving the form, and a month inside the row
		if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{
			FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &a, TierFrom: from,
		}); err != nil {
			t.Fatalf("UpdateMember(A from %q) = %v, want no error", from, err)
		}
	}
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10))

	// A statement does not cancel a later row: A from January and B from June,
	// "A from March" changes nothing; undoing the promotion is "A from June".
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b, TierFrom: "2026-06"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &a, TierFrom: "2026-03"}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), "2026-06#0="+strconv.FormatInt(b, 10))

	got, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &a, TierFrom: "2026-06"})
	if err != nil {
		t.Fatal(err)
	}
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), "2026-06#0="+strconv.FormatInt(b, 10), "2026-06#1="+strconv.FormatInt(a, 10))
	if tierIDOf(got) != a {
		t.Errorf("after undoing the promotion the tier = %v, want %d", got.TierID, a)
	}
}

func TestUpdateMemberChoosingNoTierIsANullRowFromThatMonth(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-01-10"})

	got, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: nil, TierFrom: "2026-07"})
	if err != nil {
		t.Fatalf("UpdateMember(no tier from July) = %v, want no error", err)
	}
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), "2026-07#0=0")
	if got.TierID != nil {
		t.Errorf("UpdateMember().TierID = %v, want nil", got.TierID)
	}

	// Clearing a tier a member never had writes nothing.
	jane, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: f.memberID, SetTier: true, TierID: nil})
	if err != nil {
		t.Fatal(err)
	}
	if jane.TierID != nil {
		t.Errorf("tier = %v, want nil", jane.TierID)
	}
	wantRows(t, l, f.memberID)
}

func TestUpdateMemberTierStartMonthBounds(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-03-10"})

	tests := []struct {
		name string
		from string
		want error // nil: accepted
	}{
		{"the join month itself", "2026-03", nil},
		{"one month before the join month", "2026-02", ErrDateOutOfRange},
		{"the current month", "2026-08", nil},
		{"one month past the current month", "2026-09", ErrDateOutOfRange},
		{"not a month", "2026-8", ErrInvalidArgument},
	}
	for _, tt := range tests {
		_, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b, TierFrom: tt.from})
		if tt.want == nil && err != nil {
			t.Errorf("%s (%s): err = %v, want none", tt.name, tt.from, err)
		}
		if tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("%s (%s): err = %v, want %v", tt.name, tt.from, err, tt.want)
		}
	}

	// A tier start month needs a tier to start.
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, TierFrom: "2026-05"}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("a start month without a tier: err = %v, want ErrInvalidArgument", err)
	}
}

func TestUpdateMemberCeilingIsTheJoinMonthWhenThatIsLater(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
	// Joins next month (September, the current month being August).
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Baru", TierID: &a, JoinedOn: "2026-09-01"})

	// The default start is the join month, and the member shows the tier entered
	// for them, as today.
	got, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b})
	if err != nil {
		t.Fatalf("UpdateMember() = %v, want no error", err)
	}
	wantRows(t, l, m.ID, "2026-09#0="+strconv.FormatInt(a, 10), "2026-09#1="+strconv.FormatInt(b, 10))
	if tierIDOf(got) != b {
		t.Errorf("a member who joins next month shows tier %v, want %d (the tier entered for them)", got.TierID, b)
	}

	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &a, TierFrom: "2026-10"}); !errors.Is(err, ErrDateOutOfRange) {
		t.Errorf("a start after the join month: err = %v, want ErrDateOutOfRange", err)
	}
}

func TestUpdateMemberChangeInTheJoiningMonthCorrectsTheJoinRow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
	createDuesRate(t, q, a, 10_000, "2026-01")
	createDuesRate(t, q, b, 50_000, "2026-01")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-06-05"})

	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b, TierFrom: "2026-06"}); err != nil {
		t.Fatalf("UpdateMember() = %v, want no error", err)
	}
	wantRows(t, l, m.ID, "2026-06#0="+strconv.FormatInt(a, 10), "2026-06#1="+strconv.FormatInt(b, 10))

	rows, err := l.DuesStatusForPeriod(context.Background(), f.fundID, "2026-06")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := statusFor(t, rows, m.ID)
	if !ok || got.OwedAmount != 50_000 {
		t.Errorf("June = %+v (listed %v), want owed 50000: the corrected row prices the joining month", got, ok)
	}
}

func TestUpdateMemberJoinDateMovedEarlierCarriesTheEarliestTierBack(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a, b := createDuesTier(t, q, f.fundID, "A"), createDuesTier(t, q, f.fundID, "B")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-06-05"})
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &b, TierFrom: "2026-07"}); err != nil {
		t.Fatal(err)
	}

	got, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, JoinedOn: strp("2026-02-01")})
	if err != nil {
		t.Fatalf("UpdateMember(join date back to February) = %v, want no error", err)
	}
	// The row at the new join month carries the EARLIEST row's tier (A), not the
	// current one (B).
	wantRows(t, l, m.ID, "2026-02#0="+strconv.FormatInt(a, 10), "2026-06#0="+strconv.FormatInt(a, 10), "2026-07#0="+strconv.FormatInt(b, 10))
	if tierIDOf(got) != b {
		t.Errorf("current tier = %v, want %d", got.TierID, b)
	}
}

func TestUpdateMemberJoinDateMovedLaterLeavesTheEarliestRowAheadAndMovingItBackAgainStillCarriesIt(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	createDuesRate(t, q, a, 10_000, "2026-01")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-03-01"})

	// Moved later: no row is touched, and the earliest row (March) now sits ahead
	// of the join month (May).
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, JoinedOn: strp("2026-05-01")}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, l, m.ID, "2026-03#0="+strconv.FormatInt(a, 10))

	// Moved back to January: the earliest row (March) starts at or before the old
	// join month (May), so January gets a row - without the "at or before" the
	// live-arrears backdating would open months with no tier.
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, JoinedOn: strp("2026-01-01")}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, l, m.ID, "2026-01#0="+strconv.FormatInt(a, 10), "2026-03#0="+strconv.FormatInt(a, 10))

	out, err := l.OutstandingDuesForMember(context.Background(), f.fundID, m.ID, "2026-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0].Period != "2026-01" || out[0].OwedAmount != 10_000 {
		t.Errorf("outstanding = %+v, want January..April owed at 10000 (live arrears reached back to the new join date)", out)
	}
}

func TestUpdateMemberJoinDateMovedEarlierLeavesAnUntieredStretchUntiered(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	createDuesRate(t, q, a, 10_000, "2026-01")
	// Joined in March with no tier, given tier A from June.
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Citra", JoinedOn: "2026-03-01"})
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, SetTier: true, TierID: &a, TierFrom: "2026-06"}); err != nil {
		t.Fatal(err)
	}

	// The earliest row (June) began AFTER the old join month (March): the longer
	// untiered stretch stays untiered, no row is invented.
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: m.ID, JoinedOn: strp("2026-01-01")}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, l, m.ID, "2026-06#0="+strconv.FormatInt(a, 10))

	// And an earliest row that says "no tier" is not carried back either.
	n, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Dewi", JoinedOn: "2026-03-01"})
	setMemberTier(t, q, f.fundID, n.ID, nil, "2026-03", 0)
	if _, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{FundID: f.fundID, MemberID: n.ID, JoinedOn: strp("2026-01-01")}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, l, n.ID, "2026-03#0=0")
}

func TestUpdateMemberRenameAndLeavingDateTouchNoTierRow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-03-01"})

	got, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{
		FundID: f.fundID, MemberID: m.ID, Name: strp("Andi S."), SetInactiveOn: true, InactiveOn: strp("2026-07-31"),
	})
	if err != nil {
		t.Fatalf("UpdateMember() = %v, want no error", err)
	}
	if got.Name != "Andi S." || got.InactiveOn == nil || *got.InactiveOn != "2026-07-31" {
		t.Errorf("UpdateMember() = %+v, want the new name and leaving date", got.Member)
	}
	wantRows(t, l, m.ID, "2026-03#0="+strconv.FormatInt(a, 10))
}

func TestUpdateMemberUnknownMemberOrAnotherFundsIsNotFound(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	for _, p := range []UpdateMemberParams{
		{FundID: f.fundID, MemberID: 999_999},
		{FundID: f.fundID + 1, MemberID: f.memberID}, // a real member of another fund
	} {
		if _, err := l.at(augustNow).UpdateMember(context.Background(), p); err == nil {
			t.Errorf("UpdateMember(%+v) = nil error, want not found", p)
		}
	}
}

func TestUpdateMemberRefusesAnotherFundsTierAndRollsTheRestBack(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	other, _ := q.CreateFund(context.Background(), store.CreateFundParams{Name: "Other", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "bcdefghijklmnopqrstuvw", CreatedAt: 1})
	foreign := createDuesTier(t, q, other.ID, "Asing")

	_, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{
		FundID: f.fundID, MemberID: f.memberID, Name: strp("Diganti"), SetTier: true, TierID: &foreign,
	})
	if err == nil {
		t.Fatal("UpdateMember(another fund's tier) = nil error, want the composite FK to refuse")
	}
	m, err := q.GetMemberForFund(context.Background(), store.GetMemberForFundParams{ID: f.memberID, FundID: f.fundID})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Jane" {
		t.Errorf("name = %q after the refused update, want %q: the rename must roll back with the tier row", m.Name, "Jane")
	}
}

// member_tier's start month has a 2000-01 floor and a member's first row starts at
// the join month, so the join date has the same floor (#632), checked by the ledger
// as the typed date error instead of leaking the CHECK.
func TestCreateMemberRefusesAJoinDateBefore2000(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")

	for _, tierID := range []*int64{&a, nil} {
		_, err := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Kuno", TierID: tierID, JoinedOn: "1999-12-31"})
		if !errors.Is(err, ErrDateOutOfRange) {
			t.Errorf("CreateMember(joined 1999-12-31, tier %v) = %v, want ErrDateOutOfRange", tierID, err)
		}
	}
	m, err := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Tepat", TierID: &a, JoinedOn: "2000-01-01"})
	if err != nil {
		t.Fatalf("CreateMember(joined 2000-01-01) = %v, want no error", err)
	}
	wantRows(t, l, m.ID, "2000-01#0="+strconv.FormatInt(a, 10))
	members, _ := q.ListMembersByFund(context.Background(), f.fundID)
	if len(members) != 2 { // the fixture's Jane and Tepat
		t.Errorf("fund holds %d members, want 2: a refused create leaves nothing", len(members))
	}
}

func TestUpdateMemberRefusesAJoinDateBefore2000AndChangesNothing(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	m, _ := l.CreateMember(context.Background(), CreateMemberParams{FundID: f.fundID, Name: "Andi", TierID: &a, JoinedOn: "2026-03-01"})

	_, err := l.at(augustNow).UpdateMember(context.Background(), UpdateMemberParams{
		FundID: f.fundID, MemberID: m.ID, Name: strp("Diganti"), JoinedOn: strp("1999-12-31"),
	})
	if !errors.Is(err, ErrDateOutOfRange) {
		t.Fatalf("UpdateMember(joined 1999-12-31) = %v, want ErrDateOutOfRange", err)
	}
	got, _ := q.GetMemberForFund(context.Background(), store.GetMemberForFundParams{ID: m.ID, FundID: f.fundID})
	if got.Name != "Andi" || got.JoinedOn != "2026-03-01" {
		t.Errorf("member = %q joined %s after the refused update, want it unchanged", got.Name, got.JoinedOn)
	}
	wantRows(t, l, m.ID, "2026-03#0="+strconv.FormatInt(a, 10))
}
