package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/report"
	"github.com/kerti/uruni/internal/store"
)

// What the wire and the pages say about reversals (ADR-042): the page rows carry
// the server's answer to "may this be reversed", and a total can read below zero.

// GET /api/transactions rows carry reversed, reversible, counted and
// reverses_occurred_on; reversible is ledger.ReversalRefusal's answer, so the
// client keeps no copy of the rule.
func TestGetTransactionsRowsCarryTheReversalFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	now := time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC) // 2 October, 10:00 in Asia/Jakarta
	post := func(direction ledger.Direction, amount money.Amount, on string) int64 {
		t.Helper()
		row, err := ledgerAt(f.db, time.Date(2026, 12, 31, 3, 0, 0, 0, time.UTC)).PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, Direction: direction, Amount: amount,
			OccurredOn: on,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d on %s) = %v", direction, amount, on, err)
		}
		return row.ID
	}
	balance := func() money.Amount {
		b, err := f.l.AccountBalance(ctx, f.fund.ID, f.cashID)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	counted := post("in", 100_000, "2026-07-01")
	countedOut := post("out", 12_000, "2026-07-25")
	ahead := post("in", 4_000, "2026-12-20") // from before #632's ceiling: dated after today
	// A cek kas that finds 1.000 too little and adds the entry that explains it.
	rec, err := ledgerAt(f.db, now).TakeReconciliation(ctx, ledger.TakeReconciliationParams{FundID: f.fund.ID, Counts: []ledger.AccountCount{
		{AccountID: f.cashID, ActualAmount: balance() - 1_000, Resolution: "entry_added",
			Fix: &ledger.Fix{PurposeID: f.mainID, Direction: "out", Amount: 1_000, OccurredOn: "2026-10-02"}},
	}})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v", err)
	}
	lines, err := f.l.GetReconciliationDetail(ctx, f.fund.ID, rec.ID)
	if err != nil {
		t.Fatalf("GetReconciliationDetail() = %v", err)
	}
	var fix int64
	for _, line := range lines.Lines {
		if line.AdjustmentTransactionID != nil {
			fix = *line.AdjustmentTransactionID
		}
	}
	if fix == 0 {
		t.Fatal("the count named no fix")
	}
	uncounted := post("in", 5_000, "2026-10-01")
	uncountedReversed := post("in", 6_000, "2026-10-01")

	reversalOfCounted, err := ledgerAt(f.db, now).ReverseTransaction(ctx, ledger.ReverseTransactionParams{FundID: f.fund.ID, TransactionID: countedOut})
	if err != nil {
		t.Fatalf("ReverseTransaction(counted) = %v", err)
	}
	reversalOfUncounted, err := ledgerAt(f.db, now).ReverseTransaction(ctx, ledger.ReverseTransactionParams{FundID: f.fund.ID, TransactionID: uncountedReversed})
	if err != nil {
		t.Fatalf("ReverseTransaction(uncounted) = %v", err)
	}

	r := authedRouterAt(t, f.db, func() time.Time { return now })
	rows := map[int64]transactionResponse{}
	for _, row := range decodeTransactionsPage(t, getTransactions(t, r)).Transactions {
		rows[row.ID] = row
	}

	type facts struct {
		reversed, reversible, counted bool
		reversesOccurredOn            string
	}
	for _, tc := range []struct {
		name string
		id   int64
		want facts
	}{
		{"a counted row may be reversed (today, with a count since)", counted, facts{counted: true, reversible: true}},
		{"a reversed row is not reversible again", countedOut, facts{reversed: true, counted: true}},
		{"a row no count has covered", uncounted, facts{reversible: true}},
		{"an uncounted row, reversed", uncountedReversed, facts{reversed: true}},
		{"the reversal of a counted row, dated today", reversalOfCounted.ID, facts{reversesOccurredOn: "2026-07-25"}},
		{"the reversal of an uncounted row, dated its day", reversalOfUncounted.ID, facts{reversesOccurredOn: "2026-10-01"}},
		{"a counted row dated after today cannot be reversed", ahead, facts{counted: true}},
		{"the fix a cek kas line names", fix, facts{}},
	} {
		got, ok := rows[tc.id]
		if !ok {
			t.Errorf("%s: row %d is missing from GET /api/transactions", tc.name, tc.id)
			continue
		}
		have := facts{got.Reversed, got.Reversible, got.Counted, ""}
		if got.ReversesOccurredOn != nil {
			have.reversesOccurredOn = *got.ReversesOccurredOn
		}
		if have != tc.want {
			t.Errorf("%s: reversed/reversible/counted/reverses_occurred_on = %+v, want %+v", tc.name, have, tc.want)
		}
	}
}

// A counted spend reversed in a later month takes Total keluar below zero. Beranda's
// Arus kas bulan ini, the report page and the PDF each print it as it is, with
// the ASCII hyphen the font has (ADR-042, ADR-038).
func TestANegativeTotalKeluarIsPrintedEverywhere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-07-01")
	out, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 12_000, OccurredOn: "2026-07-25",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v", err)
	}
	if _, err := f.l.TakeReconciliation(ctx, ledger.TakeReconciliationParams{FundID: f.fund.ID, Counts: []ledger.AccountCount{
		{AccountID: f.cashID, ActualAmount: 88_000, Resolution: "matched"},
	}}); err != nil {
		t.Fatalf("TakeReconciliation() = %v", err)
	}
	// Reversed on 10 August by the injected clock: a later month than the spend.
	august := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	reversal, err := ledgerAt(f.db, august).ReverseTransaction(ctx, ledger.ReverseTransactionParams{FundID: f.fund.ID, TransactionID: out.ID})
	if err != nil {
		t.Fatalf("ReverseTransaction() = %v", err)
	}
	if reversal.OccurredOn != "2026-08-10" {
		t.Fatalf("reversal OccurredOn = %q, want 2026-08-10", reversal.OccurredOn)
	}

	// The report page: Saldo 31 Juli 88.000, nothing in, -12.000 out, Saldo 31 Agustus 100.000.
	want := []totalLine{
		{report.Text.WalkStart("31 Juli 2026"), money.FormatIDR(88_000)},
		{report.Text.TotalIn, money.FormatIDR(0)},
		{report.Text.TotalOut, money.FormatIDR(-12_000)},
		{report.Text.WalkEnd("31 Agustus 2026"), money.FormatIDR(100_000)},
	}
	page := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-08")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /report = %d, want 200", page.Code)
	}
	if got := totalsOf(t, page.Body.String()); !equalLines(got, want) {
		t.Errorf("report walk = %v, want %v", got, want)
	}

	// The PDF carries the same runs in the same order.
	pdf := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-08")
	if pdf.Code != http.StatusOK {
		t.Fatalf("GET /report/pdf = %d, want 200", pdf.Code)
	}
	runs := pdfText(t, pdf.Body.Bytes())
	var wantRuns []string
	for _, line := range want {
		wantRuns = append(wantRuns, line.label, line.value)
	}
	at := slicesIndexFrom(runs, wantRuns[0])
	if at < 0 || at+len(wantRuns) > len(runs) || !slices.Equal(runs[at:at+len(wantRuns)], wantRuns) {
		t.Errorf("PDF walk runs do not read %q; runs:\n%s", wantRuns, strings.Join(runs, "\n"))
	}
	if !strings.Contains(strings.Join(runs, "|"), "-Rp\u00a012.000") {
		t.Errorf("PDF has no %q run", "-Rp\u00a012.000")
	}

	// Beranda reads the same walk through GET /api/balances, in the running month.
	// Asked on 20 August the running month is August.
	r := authedRouterAt(t, f.db, func() time.Time { return time.Date(2026, 8, 20, 3, 0, 0, 0, time.UTC) })
	rec := getBalances(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/balances = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if m := decodeBalances(t, rec).Month; m.In != 0 || m.Out != -12_000 {
		t.Errorf("Beranda month = %+v, want in 0 out -12000", m)
	}
}

// Every refusal the ledger gives a reversal reaches the wire under its own code and
// status, and the purpose-correction route answers a reversed row with its own code
// (ADR-042). The rows are built through the ledger, the calls go through the router.
func TestReversalRefusalsReachTheWireWithTheirCodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	today := time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC) // 2 October, Asia/Jakarta
	ahead := time.Date(2026, 12, 31, 3, 0, 0, 0, time.UTC)
	r := authedRouterAt(t, f.db, func() time.Time { return today })

	post := func(direction ledger.Direction, amount money.Amount, on string, purpose int64) int64 {
		t.Helper()
		row, err := ledgerAt(f.db, ahead).PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: f.fund.ID, AccountID: f.cashID, PurposeID: purpose, Direction: direction,
			Amount: amount, OccurredOn: on,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d on %s) = %v", direction, amount, on, err)
		}
		return row.ID
	}

	// An opening balance, on a second location.
	bank, err := f.l.CreateAccount(ctx, ledger.CreateAccountParams{
		FundID: f.fund.ID, Kind: "bank", Name: "Bank",
		OpeningBalance: &ledger.OpeningBalance{Amount: 50_000, OccurredOn: "2026-07-01"},
	})
	if err != nil {
		t.Fatalf("CreateAccount() = %v", err)
	}
	var opening int64
	rows, err := store.New(f.db).ListTransactionsByFund(ctx, f.fund.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind == "opening" {
			opening = row.ID
		}
	}
	post("in", 200_000, "2026-07-02", f.mainID)

	// A settled claim's payout.
	member, err := store.New(f.db).CreateMember(ctx, store.CreateMemberParams{FundID: f.fund.ID, Name: "Budi", JoinedOn: "2000-01-01", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.New(f.db).CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: f.fund.ID, MemberID: member.ID, PurposeID: f.mainID, Amount: 10_000, IncurredOn: "2026-07-03", CreatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	payout, err := ledgerAt(f.db, today).SettleReimbursement(ctx, ledger.SettleReimbursementParams{
		FundID: f.fund.ID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-07-04",
	})
	if err != nil {
		t.Fatalf("SettleReimbursement() = %v", err)
	}

	// A transfer leg.
	transfer, err := ledgerAt(f.db, today).PostTransferBetweenAccounts(ctx, ledger.PostTransferBetweenAccountsParams{
		FundID: f.fund.ID, PurposeID: f.mainID, FromAccountID: f.cashID, ToAccountID: bank.ID,
		Amount: 5_000, OccurredOn: "2026-07-05",
	})
	if err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v", err)
	}
	var leg int64
	rows, _ = store.New(f.db).ListTransactionsByFund(ctx, f.fund.ID)
	for _, row := range rows {
		if row.TransferID != nil && *row.TransferID == transfer.ID {
			leg = row.ID
		}
	}

	// A closed envelope's contribution.
	envelope, err := f.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{FundID: f.fund.ID, Occasion: "Duka", OpenedOn: "2026-07-01"})
	if err != nil {
		t.Fatal(err)
	}
	inEnvelope := post("in", 20_000, "2026-07-06", envelope.PurposeID)
	if _, err := f.l.CloseIncidentalAndRoll(ctx, ledger.CloseIncidentalAndRollParams{
		FundID: f.fund.ID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-07-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v", err)
	}

	// A reversed row, for reverse-then-correct.
	reversed := post("out", 3_000, "2026-07-07", f.mainID)
	if _, err := ledgerAt(f.db, today).ReverseTransaction(ctx, ledger.ReverseTransactionParams{FundID: f.fund.ID, TransactionID: reversed}); err != nil {
		t.Fatalf("ReverseTransaction() = %v", err)
	}

	// A cek kas that adds the entry explaining a gap, then a row counted by a later
	// count and dated after today.
	cashNow, err := f.l.AccountBalance(ctx, f.fund.ID, f.cashID)
	if err != nil {
		t.Fatal(err)
	}
	bankNow, err := f.l.AccountBalance(ctx, f.fund.ID, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	count, err := ledgerAt(f.db, today).TakeReconciliation(ctx, ledger.TakeReconciliationParams{FundID: f.fund.ID, Counts: []ledger.AccountCount{
		{AccountID: f.cashID, ActualAmount: cashNow - 1_000, Resolution: "entry_added",
			Fix: &ledger.Fix{PurposeID: f.mainID, Direction: "out", Amount: 1_000, OccurredOn: "2026-10-02"}},
		{AccountID: bank.ID, ActualAmount: bankNow, Resolution: "matched"},
	}})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v", err)
	}
	detail, err := f.l.GetReconciliationDetail(ctx, f.fund.ID, count.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fix int64
	for _, line := range detail.Lines {
		if line.AdjustmentTransactionID != nil {
			fix = *line.AdjustmentTransactionID
		}
	}
	aheadRow := post("in", 4_000, "2026-12-20", f.mainID)
	cashNow, _ = f.l.AccountBalance(ctx, f.fund.ID, f.cashID)
	bankNow, _ = f.l.AccountBalance(ctx, f.fund.ID, bank.ID)
	if _, err := ledgerAt(f.db, today).TakeReconciliation(ctx, ledger.TakeReconciliationParams{FundID: f.fund.ID, Counts: []ledger.AccountCount{
		{AccountID: f.cashID, ActualAmount: cashNow, Resolution: "matched"},
		{AccountID: bank.ID, ActualAmount: bankNow, Resolution: "matched"},
	}}); err != nil {
		t.Fatalf("TakeReconciliation(second) = %v", err)
	}

	for _, tc := range []struct {
		name   string
		id     int64
		status int
		code   string
	}{
		{"an opening balance", opening, http.StatusConflict, "reversal_opening"},
		{"a reimbursement payout", payout.ID, http.StatusConflict, "reversal_reimbursement"},
		{"a transfer leg", leg, http.StatusConflict, "reversal_transfer"},
		{"the fix a cek kas line names", fix, http.StatusConflict, "reversal_reconciliation_fix"},
		{"a counted row dated after today", aheadRow, http.StatusConflict, "reversal_before_original"},
		{"a row in a closed envelope", inEnvelope, http.StatusConflict, "incidental_closed"},
	} {
		rec := postReversal(t, r, tc.id, reverseTransactionRequest{})
		if rec.Code != tc.status {
			t.Errorf("%s: POST reversal = %d, want %d (body: %s)", tc.name, rec.Code, tc.status, rec.Body.String())
			continue
		}
		if got := decodeError(t, rec); got.Code != tc.code {
			t.Errorf("%s: error code = %q, want %q", tc.name, got.Code, tc.code)
		}
	}

	// Reverse, then correct: the purpose-correction route's own code.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/transactions/%d/purpose-correction", reversed),
		strings.NewReader(fmt.Sprintf(`{"purpose_id":%d}`, envelope.PurposeID))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST purpose-correction of a reversed row = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "purpose_correction_already_reversed" {
		t.Errorf("error code = %q, want purpose_correction_already_reversed", got.Code)
	}
}

// The pair reads as linked on the public report and in the PDF (ADR-042): the
// original carries Sudah dibatalkan, the reversal of an unnamed row states the
// original's date, and neither is a dues or contribution label. The date is the
// original's (25 Juli), not the day the reversal was posted.
func TestTheReportAndPDFReadAReversedPairAsLinked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-07-01")
	out, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 12_000, OccurredOn: "2026-07-25",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v", err)
	}
	if _, err := ledgerAt(f.db, time.Date(2026, 7, 28, 3, 0, 0, 0, time.UTC)).ReverseTransaction(ctx, ledger.ReverseTransactionParams{
		FundID: f.fund.ID, TransactionID: out.ID,
	}); err != nil {
		t.Fatalf("ReverseTransaction() = %v", err)
	}

	label := "Pembatalan \u00b7 transaksi 25 Juli 2026"
	page := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-07")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /report = %d, want 200", page.Code)
	}
	section := txnSection(t, page.Body.String())
	if n := strings.Count(section, `<span class="label-line">`+label+`</span>`); n != 1 {
		t.Errorf("reversal labels on the page = %d, want 1:\n%s", n, section)
	}
	if n := strings.Count(section, ">Sudah dibatalkan<"); n != 1 {
		t.Errorf("Sudah dibatalkan marks on the page = %d, want 1 (the original only):\n%s", n, section)
	}

	pdf := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-07")
	if pdf.Code != http.StatusOK {
		t.Fatalf("GET /report/pdf = %d, want 200", pdf.Code)
	}
	runs := pdfText(t, pdf.Body.Bytes())
	var haveLabel, marks int
	for _, run := range runs {
		if strings.Contains(run, "Pembatalan") && strings.Contains(run, "transaksi 25 Juli 2026") {
			haveLabel++
		}
		if run == "Sudah dibatalkan" {
			marks++
		}
	}
	if haveLabel != 1 || marks != 1 {
		t.Errorf("PDF reversal labels = %d, marks = %d, want 1 and 1; runs:\n%s", haveLabel, marks, strings.Join(runs, "\n"))
	}
}
