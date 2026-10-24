package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// seedYearFund builds a fund the size of a busy year (#520): about 1,500
// transactions over twelve months across four purposes and two locations, 60
// members on one dues tier with September paid, an open envelope and a
// reconciliation. It returns the router serving the report and the slug.
func seedYearFund(b *testing.B) (http.Handler, string) {
	b.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, ":memory:")
	if err != nil {
		b.Fatalf("db.Open() = %v", err)
	}
	b.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Up(ctx, sqlDB, testLogger()); err != nil {
		b.Fatalf("db.Up() = %v", err)
	}

	l := ledger.New(sqlDB, ledger.WithClock(fixedClock(reportNow)))
	q := store.New(sqlDB)
	res, err := l.SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: "Kas RT 05",
		Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}, {Kind: "bank", Name: "Bank"}},
	})
	if err != nil {
		b.Fatalf("SetUpFund() = %v", err)
	}
	cash, bank := res.Accounts[0].ID, res.Accounts[1].ID
	purposes := []int64{res.MainPurposeID}
	for _, name := range []string{"Keamanan", "Kebersihan", "Sosial"} {
		p, err := q.CreatePurpose(ctx, store.CreatePurposeParams{FundID: res.Fund.ID, Kind: "pass_through", Name: name, CreatedAt: 1})
		if err != nil {
			b.Fatalf("CreatePurpose(%s) = %v", name, err)
		}
		purposes = append(purposes, p.ID)
	}

	// 1,500 entries: about four a day over 2025-10-01 .. 2026-09-28.
	day := time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 1500 {
		d := day.AddDate(0, 0, i/4%363)
		dir, amount := ledger.DirectionIn, money.Amount(20_000+i%7*5_000)
		if i%3 == 0 {
			dir = ledger.DirectionOut
		}
		note := fmt.Sprintf("Catatan %d", i)
		acct := cash
		if i%2 == 0 {
			acct = bank
		}
		if _, err := l.PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: res.Fund.ID, AccountID: acct, PurposeID: purposes[i%len(purposes)],
			Direction: dir, Amount: amount, OccurredOn: d.Format("2006-01-02"), Note: &note,
		}); err != nil {
			b.Fatalf("PostTransaction(%d) = %v", i, err)
		}
	}

	tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{FundID: res.Fund.ID, Name: "Madya", CreatedAt: 1})
	if err != nil {
		b.Fatalf("CreateDuesTier() = %v", err)
	}
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{TierID: tier.ID, Amount: 25_000, EffectiveFrom: "2025-01", CreatedAt: 1}); err != nil {
		b.Fatalf("CreateDuesRate() = %v", err)
	}
	for i := range 60 {
		m, err := l.CreateMember(ctx, ledger.CreateMemberParams{
			FundID: res.Fund.ID, Name: fmt.Sprintf("Anggota %02d", i), TierID: &tier.ID, JoinedOn: "2025-01-01",
		})
		if err != nil {
			b.Fatalf("CreateMember() = %v", err)
		}
		if i%4 == 3 {
			continue // a quarter still owe September
		}
		if _, err := l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
			FundID: res.Fund.ID, AccountID: cash, MemberID: m.ID, OccurredOn: "2026-09-05",
			Periods: []ledger.PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
		}); err != nil {
			b.Fatalf("PostDuesPayments() = %v", err)
		}
	}

	minimum := money.Amount(20_000)
	if _, err := l.OpenIncidental(ctx, ledger.OpenIncidentalParams{
		FundID: res.Fund.ID, Occasion: "Tujuhbelasan", OpenedOn: "2026-08-01", MinimumPerMember: &minimum,
	}); err != nil {
		b.Fatalf("OpenIncidental() = %v", err)
	}
	if _, err := l.TakeReconciliation(ctx, ledger.TakeReconciliationParams{
		FundID: res.Fund.ID,
		Counts: []ledger.AccountCount{
			{AccountID: cash, ActualAmount: 1, Resolution: "left_open"},
			{AccountID: bank, ActualAmount: 1, Resolution: "left_open"},
		},
	}); err != nil {
		b.Fatalf("TakeReconciliation() = %v", err)
	}

	r := chi.NewRouter()
	r.Get("/report/{slug}", reportHandler(l, q, testLogger()))
	r.Get("/report/{slug}/pdf", reportPDFHandler(l, q, testLogger()))
	return r, res.Fund.ReportSlug
}

func benchReport(b *testing.B, path func(slug string) string, wantType string) {
	r, slug := seedYearFund(b)
	target := path(slug)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK {
			b.Fatalf("GET report = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != wantType {
			b.Fatalf("Content-Type = %q, want %q", got, wantType)
		}
	}
}

// BenchmarkReportHTML is one render of September for the seeded year.
func BenchmarkReportHTML(b *testing.B) {
	benchReport(b, func(slug string) string { return "/report/" + slug + "?month=2026-09" }, "text/html; charset=utf-8")
}

// BenchmarkReportPDF is one PDF of the same month, fonts and all.
func BenchmarkReportPDF(b *testing.B) {
	benchReport(b, func(slug string) string { return "/report/" + slug + "/pdf?month=2026-09" }, "application/pdf")
}
