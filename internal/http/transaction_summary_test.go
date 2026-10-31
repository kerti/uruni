package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
)

func getTransactionSummary(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/transactions/summary"+query, nil))
	return rec
}

func decodeTransactionSummary(t *testing.T, rec *httptest.ResponseRecorder) transactionSummaryResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions/summary = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got transactionSummaryResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding summary: %v", err)
	}
	return got
}

// TestTransactionSummaryIsTheReportsWalk: Riwayat's month summary (#751) is
// the public report's walk for the same month and filters - the whole fund,
// one pos, and a member, whose summary has In and Out only.
func TestTransactionSummaryIsTheReportsWalk(t *testing.T) {
	t.Parallel()
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	cash, main := setup.CashAccountID(t), setup.MainPurposeID
	giver := createMemberFor(t, r, "Giver")
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	for _, tx := range []transactionRequest{
		{AccountID: cash, PurposeID: main, Direction: "in", Amount: 100_000, OccurredOn: "2026-07-10"},
		{AccountID: cash, PurposeID: main, Direction: "in", Amount: 50_000, OccurredOn: "2026-08-05"},
		{AccountID: cash, PurposeID: main, Direction: "out", Amount: 20_000, OccurredOn: "2026-08-06"},
		{AccountID: cash, PurposeID: envelope.PurposeID, Direction: "in", Amount: 30_000, OccurredOn: "2026-08-07", MemberID: &giver.ID},
		{AccountID: cash, PurposeID: main, Direction: "in", Amount: 999, OccurredOn: "2026-09-02"},
	} {
		if rec := postTransaction(t, r, tx); rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
	}

	cases := []struct {
		name   string
		query  string
		params ledger.ReportParams
		want   transactionSummaryResponse
	}{
		{
			name:   "whole fund",
			query:  "?month=2026-08",
			params: ledger.ReportParams{Month: "2026-08"},
			want:   transactionSummaryResponse{Full: true, Start: 100_000, In: 80_000, Out: 20_000, End: 160_000},
		},
		{
			name:   "one pos",
			query:  "?month=2026-08&purpose_id=" + itoa(main),
			params: ledger.ReportParams{Month: "2026-08", PurposeID: &main},
			want:   transactionSummaryResponse{Full: true, Start: 100_000, In: 50_000, Out: 20_000, End: 130_000},
		},
		{
			name:   "a member: in and out only",
			query:  "?month=2026-08&member_id=" + itoa(giver.ID),
			params: ledger.ReportParams{Month: "2026-08", MemberID: &giver.ID},
			want:   transactionSummaryResponse{In: 30_000},
		},
		{
			name:   "a direction: in and out only",
			query:  "?month=2026-08&direction=out",
			params: ledger.ReportParams{Month: "2026-08", Direction: ledger.DirectionOut},
			want:   transactionSummaryResponse{Out: 20_000},
		},
	}
	for _, c := range cases {
		got := decodeTransactionSummary(t, getTransactionSummary(t, r, c.query))
		gotDates := got.StartOn != nil || got.EndOn != nil
		got.StartOn, got.EndOn = nil, nil
		if got != c.want {
			t.Errorf("%s: summary = %+v, want %+v", c.name, got, c.want)
		}
		if gotDates != c.want.Full {
			t.Errorf("%s: dates present = %v, want %v", c.name, gotDates, c.want.Full)
		}

		// The same figures the public report prints for that month and filter.
		c.params.FundID = setup.Fund.ID
		report, err := l.MonthlyReport(context.Background(), c.params)
		if err != nil {
			t.Fatalf("%s: MonthlyReport() = %v", c.name, err)
		}
		w := report.Walk
		fromReport := transactionSummaryResponse{
			Full: w.Full, Start: w.Start.Int64(), Openings: w.Openings.Int64(), In: w.In.Int64(), Out: w.Out.Int64(),
			Adjustments: w.Adjustments.Int64(), Moved: w.Moved.Int64(), End: w.End.Int64(),
		}
		if got != fromReport {
			t.Errorf("%s: summary = %+v, report's walk = %+v", c.name, got, fromReport)
		}
	}
}

// TestTransactionSummaryNeedsAMonth: with no month there is no walk to read;
// a malformed filter is the list's own 400.
func TestTransactionSummaryNeedsAMonth(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	for _, query := range []string{"", "?month=2026-13", "?month=2026-08&direction=sideways", "?month=2026-08&purpose_id=0"} {
		if rec := getTransactionSummary(t, r, query); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/transactions/summary%s = %d, want %d", query, rec.Code, http.StatusBadRequest)
		}
	}
}
