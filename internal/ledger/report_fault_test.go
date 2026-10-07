package ledger

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// faultQuerier wraps a real Querier and breaks one of MonthlyReport's reads:
// fail names the method that returns errFault, and rows, firstDate and
// openLines, when set, replace a read's result so a test can hand the report
// a ledger shape the ledger's own writes never produce. Every other call goes through.
type faultQuerier struct {
	store.Querier
	fail      string
	firstDate *string
	// failPurpose, when set, breaks ReportPurposeBalance for that one purpose only,
	// so a test can reach the envelope section's own balance read past the
	// header's.
	failPurpose int64
	rows        []store.ListReportTransactionsRow
	// openLines, when set, replaces ReportOpenReconciliationLines's result.
	openLines []store.ReconciliationLine
}

var errFault = errors.New("injected fault")

func (f faultQuerier) GetFund(ctx context.Context, id int64) (store.Fund, error) {
	if f.fail == "GetFund" {
		return store.Fund{}, errFault
	}
	return f.Querier.GetFund(ctx, id)
}

func (f faultQuerier) FirstTransactionDateByFund(ctx context.Context, fundID int64) (string, error) {
	if f.fail == "FirstTransactionDateByFund" {
		return "", errFault
	}
	if f.firstDate != nil {
		return *f.firstDate, nil
	}
	return f.Querier.FirstTransactionDateByFund(ctx, fundID)
}

func (f faultQuerier) ReportLatestReconciliation(ctx context.Context, arg store.ReportLatestReconciliationParams) (store.Reconciliation, error) {
	if f.fail == "ReportLatestReconciliation" {
		return store.Reconciliation{}, errFault
	}
	return f.Querier.ReportLatestReconciliation(ctx, arg)
}

func (f faultQuerier) ReportOpenReconciliationLines(ctx context.Context, arg store.ReportOpenReconciliationLinesParams) ([]store.ReconciliationLine, error) {
	if f.fail == "ReportOpenReconciliationLines" {
		return nil, errFault
	}
	if f.openLines != nil {
		return f.openLines, nil
	}
	return f.Querier.ReportOpenReconciliationLines(ctx, arg)
}

func (f faultQuerier) ListIncidentalsByFund(ctx context.Context, fundID int64) ([]store.Incidental, error) {
	if f.fail == "ListIncidentalsByFund" {
		return nil, errFault
	}
	return f.Querier.ListIncidentalsByFund(ctx, fundID)
}

func (f faultQuerier) ListPurposesByFund(ctx context.Context, fundID int64) ([]store.Purpose, error) {
	if f.fail == "ListPurposesByFund" {
		return nil, errFault
	}
	return f.Querier.ListPurposesByFund(ctx, fundID)
}

func (f faultQuerier) ListReportTransactions(ctx context.Context, arg store.ListReportTransactionsParams) ([]store.ListReportTransactionsRow, error) {
	if f.fail == "ListReportTransactions" {
		return nil, errFault
	}
	if f.rows != nil {
		return f.rows, nil
	}
	return f.Querier.ListReportTransactions(ctx, arg)
}

func (f faultQuerier) ListDuesTiersByFund(ctx context.Context, fundID int64) ([]store.DuesTier, error) {
	if f.fail == "ListDuesTiersByFund" {
		return nil, errFault
	}
	return f.Querier.ListDuesTiersByFund(ctx, fundID)
}

func (f faultQuerier) ListIncidentalRecipients(ctx context.Context, purposeID int64) ([]store.ListIncidentalRecipientsRow, error) {
	if f.fail == "ListIncidentalRecipients" {
		return nil, errFault
	}
	return f.Querier.ListIncidentalRecipients(ctx, purposeID)
}

func (f faultQuerier) ReportFundBalance(ctx context.Context, arg store.ReportFundBalanceParams) (int64, error) {
	if f.fail == "ReportFundBalance" {
		return 0, errFault
	}
	return f.Querier.ReportFundBalance(ctx, arg)
}

func (f faultQuerier) ReportPurposeBalance(ctx context.Context, arg store.ReportPurposeBalanceParams) (int64, error) {
	if f.fail == "ReportPurposeBalance" || (f.failPurpose != 0 && arg.PurposeID == f.failPurpose) {
		return 0, errFault
	}
	return f.Querier.ReportPurposeBalance(ctx, arg)
}

func (f faultQuerier) ReportOwedToMembers(ctx context.Context, arg store.ReportOwedToMembersParams) (int64, error) {
	if f.fail == "ReportOwedToMembers" {
		return 0, errFault
	}
	return f.Querier.ReportOwedToMembers(ctx, arg)
}

func (f faultQuerier) IncidentalActivityTotals(ctx context.Context, arg store.IncidentalActivityTotalsParams) (store.IncidentalActivityTotalsRow, error) {
	if f.fail == "IncidentalActivityTotals" {
		return store.IncidentalActivityTotalsRow{}, errFault
	}
	return f.Querier.IncidentalActivityTotals(ctx, arg)
}

func (f faultQuerier) ContributedByIncidentalMember(ctx context.Context, arg store.ContributedByIncidentalMemberParams) ([]store.ContributedByIncidentalMemberRow, error) {
	if f.fail == "ContributedByIncidentalMember" {
		return nil, errFault
	}
	return f.Querier.ContributedByIncidentalMember(ctx, arg)
}

func (f faultQuerier) GetIncidental(ctx context.Context, arg store.GetIncidentalParams) (store.Incidental, error) {
	if f.fail == "GetIncidental" {
		return store.Incidental{}, errFault
	}
	return f.Querier.GetIncidental(ctx, arg)
}

func (f faultQuerier) ListMembersByFund(ctx context.Context, fundID int64) ([]store.Member, error) {
	if f.fail == "ListMembersByFund" {
		return nil, errFault
	}
	return f.Querier.ListMembersByFund(ctx, fundID)
}

// faultyReportFixture is a fund with every surface MonthlyReport reads - a
// reconciliation, an open envelope, a dues tier, a September row - so each
// injected fault is reached rather than skipped by an empty result.
func faultyReportFixture(t *testing.T) (*Ledger, int64) {
	l, fundID, _ := faultyReportFixtureWithClosed(t)
	return l, fundID
}

// faultyReportFixtureWithClosed adds an envelope closed in September: absent
// from the header's balances, present in the envelope section. Its purpose id
// is returned so a test can break that one balance read.
func faultyReportFixtureWithClosed(t *testing.T) (*Ledger, int64, int64) {
	t.Helper()
	l := newTestLedger(t)
	s := newMonthScenario(t, l)
	openTestIncidental(t, l, s.f.fundID, "Open Collection", "2026-09-01")
	// Counted inside September, so the September report reads it (ADR-037).
	at := jakartaAt(2026, time.September, 25, 12, 0, 0).Unix()
	if _, err := store.New(l.db).CreateReconciliation(context.Background(), store.CreateReconciliationParams{
		FundID: s.f.fundID, PerformedAt: at, CreatedAt: at,
	}); err != nil {
		t.Fatalf("CreateReconciliation() = %v, want no error", err)
	}
	closed := openTestIncidental(t, l, s.f.fundID, "Closed Collection", "2026-09-02")
	if _, err := l.CloseIncidentalAndRoll(context.Background(), CloseIncidentalAndRollParams{
		FundID: s.f.fundID, PurposeID: closed.PurposeID, AccountID: s.f.cashID, ClosedOn: "2026-09-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	return l, s.f.fundID, closed.PurposeID
}

func TestMonthlyReportSurfacesEveryReadFailure(t *testing.T) {
	t.Parallel()
	for _, method := range []string{
		"GetFund",
		"FirstTransactionDateByFund",
		"ReportLatestReconciliation",
		"ReportOpenReconciliationLines",
		"ListIncidentalsByFund",
		"ListPurposesByFund",
		"ListReportTransactions",
		"ListDuesTiersByFund",
		"ListIncidentalRecipients",
		"ReportFundBalance",
		"ReportPurposeBalance",
		"ReportOwedToMembers",
		"IncidentalActivityTotals",
		"ContributedByIncidentalMember",
		"GetIncidental",
		"ListMembersByFund",
	} {
		t.Run(method, func(t *testing.T) {
			l, fundID := faultyReportFixture(t)
			broken := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, fail: method}}

			_, err := broken.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
			if !errors.Is(err, errFault) {
				t.Fatalf("monthlyReport() with %s failing = %v, want the injected fault, not a partial report", method, err)
			}
		})
	}
}

func TestMonthlyReportRefusesALedgerShapeItCannotHaveWritten(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	id := func(n int64) *int64 { return &n }
	transferRow := func(kind *string, transferID *int64) store.ListReportTransactionsRow {
		return store.ListReportTransactionsRow{
			OccurredOn: "2026-09-10", Direction: "out", Amount: 1_000, Kind: "transfer",
			PurposeName: "Main", TransferKind: kind, TransferID: transferID,
		}
	}

	tests := []struct {
		name string
		row  store.ListReportTransactionsRow
		want string
	}{
		{"a transfer leg with no transfer", transferRow(nil, nil), "carries no transfer"},
		{"an unknown transfer kind", transferRow(str("sideways"), id(1)), "unknown transfer kind"},
		{"a purpose move missing a leg", transferRow(str("reclass_purpose"), id(1)), "missing a leg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, fundID := faultyReportFixture(t)
			odd := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, rows: []store.ListReportTransactionsRow{tt.row}}}

			_, err := odd.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("monthlyReport() = %v, want an error mentioning %q - a trust report says the ledger is broken rather than dropping a row", err, tt.want)
			}
		})
	}
}

// Two corrections of one row both leaving the same purpose cannot be a path:
// the ledger never writes that, and the report says so rather than guess a
// net move (#280).
func TestMonthlyReportRefusesCorrectionsThatAreNotAPath(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	id := func(n int64) *int64 { return &n }
	leg := func(transferID, from, to int64) store.ListReportTransactionsRow {
		return store.ListReportTransactionsRow{
			OccurredOn: "2026-09-10", Direction: "in", Amount: 1_000, Kind: "transfer", PurposeName: "Main",
			TransferKind: str("reclass_purpose"), TransferID: id(transferID), TransferCorrectsTransactionID: id(99),
			TransferFromPurposeID: id(from), TransferToPurposeID: id(to),
			TransferFromPurposeName: str("A"), TransferToPurposeName: str("B"),
		}
	}
	l, fundID := faultyReportFixture(t)
	odd := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, rows: []store.ListReportTransactionsRow{leg(1, 1, 2), leg(2, 1, 3)}}}

	_, err := odd.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
	if err == nil || !strings.Contains(err.Error(), "do not form a path") {
		t.Fatalf("monthlyReport() = %v, want an error naming the broken path", err)
	}
}

func TestMonthlyReportRefusesAMalformedFirstTransactionDate(t *testing.T) {
	t.Parallel()
	l, fundID := faultyReportFixture(t)
	bad := "20x6-09-10"
	odd := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, firstDate: &bad}}

	_, err := odd.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
	if err == nil || !strings.Contains(err.Error(), "first transaction date") {
		t.Fatalf("monthlyReport() = %v, want an error naming the first transaction date", err)
	}
}

func TestMonthlyReportSurfacesAClosedEnvelopesBalanceFailure(t *testing.T) {
	t.Parallel()
	l, fundID, closedPurpose := faultyReportFixtureWithClosed(t)
	broken := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, failPurpose: closedPurpose}}

	_, err := broken.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
	if !errors.Is(err, errFault) {
		t.Fatalf("monthlyReport() = %v, want the injected fault from the closed envelope's balance", err)
	}
}

// The ledger's own SUM refuses an overflowing balance first (see
// TestMonthlyReportTotalsRefuseToWrap), so these hand the report rows no real
// fund could hold, to prove its own sums refuse to wrap too.
func TestMonthlyReportOwnSumsRefuseToWrap(t *testing.T) {
	t.Parallel()
	huge := int64(math.MaxInt64)
	row := store.ListReportTransactionsRow{
		OccurredOn: "2026-09-10", Direction: "in", Amount: huge, Kind: "normal", PurposeName: "Main",
	}

	t.Run("totals", func(t *testing.T) {
		l, fundID := faultyReportFixture(t)
		odd := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, rows: []store.ListReportTransactionsRow{row, row}}}
		_, err := odd.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
		if err == nil || !strings.Contains(err.Error(), "totalling") {
			t.Fatalf("monthlyReport() = %v, want a totalling overflow", err)
		}
	})

	t.Run("open differences", func(t *testing.T) {
		l, fundID := faultyReportFixture(t)
		line := store.ReconciliationLine{DifferenceAmount: huge}
		odd := &Ledger{db: l.db, q: faultQuerier{Querier: l.q, openLines: []store.ReconciliationLine{line, line}}}
		_, err := odd.monthlyReport(context.Background(), ReportParams{FundID: fundID, Month: "2026-09", Now: reportNow})
		if err == nil || !strings.Contains(err.Error(), "summing open differences") {
			t.Fatalf("monthlyReport() = %v, want a difference overflow", err)
		}
	})
}
