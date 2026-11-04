package http

import (
	"context"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/report"
	"github.com/kerti/uruni/internal/store"
)

// ADR-048: the public page and the PDF list a dues payment as one row, in the
// words of the approved copy, and every figure on them stays what it was.
func TestReportListsAMultiMonthPaymentAsOneRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	m, err := store.New(f.db).CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01", FundID: f.fund.ID, Name: "Ani", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateMember() = %v", err)
	}
	pay := func(on string, periods ...string) []store.Transaction {
		var rows []ledger.PeriodAmount
		for _, p := range periods {
			rows = append(rows, ledger.PeriodAmount{DuesPeriod: p, Amount: 25_000})
		}
		posted, err := f.l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
			FundID: f.fund.ID, AccountID: f.cashID, MemberID: m.ID, OccurredOn: on, Periods: rows,
		})
		if err != nil {
			t.Fatalf("PostDuesPayments(%v) = %v", periods, err)
		}
		return posted
	}
	pay("2026-09-05", "2026-05", "2026-06", "2026-10")
	one := pay("2026-09-06", "2026-09")
	if _, err := f.l.ReverseDuesPayment(ctx, ledger.ReverseDuesPaymentParams{FundID: f.fund.ID, DuesPaymentID: *one[0].DuesPaymentID}); err != nil {
		t.Fatalf("ReverseDuesPayment() = %v", err)
	}

	path := "/report/" + f.fund.ReportSlug + "?month=2026-09"
	body := f.get(t, path).Body.String()
	section := txnSection(t, body)

	if got := rowCount(t, body); got != 3 {
		t.Fatalf("rows = %d, want 3: the payment, the one-month payment and its reversal\n%s", got, section)
	}
	for _, want := range []string{
		"Mei" + report.PeriodHyphen + "Jun, Okt 2026 \u00b7 Ani", // the three-month payment: one row, approved copy; the page never breaks a range
		"September 2026 \u00b7 Ani",                              // a single month reads as before
		"Pembatalan \u00b7 September 2026",                       // its reversal, in today's wording
		report.Text.RowReversed,                                  // the one-month payment is reversed whole
		money.FormatIDR(75_000),                                  // the sum of the three months
	} {
		if !strings.Contains(section, want) {
			t.Errorf("transactions list lacks %q:\n%s", want, section)
		}
	}
	// The walk counts every row: 75.000 + 25.000 in, the reversal taken off.
	var gotInOut []totalLine
	for _, line := range totalsOf(t, body) {
		if line.label == report.Text.TotalIn || line.label == report.Text.TotalOut {
			gotInOut = append(gotInOut, line)
		}
	}
	if want := inOut(75_000, 0); !equalLines(gotInOut, want) {
		t.Errorf("totals = %v, want %v", gotInOut, want)
	}

	pdf := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-09").Body.Bytes()
	text := strings.Join(pdfText(t, pdf), "\n")
	for _, want := range []string{"Mei-Jun, Okt 2026 \u00b7 Ani", "Pembatalan \u00b7 September 2026"} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF lacks %q", want)
		}
	}
}
