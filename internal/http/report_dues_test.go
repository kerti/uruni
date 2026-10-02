package http

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// duesScenario is September 2026 with one member in every status, two who
// are not owing that month, and one with no tier:
//
//	Ani    paid 25.000 of 25.000 for 2026-09          Lunas
//	Budi   nothing                                    Belum bayar
//	Cici   paid 10.000 of 25.000                      Bayar sebagian
//	Dedi   paid 2026-09 and 2026-10                   Lunas, sampai Oktober 2026
//	Eka    paid 2026-09 and 2026-11, skipped October  Lunas - sudah bayar di muka
//	Fina   joined 2026-10-01                          not owing September
//	Gita   inactive from 2026-08-31                   not owing September
//	Hadi   no golongan                                owes nothing
type duesScenario struct {
	reportFixture
	base string
}

func newDuesScenario(t *testing.T) duesScenario {
	t.Helper()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	q := store.New(f.db)

	tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{FundID: f.fund.ID, Name: "Madya", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateDuesTier() = %v", err)
	}
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{TierID: tier.ID, Amount: 25_000, EffectiveFrom: "2026-01", CreatedAt: 1}); err != nil {
		t.Fatalf("CreateDuesRate() = %v", err)
	}
	str := func(s string) *string { return &s }
	mk := func(name string, tierID *int64, joined, inactive *string) int64 {
		m, err := q.CreateMember(ctx, store.CreateMemberParams{
			FundID: f.fund.ID, Name: name, TierID: tierID, JoinedOn: joined, InactiveOn: inactive, CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("CreateMember(%s) = %v", name, err)
		}
		return m.ID
	}
	ani := mk("Ani", &tier.ID, nil, nil)
	mk("Budi", &tier.ID, nil, nil)
	cici := mk("Cici", &tier.ID, nil, nil)
	dedi := mk("Dedi", &tier.ID, nil, nil)
	eka := mk("Eka", &tier.ID, nil, nil)
	mk("Fina", &tier.ID, str("2026-10-01"), nil)
	mk("Gita", &tier.ID, nil, str("2026-08-31"))
	mk("Hadi", nil, nil, nil)

	pay := func(member int64, amount money.Amount, period string) {
		t.Helper()
		if _, err := f.l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
			FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, MemberID: member, OccurredOn: "2026-09-05",
			Periods: []ledger.PeriodAmount{{DuesPeriod: period, Amount: amount}},
		}); err != nil {
			t.Fatalf("PostDuesPayments(%s) = %v", period, err)
		}
	}
	pay(ani, 25_000, "2026-09")
	pay(cici, 10_000, "2026-09")
	pay(dedi, 25_000, "2026-09")
	pay(dedi, 25_000, "2026-10")
	pay(eka, 25_000, "2026-09")
	pay(eka, 25_000, "2026-11")
	return duesScenario{reportFixture: f, base: "/report/" + f.fund.ReportSlug + "?month=2026-09"}
}

// duesSection is the dues section's markup alone, unescaped.
func duesSection(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `id="dues"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</section>")
	return html.UnescapeString(body[i : i+j])
}

func duesNames(sec string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<span class="name">([^<]*)</span>`).FindAllStringSubmatch(sec, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestReportDuesShowsEveryStatusInTheAppsWords(t *testing.T) {
	s := newDuesScenario(t)
	rec := s.get(t, s.base)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	sec := duesSection(t, rec.Body.String())
	if sec == "" {
		t.Fatal("no dues section in body")
	}
	if got, want := strings.Join(duesNames(sec), ","), "Ani,Budi,Cici,Dedi,Eka"; got != want {
		t.Errorf("members = %q, want %q (name order; Fina, Gita and Hadi do not owe September)", got, want)
	}
	for _, want := range []string{
		`<span class="badge paid">Lunas</span>`,
		`<span class="badge unpaid">Belum bayar</span>`,
		`<span class="badge partial">Bayar sebagian</span>`,
		"Lunas \u2014 sudah bayar di muka",
		"Golongan Madya",
		"Iuran: Rp\u00a025.000",
		"Dibayar: Rp\u00a010.000",
		"Dibayar: Rp\u00a00",
		"Sudah dibayar sampai Oktober 2026",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("dues section lacks %q:\n%s", want, sec)
		}
	}
	if strings.Count(sec, `class="badge paid"`) != 2 { // Ani and Dedi
		t.Errorf("paid badges = %d, want 2 (Ani, and Dedi whose end month is known)", strings.Count(sec, `class="badge paid"`))
	}
	if !strings.Contains(sec, `class="badge paid_in_advance"`) {
		t.Errorf("Eka (ahead with a gap) lacks the paid-in-advance badge:\n%s", sec)
	}
	for _, bad := range []string{"Fina", "Gita", "Hadi"} {
		if strings.Contains(sec, bad) {
			t.Errorf("dues section names %q, who does not owe September", bad)
		}
	}
}

func TestReportDuesFilter(t *testing.T) {
	s := newDuesScenario(t)
	for _, tc := range []struct {
		value, want string
	}{
		{"unpaid", "Budi"},
		{"partial", "Cici"},
		{"paid", "Ani,Dedi,Eka"},
		{"bogus", "Ani,Budi,Cici,Dedi,Eka"},
		{"", "Ani,Budi,Cici,Dedi,Eka"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			rec := s.get(t, s.base+"&dues="+tc.value)
			body := rec.Body.String()
			if got := strings.Join(duesNames(duesSection(t, body)), ","); got != tc.want {
				t.Errorf("dues=%q members = %q, want %q", tc.value, got, tc.want)
			}
			if tc.value != "bogus" && tc.value != "" && !strings.Contains(body, `<option value="`+tc.value+`" selected>`) {
				t.Errorf("dues=%q option is not marked selected", tc.value)
			}
			if (tc.value == "bogus" || tc.value == "") && regexp.MustCompile(`<option value="(unpaid|partial|paid)" selected>`).MatchString(body) {
				t.Errorf("dues=%q marked an option selected, want none", tc.value)
			}
		})
	}
}

func TestReportDuesFilterLeavesTransactionsAlone(t *testing.T) {
	s := newDuesScenario(t)
	all := txnSection(t, s.get(t, s.base).Body.String())
	filtered := txnSection(t, s.get(t, s.base+"&dues=unpaid").Body.String())
	if all == "" || all != filtered {
		t.Errorf("transactions changed under dues=unpaid:\nall:\n%s\nfiltered:\n%s", all, filtered)
	}
}

func TestReportDuesFilterWithNoMatchKeepsSectionAndFilter(t *testing.T) {
	s := newDuesScenario(t)
	// Everyone but Budi has paid something, so "unpaid" is Budi alone;
	// narrowing by month to October leaves partial empty.
	rec := s.get(t, "/report/"+s.fund.ReportSlug+"?month=2026-09&dues=partial")
	if sec := duesSection(t, rec.Body.String()); !strings.Contains(sec, "Cici") {
		t.Fatalf("partial should list Cici:\n%s", sec)
	}
	// Pay Cici off, then nobody is partial: the section stays, with its line.
	q := store.New(s.db)
	members, _ := q.ListMembersByFund(context.Background(), s.fund.ID)
	for _, m := range members {
		if m.Name == "Cici" {
			if _, err := s.l.PostDuesPayments(context.Background(), ledger.PostDuesPaymentsParams{
				FundID: s.fund.ID, AccountID: s.cashID, PurposeID: s.mainID, MemberID: m.ID, OccurredOn: "2026-09-06",
				Periods: []ledger.PeriodAmount{{DuesPeriod: "2026-09", Amount: 15_000}},
			}); err != nil {
				t.Fatalf("PostDuesPayments() = %v", err)
			}
		}
	}
	body := s.get(t, s.base+"&dues=partial").Body.String()
	sec := duesSection(t, body)
	if sec == "" || !strings.Contains(sec, "Tidak ada anggota yang cocok bulan ini.") {
		t.Errorf("an empty filtered result should keep the section with its line:\n%s", sec)
	}
	if !strings.Contains(body, `<select name="dues">`) {
		t.Error("the dues filter must stay so the visitor can clear it")
	}
}

func TestReportDuesFilterIsCarriedByMonthLinks(t *testing.T) {
	s := newDuesScenario(t)
	body := s.get(t, s.base+"&dues=unpaid").Body.String()
	if !strings.Contains(body, `<input type="hidden" name="dues" value="unpaid">`) {
		t.Error("the month form does not carry dues=unpaid")
	}
	m := regexp.MustCompile(`href="(\?month=[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no month step link in body")
	}
	u, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("dues") != "unpaid" {
		t.Errorf("month link %q lacks dues=unpaid", m[1])
	}
}

func TestReportDuesHasNoArrearsWording(t *testing.T) {
	s := newDuesScenario(t)
	for _, path := range []string{s.base, s.base + "&dues=unpaid", "/report/" + s.fund.ReportSlug + "?month=2026-10"} {
		body := strings.ToLower(s.get(t, path).Body.String())
		for _, bad := range []string{"tunggakan", "arrears", "bulan lagi", "terlambat"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q", path, bad)
			}
		}
	}
}

func TestReportDuesOnlyMembersOwingThatMonth(t *testing.T) {
	s := newDuesScenario(t)
	// October: Fina has joined, Gita is still gone.
	sec := duesSection(t, s.get(t, "/report/"+s.fund.ReportSlug+"?month=2026-10").Body.String())
	got := strings.Join(duesNames(sec), ",")
	if !strings.Contains(got, "Fina") || strings.Contains(got, "Gita") {
		t.Errorf("October members = %q, want Fina in and Gita out", got)
	}
}

func TestReportDuesSectionHidden(t *testing.T) {
	t.Run("no tiers", func(t *testing.T) {
		f := newReportFixture(t, "Kas RT 05")
		f.post(t, "in", 10_000, "2026-09-03")
		body := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-09").Body.String()
		if strings.Contains(body, `id="dues"`) || strings.Contains(body, `name="dues"`) || strings.Contains(body, "Status iuran") {
			t.Error("a fund with no tiers shows dues markup")
		}
	})
	t.Run("no member owing that month", func(t *testing.T) {
		ctx := context.Background()
		f := newReportFixture(t, "Kas RT 05")
		f.post(t, "in", 10_000, "2026-09-03")
		q := store.New(f.db)
		tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{FundID: f.fund.ID, Name: "Madya", CreatedAt: 1})
		if err != nil {
			t.Fatalf("CreateDuesTier() = %v", err)
		}
		if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{TierID: tier.ID, Amount: 25_000, EffectiveFrom: "2026-01", CreatedAt: 1}); err != nil {
			t.Fatalf("CreateDuesRate() = %v", err)
		}
		joined := "2026-12-01"
		if _, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: f.fund.ID, Name: "Fina", TierID: &tier.ID, JoinedOn: &joined, CreatedAt: 1}); err != nil {
			t.Fatalf("CreateMember() = %v", err)
		}
		body := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-09").Body.String()
		if strings.Contains(body, `id="dues"`) || strings.Contains(body, `name="dues"`) {
			t.Error("a month nobody owes shows dues markup")
		}
		if !strings.Contains(body, `id="transactions"`) {
			t.Error("the transactions section is gone")
		}
	})
}

// The dues filter has its own form beside the table it filters; each of the
// two filter forms carries the other's choices, so neither drops them.
func TestReportDuesAndTransactionFormsCarryEachOther(t *testing.T) {
	s := newDuesScenario(t)
	body := s.get(t, s.base+"&dues=unpaid&dir=in").Body.String()

	txnForm := body[strings.Index(body, `action="#transactions"`):]
	txnForm = txnForm[:strings.Index(txnForm, "</form>")]
	if !strings.Contains(txnForm, `<input type="hidden" name="dues" value="unpaid">`) {
		t.Error("the transactions form drops the dues filter")
	}
	if strings.Contains(txnForm, `name="dues"><`) || strings.Contains(txnForm, `<select name="dues"`) {
		t.Error("the dues select belongs in the dues section, not the transactions form")
	}

	sec := duesSection(t, body)
	if !strings.Contains(sec, `action="#dues"`) || !strings.Contains(sec, `<select name="dues"`) {
		t.Error("the dues section has no form of its own")
	}
	if !strings.Contains(sec, `<input type="hidden" name="dir" value="in">`) {
		t.Error("the dues form drops the transactions filter")
	}
	if strings.Contains(sec, `type="hidden" name="dues"`) {
		t.Error("the dues form repeats dues as a hidden field")
	}
}
