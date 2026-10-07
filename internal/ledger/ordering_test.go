package ledger

import (
	"context"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// Every non-chronological list has a stated order (maintainer, 2026-10-05):
// members and tiers by name ignoring case, locations cash first then by
// name, pos Kas Utama first then by name. Names are added out of order and
// in mixed case, so creation (id) order would fail every assertion.
func TestListsReadInTheirStatedOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)

	tier := createDuesTier(t, q, f.fundID, "pelaksana")
	createDuesTier(t, q, f.fundID, "Fungsional")
	createDuesTier(t, q, f.fundID, "Madya")
	createDuesRate(t, q, tier, 25_000, "2026-01")
	for _, name := range []string{"budi", "Zul", "Ani", "Cici"} {
		createDuesMember(t, q, f.fundID, duesMemberParams{name: name, tierID: &tier})
	}
	createAccount(t, q, f.fundID, "bank", "Bank Aceh")
	createAccount(t, q, f.fundID, "cash", "Kotak kedua")
	createPurpose(t, q, f.fundID, "pass_through", "aa titipan")
	openTestIncidental(t, l, f.fundID, "Zakat", "2026-09-01")

	members, err := q.ListMembersByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v", err)
	}
	assertFolded(t, "members", names(members, func(m store.Member) string { return m.Name }))

	status, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-09")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() = %v", err)
	}
	assertFolded(t, "dues status", names(status, func(s MemberDuesStatus) string { return s.Member.Name }))

	page, err := q.ListMembersPage(ctx, store.ListMembersPageParams{CurrentPeriod: "2026-09", FundID: f.fundID, PageLimit: 100})
	if err != nil {
		t.Fatalf("ListMembersPage() = %v", err)
	}
	assertFolded(t, "member roster", names(page, func(m store.ListMembersPageRow) string { return m.Name }))

	tiers, err := q.ListDuesTiersByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListDuesTiersByFund() = %v", err)
	}
	assertFolded(t, "tiers", names(tiers, func(d store.DuesTier) string { return d.Name }))

	accounts, err := q.ListAccountsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListAccountsByFund() = %v", err)
	}
	var cash, bank []string
	seenBank := false
	for _, a := range accounts {
		if a.Kind == "cash" {
			if seenBank {
				t.Errorf("cash location %q listed after a bank", a.Name)
			}
			cash = append(cash, a.Name)
		} else {
			seenBank = true
			bank = append(bank, a.Name)
		}
	}
	assertFolded(t, "cash locations", cash)
	assertFolded(t, "bank locations", bank)

	for _, list := range []struct {
		name string
		get  func() ([]store.Purpose, error)
	}{
		{"purposes", func() ([]store.Purpose, error) { return q.ListPurposesByFund(ctx, f.fundID) }},
		{"selectable purposes", func() ([]store.Purpose, error) { return q.ListSelectablePurposesByFund(ctx, f.fundID) }},
	} {
		ps, err := list.get()
		if err != nil {
			t.Fatalf("%s: %v", list.name, err)
		}
		if len(ps) == 0 || ps[0].Kind != "main" {
			t.Fatalf("%s = %+v, want Kas Utama first", list.name, ps)
		}
		assertFolded(t, list.name+" after Kas Utama", names(ps[1:], func(p store.Purpose) string { return p.Name }))
	}
}

func names[T any](rows []T, name func(T) string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, name(r))
	}
	return out
}

// assertFolded fails unless got is in order by name, ignoring case - and
// holds at least two names, so an empty list cannot pass by accident.
func assertFolded(t *testing.T, what string, got []string) {
	t.Helper()
	if len(got) < 2 {
		t.Fatalf("%s = %v, want at least two to order", what, got)
	}
	for i := 1; i < len(got); i++ {
		if strings.ToLower(got[i-1]) > strings.ToLower(got[i]) {
			t.Errorf("%s = %v, want by name ignoring case", what, got)
			return
		}
	}
}

// The roster's keyset cursor compares the way its ORDER BY sorts: walking it
// two at a time across mixed-case names returns every member once, in order.
func TestMemberRosterPagesAcrossMixedCase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	for _, name := range []string{"budi", "Zul", "Ani", "cici", "Budi", "Dedi"} {
		createDuesMember(t, q, f.fundID, duesMemberParams{name: name})
	}
	all, err := q.ListMembersByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v", err)
	}

	var walked []string
	params := store.ListMembersPageParams{CurrentPeriod: "2026-09", FundID: f.fundID, PageLimit: 2}
	for range len(all) + 1 {
		page, err := q.ListMembersPage(ctx, params)
		if err != nil {
			t.Fatalf("ListMembersPage() = %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, m := range page {
			walked = append(walked, m.Name)
		}
		last := page[len(page)-1]
		params.CursorName, params.CursorID = last.Name, &last.ID
	}
	want := names(all, func(m store.Member) string { return m.Name })
	if strings.Join(walked, ",") != strings.Join(want, ",") {
		t.Errorf("walked %v, want %v", walked, want)
	}
}
