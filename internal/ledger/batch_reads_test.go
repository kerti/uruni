package ledger

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// #532: the roster's arrears, the dues-status roster and the home balances read
// in a fixed number of queries. These tests hold two things. The batched reads
// return exactly what the one-at-a-time reads they replaced return, over a
// fixture built to hit every rule; and the number of queries does not depend on
// how many members, months, accounts or purposes there are.

// --- the old, one-at-a-time reads, kept as the reference ----------------------

// duesStatusPerMember is DuesStatusForPeriod as it was before #532: each
// paid-in-advance row fetches its own payment history. Everything else is the
// same walk, so the only thing the comparison can show is the batch.
func duesStatusPerMember(ctx context.Context, l *Ledger, fundID int64, period string) ([]MemberDuesStatus, error) {
	members, err := l.q.ListMembersByFund(ctx, fundID)
	if err != nil {
		return nil, err
	}
	tierRows, err := l.q.ListMemberTiersByFund(ctx, fundID)
	if err != nil {
		return nil, err
	}
	rateRows, err := l.q.ListDuesRatesForPricing(ctx, fundID)
	if err != nil {
		return nil, err
	}
	prices := newPricing(tierRows, rateRows)
	paidRows, err := l.q.DuesPaidByPeriod(ctx, store.DuesPaidByPeriodParams{FundID: fundID, DuesPeriod: &period})
	if err != nil {
		return nil, err
	}
	paidByMember := map[int64]money.Amount{}
	for _, row := range paidRows {
		paidByMember[*row.MemberID] = money.FromDB(row.PaidAmount)
	}
	latestRows, err := l.q.LatestDuesPeriodPaidByMember(ctx, fundID)
	if err != nil {
		return nil, err
	}
	latestByMember := map[int64]string{}
	for _, row := range latestRows {
		latestByMember[*row.MemberID] = row.LatestPeriod
	}

	var statuses []MemberDuesStatus
	for _, m := range members {
		if !memberOwesPeriod(m, period) {
			continue
		}
		tierID, rate, ok := prices.priceAt(m.ID, period)
		if !ok {
			continue
		}
		owed, paid := money.FromDB(rate), paidByMember[m.ID]
		status := classifyDuesStatus(owed, paid)
		var through string
		if status == DuesStatusPaid {
			if latest, ok := latestByMember[m.ID]; ok && latest > period {
				status = DuesStatusPaidInAdvance
				memberPaid, err := l.q.DuesPaidByMemberGroupedByPeriod(ctx, store.DuesPaidByMemberGroupedByPeriodParams{FundID: fundID, MemberID: &m.ID})
				if err != nil {
					return nil, err
				}
				byPeriod := map[string]money.Amount{}
				for _, row := range memberPaid {
					byPeriod[row.DuesPeriod] = money.FromDB(row.PaidAmount)
				}
				if through, err = paidThrough(m, prices, byPeriod, period, latest); err != nil {
					return nil, err
				}
			}
		}
		statuses = append(statuses, MemberDuesStatus{
			Member: Member{Member: m, TierID: &tierID}, OwedAmount: owed, PaidAmount: paid, Status: status, PaidThrough: through,
		})
	}
	return statuses, nil
}

// --- a fixture that hits every rule -------------------------------------------

type richFund struct {
	l      *Ledger
	f      fixture
	member map[string]int64

	savingsID int64 // a bank account nothing was ever posted on
	emptyID   int64 // a purpose nothing was ever posted on
	envelope  int64 // an open incidental with contributions
	closedEnv int64 // an incidental that was closed, its leftover rolled into Kas Utama
}

var richNow = time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)

const richOccurred = "2026-10-10"

func newRichFund(t *testing.T) richFund {
	t.Helper()
	ctx := context.Background()
	l, _ := newCountingLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	r := richFund{l: l, f: f, member: map[string]int64{}}

	// --- tiers and rates -----------------------------------------------------
	tierA := createDuesTier(t, q, f.fundID, "A")
	createDuesRate(t, q, tierA, 10_000, "2025-01")
	tierB := createDuesTier(t, q, f.fundID, "B")
	createDuesRate(t, q, tierB, 20_000, "2025-01")
	// A rise entered wrongly and corrected, and a rise entered and taken back.
	rise, err := l.CreateDuesRate(ctx, CreateDuesRateParams{FundID: f.fundID, TierID: tierB, Amount: 40_000, EffectiveFrom: "2026-03", Now: richNow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.CorrectDuesRate(ctx, f.fundID, rise.ID, 30_000); err != nil {
		t.Fatal(err)
	}
	taken, err := l.CreateDuesRate(ctx, CreateDuesRateParams{FundID: f.fundID, TierID: tierB, Amount: 50_000, EffectiveFrom: "2026-08", Now: richNow})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.DeleteDuesRate(ctx, f.fundID, taken.ID); err != nil {
		t.Fatal(err)
	}
	tierC := createDuesTier(t, q, f.fundID, "C (rate decided late)")
	createDuesRate(t, q, tierC, 15_000, "2026-06")
	tierD := createDuesTier(t, q, f.fundID, "D (no rate yet)")

	// --- members -------------------------------------------------------------
	add := func(name string, tier *int64, joined string, inactive *string) int64 {
		id := createDuesMember(t, q, f.fundID, duesMemberParams{name: name, tierID: tier, joinedOn: &joined, inactiveOn: inactive})
		r.member[name] = id
		return id
	}
	inactiveApril := "2026-04-30"
	ani := add("Ani", &tierA, "2025-01-10", nil)
	budi := add("Budi", &tierA, "2025-01-01", nil)
	setMemberTier(t, q, f.fundID, budi, &tierB, "2026-03", 0) // promoted mid-year
	add("Citra", &tierA, "2026-05-20", nil)                   // joined mid-month
	dewi := add("Dewi", nil, "2025-01-01", nil)               // untiered
	eka := add("Eka", &tierB, "2025-06-01", nil)
	fajar := add("Fajar", &tierC, "2025-01-01", nil)
	gita := add("Gita", &tierA, "2025-01-01", &inactiveApril)
	hari := add("Hari", &tierA, "2025-01-01", nil)
	add("Indra", &tierD, "2025-01-01", nil)
	joko := add("Joko", &tierA, "2025-03-01", nil)
	lina := add("Lina", &tierA, "2025-01-01", nil)
	setMemberTier(t, q, f.fundID, lina, nil, "2026-06", 0) // dropped to no tier

	pay := func(memberID int64, amount money.Amount, periods ...string) []store.Transaction {
		t.Helper()
		rows := make([]PeriodAmount, len(periods))
		for i, p := range periods {
			rows[i] = PeriodAmount{DuesPeriod: p, Amount: amount}
		}
		posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: memberID, OccurredOn: richOccurred, Periods: rows, Now: richNow,
		})
		if err != nil {
			t.Fatalf("PostDuesPayments(member %d, %v) = %v", memberID, periods, err)
		}
		return posted
	}
	pay(ani, 10_000, span("2025-01", 23)...)  // square through November 2026: paid ahead of October
	pay(budi, 10_000, span("2025-01", 12)...) // at the old rate throughout: short from March
	pay(dewi, 10_000, "2026-02")              // payment from a member who owes nothing
	pay(eka, 20_000, span("2025-06", 10)...)
	pay(eka, 30_000, "2026-04", "2026-05", "2026-06")
	pay(eka, 30_000, "2026-08", "2026-09", "2026-10", "2026-12") // a gap in November, then ahead
	pay(fajar, 15_000, span("2026-06", 6)...)
	pay(gita, 10_000, span("2025-01", 6)...)
	reversed := pay(hari, 10_000, "2026-01", "2026-02", "2026-03", "2026-04")
	if _, err := l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: reversed[1].ID, Now: richNow}); err != nil {
		t.Fatal(err)
	}
	pay(joko, 4_000, span("2025-03", 5)...) // short every month it paid
	pay(joko, 10_000, span("2025-08", 3)...)
	pay(lina, 10_000, span("2025-01", 12)...)

	// --- the money: accounts, purposes, an envelope, a pass-through -----------
	r.savingsID = createAccount(t, q, f.fundID, "bank", "Savings")
	r.emptyID = createPurpose(t, q, f.fundID, "incidental", "Never used")
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 500_000, "2025-01-01")
	postOpeningBalance(t, l, f.fundID, f.bankID, f.mainID, 300_000, "2025-01-01")

	post := func(account, purpose int64, direction string, amount money.Amount, adjustment bool) store.Transaction {
		t.Helper()
		tx, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: account, PurposeID: purpose, Direction: direction, Amount: amount,
			OccurredOn: richOccurred, IsAdjustment: adjustment, Now: richNow,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d) = %v", direction, amount, err)
		}
		return tx
	}
	post(f.cashID, f.mainID, "in", 120_000, false)
	spend := post(f.bankID, f.mainID, "out", 45_000, false)
	post(f.bankID, f.passID, "in", 90_000, false) // a pass-through never touches Kas Utama
	post(f.bankID, f.passID, "out", 30_000, false)
	post(f.cashID, f.mainID, "out", 2_500, true) // an adjusting entry
	if _, err := l.ReverseTransaction(ctx, ReverseTransactionParams{FundID: f.fundID, TransactionID: spend.ID, Now: richNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID, FromAccountID: f.cashID, ToAccountID: f.bankID, Amount: 70_000, OccurredOn: richOccurred, Now: richNow,
	}); err != nil {
		t.Fatal(err)
	}

	open := func(occasion string) int64 {
		t.Helper()
		inc, err := l.OpenIncidental(ctx, OpenIncidentalParams{FundID: f.fundID, Occasion: occasion, OpenedOn: "2026-09-01"})
		if err != nil {
			t.Fatal(err)
		}
		return inc.PurposeID
	}
	r.envelope = open("Pernikahan")
	post(f.cashID, r.envelope, "in", 80_000, false)
	post(f.cashID, r.envelope, "out", 20_000, false)
	r.closedEnv = open("Arisan")
	post(f.bankID, r.closedEnv, "in", 60_000, false)
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: r.closedEnv, AccountID: f.bankID, ClosedOn: richOccurred, Now: richNow,
	}); err != nil {
		t.Fatal(err)
	}
	// A row filed under the wrong purpose, then corrected; and a move between purposes.
	wrong := post(f.cashID, f.mainID, "in", 33_000, false)
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: wrong.ID, PurposeID: r.envelope}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PostPurposeMove(ctx, PostPurposeMoveParams{
		FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: r.envelope, AccountID: f.cashID, Amount: 15_000, OccurredOn: richOccurred, Now: richNow,
	}); err != nil {
		t.Fatal(err)
	}

	// A claim someone fronted and nobody paid yet: owed, and inside the total.
	if _, err := l.CreateReimbursement(ctx, CreateReimbursementParams{
		FundID: f.fundID, MemberID: ani, PurposeID: f.mainID, Amount: 25_000, IncurredOn: richOccurred, Now: richNow,
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

// span is n consecutive "YYYY-MM" periods from `from`.
func span(from string, n int) []string {
	start, _ := time.Parse(duesPeriodLayout, from)
	out := make([]string, n)
	for i := range out {
		out[i] = start.AddDate(0, i, 0).Format(duesPeriodLayout)
	}
	return out
}

// --- the batches return what the one-at-a-time reads return ---------------------

func TestArrearsForAPageEqualsArrearsOneMemberAtATime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRichFund(t)
	members, err := r.l.q.ListMembersByFund(ctx, r.f.fundID)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[int]bool{}
	// Every month the fixture has anything in, and a year either side of it.
	for _, current := range span("2024-01", 48) {
		got, err := r.l.ArrearsMonthsForMembers(ctx, r.f.fundID, members, current)
		if err != nil {
			t.Fatalf("ArrearsMonthsForMembers(%s) = %v", current, err)
		}
		if len(got) != len(members) {
			t.Fatalf("ArrearsMonthsForMembers(%s) has %d entries, want one per member (%d)", current, len(got), len(members))
		}
		for _, m := range members {
			want, err := r.l.ArrearsMonthsForMember(ctx, r.f.fundID, m.ID, current)
			if err != nil {
				t.Fatal(err)
			}
			if got[m.ID] != want {
				t.Errorf("arrears of %s at %s = %d in the batch, %d one at a time", m.Name, current, got[m.ID], want)
			}
			seen[want] = true
		}
	}
	if len(seen) < 4 {
		t.Errorf("the fixture produced only the arrears counts %v, want a spread (zero and several non-zero)", seen)
	}

	// A page is a subset: its members read the same as in the full roster.
	page := members[2:5]
	got, err := r.l.ArrearsMonthsForMembers(ctx, r.f.fundID, page, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range page {
		want, _ := r.l.ArrearsMonthsForMember(ctx, r.f.fundID, m.ID, "2026-10")
		if got[m.ID] != want {
			t.Errorf("paged arrears of %s = %d, want %d", m.Name, got[m.ID], want)
		}
	}
	if len(got) != len(page) {
		t.Errorf("a page of %d members answered %d entries", len(page), len(got))
	}

	if none, err := r.l.ArrearsMonthsForMembers(ctx, r.f.fundID, nil, "2026-10"); err != nil || len(none) != 0 {
		t.Errorf("ArrearsMonthsForMembers(no members) = %v, %v, want an empty answer", none, err)
	}
	if _, err := r.l.ArrearsMonthsForMembers(ctx, r.f.fundID, members, "2026-13"); err == nil {
		t.Error("ArrearsMonthsForMembers(malformed period) = nil error, want ErrInvalidArgument")
	}
}

func TestDuesStatusRosterEqualsTheOneAtATimeRoster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRichFund(t)

	seen := map[DuesStatus]bool{}
	through := false
	for _, period := range span("2024-12", 26) {
		got, err := r.l.DuesStatusForPeriod(ctx, r.f.fundID, period)
		if err != nil {
			t.Fatalf("DuesStatusForPeriod(%s) = %v", period, err)
		}
		want, err := duesStatusPerMember(ctx, r.l, r.f.fundID, period)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("DuesStatusForPeriod(%s):\n got  %+v\n want %+v", period, got, want)
		}

		// And against the other derivation of the same figures: a member is
		// outstanding for a period exactly when the walk lists it, with the same
		// amounts.
		for _, row := range got {
			seen[row.Status] = true
			if row.PaidThrough != "" {
				through = true
			}
			outstanding, err := r.l.OutstandingDuesForMember(ctx, r.f.fundID, row.Member.ID, period, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			var listed *OutstandingDuesPeriod
			for i := range outstanding {
				if outstanding[i].Period == period {
					listed = &outstanding[i]
				}
			}
			owing := row.Status == DuesStatusUnpaid || row.Status == DuesStatusPartial
			switch {
			case owing && listed == nil:
				t.Errorf("%s at %s is %s but OutstandingDuesForMember does not list the period", row.Member.Name, period, row.Status)
			case owing && (listed.OwedAmount != row.OwedAmount || listed.PaidAmount != row.PaidAmount || listed.Status != row.Status):
				t.Errorf("%s at %s: roster %+v, walk %+v", row.Member.Name, period, row, *listed)
			case !owing && listed != nil:
				t.Errorf("%s at %s is %s but OutstandingDuesForMember lists the period", row.Member.Name, period, row.Status)
			}
		}
	}
	for _, status := range []DuesStatus{DuesStatusUnpaid, DuesStatusPartial, DuesStatusPaid, DuesStatusPaidInAdvance} {
		if !seen[status] {
			t.Errorf("no member read %s in any period: the fixture does not reach that rule", status)
		}
	}
	if !through {
		t.Error("no paid-in-advance row carried a paid-through period: the fixture does not reach paidThrough")
	}

	// Eka paid August to October and December 2026 but not November: at August
	// she is paid through October, not December.
	rows, err := r.l.DuesStatusForPeriod(ctx, r.f.fundID, "2026-08")
	if err != nil {
		t.Fatal(err)
	}
	eka, ok := statusFor(t, rows, r.member["Eka"])
	if !ok || eka.Status != DuesStatusPaidInAdvance || eka.PaidThrough != "2026-10" {
		t.Errorf("Eka at 2026-08 = %+v, want paid in advance, through 2026-10 (November was skipped)", eka)
	}
}

func TestBalancesEqualTheOneAtATimeBalances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRichFund(t)

	got, err := r.l.Balances(ctx, r.f.fundID, richNow)
	if err != nil {
		t.Fatal(err)
	}

	accounts, err := r.l.q.ListAccountsByFund(ctx, r.f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != len(accounts) {
		t.Fatalf("Balances has %d accounts, want %d", len(got.Accounts), len(accounts))
	}
	var sum money.Amount
	for i, a := range accounts {
		want, err := r.l.AccountBalance(ctx, r.f.fundID, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Accounts[i].Account != a || got.Accounts[i].Balance != want {
			t.Errorf("account %d = %+v, want %+v with balance %d", i, got.Accounts[i], a, want)
		}
		sum += want
	}
	purposes, err := r.l.q.ListPurposesByFund(ctx, r.f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Purposes) != len(purposes) {
		t.Fatalf("Balances has %d purposes, want %d", len(got.Purposes), len(purposes))
	}
	for i, p := range purposes {
		want, err := r.l.PurposeBalance(ctx, r.f.fundID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Purposes[i].Purpose != p || got.Purposes[i].Balance != want {
			t.Errorf("purpose %d = %+v, want %+v with balance %d", i, got.Purposes[i], p, want)
		}
	}

	wantTotal, err := r.l.FundBalance(ctx, r.f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	wantOwed, err := r.l.OwedToMembers(ctx, r.f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	wantIn, wantOut, err := r.l.MonthInOut(ctx, r.f.fundID, richNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.FundTotal != wantTotal || got.OwedToMembers != wantOwed || got.MonthIn != wantIn || got.MonthOut != wantOut {
		t.Errorf("Balances = total %d owed %d month %d/%d, want %d %d %d/%d",
			got.FundTotal, got.OwedToMembers, got.MonthIn, got.MonthOut, wantTotal, wantOwed, wantIn, wantOut)
	}
	if got.FundTotal != sum {
		t.Errorf("fund total %d != the accounts added up (%d)", got.FundTotal, sum)
	}

	// The fixture reaches the cases that matter: an untouched account and purpose
	// read zero, the pass-through holds its own money, the closed envelope rolled
	// to nothing, and something is owed.
	balanceOf := func(purposeID int64) money.Amount {
		for _, p := range got.Purposes {
			if p.Purpose.ID == purposeID {
				return p.Balance
			}
		}
		t.Fatalf("purpose %d is not in the answer", purposeID)
		return 0
	}
	var savings *AccountBalanceRow
	for i := range got.Accounts {
		if got.Accounts[i].Account.ID == r.savingsID {
			savings = &got.Accounts[i]
		}
	}
	if savings == nil || savings.Balance != 0 {
		t.Errorf("the untouched account = %+v, want present at Rp 0", savings)
	}
	if balanceOf(r.emptyID) != 0 || balanceOf(r.closedEnv) != 0 {
		t.Errorf("untouched %d and closed %d purposes, want Rp 0 each", balanceOf(r.emptyID), balanceOf(r.closedEnv))
	}
	if balanceOf(r.f.passID) != 60_000 || balanceOf(r.envelope) == 0 || got.OwedToMembers != 25_000 {
		t.Errorf("pass-through %d, envelope %d, owed %d, want 60000, non-zero, 25000", balanceOf(r.f.passID), balanceOf(r.envelope), got.OwedToMembers)
	}

	if _, err := r.l.Balances(ctx, 999, richNow); err == nil {
		t.Error("Balances(missing fund) = nil error, want an error")
	}
}

// --- and cost the same however big the fund is --------------------------------

func TestRosterAndBalanceReadsDoNotGrowWithTheFund(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type cost struct{ page, status, balances int64 }
	measure := func(members, months, extraAccounts, extraPurposes int) cost {
		l, n := newCountingLedger(t)
		sf := newScaleFund(t, l, members, months)
		q := store.New(l.db)
		for i := 0; i < extraAccounts; i++ {
			createAccount(t, q, sf.f.fundID, "bank", fmt.Sprintf("Rekening %d", i))
		}
		for i := 0; i < extraPurposes; i++ {
			p := createPurpose(t, q, sf.f.fundID, "incidental", fmt.Sprintf("Acara %d", i))
			if _, err := l.PostTransaction(ctx, PostTransactionParams{
				FundID: sf.f.fundID, AccountID: sf.f.cashID, PurposeID: p, Direction: "in", Amount: 1_000, OccurredOn: "2026-10-01", Now: scaleNow,
			}); err != nil {
				t.Fatal(err)
			}
		}

		var c cost
		c.page = queriesRun(n, func() {
			rows, err := q.ListMembersPage(ctx, store.ListMembersPageParams{FundID: sf.f.fundID, CurrentPeriod: scalePeriod, PageLimit: 26})
			if err != nil {
				t.Fatal(err)
			}
			page := make([]store.Member, len(rows))
			for i, m := range rows {
				page[i] = store.Member{ID: m.ID, FundID: m.FundID, Name: m.Name, JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt}
			}
			if _, err := l.ArrearsMonthsForMembers(ctx, sf.f.fundID, page, scalePeriod); err != nil {
				t.Fatal(err)
			}
		})
		c.status = queriesRun(n, func() {
			rows, err := l.DuesStatusForPeriod(ctx, sf.f.fundID, "2026-09")
			if err != nil {
				t.Fatal(err)
			}
			advance := 0
			for _, row := range rows {
				if row.Status == DuesStatusPaidInAdvance {
					advance++
				}
			}
			if advance == 0 {
				t.Fatal("no paid-in-advance row: the fixture does not reach the batched paidThrough read")
			}
		})
		c.balances = queriesRun(n, func() {
			if _, err := l.Balances(ctx, sf.f.fundID, scaleNow); err != nil {
				t.Fatal(err)
			}
		})
		return c
	}

	small := measure(4, 6, 0, 0)
	large := measure(24, 36, 6, 6)
	if small.page == 0 || small.status == 0 || small.balances == 0 {
		t.Fatalf("queries = %+v, want every path counted: a zero means the counting driver saw nothing", small)
	}
	if small != large {
		t.Errorf("queries for 4 members over 6 months and a bare fund = %+v; for 24 over 36 months with 6 more accounts and purposes = %+v; want the same", small, large)
	}
}
