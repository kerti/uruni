package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func getReportMonths(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/report/months", nil))
	return rec
}

func decodeReportMonths(t *testing.T, rec *httptest.ResponseRecorder) reportMonthsResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/report/months = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got reportMonthsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// The PDF picker's range is the report selector's: the first transaction's
// month through the current one in the fund's zone, empty months between
// included.
func TestGetReportMonthsSpansFirstTransactionToTheCurrentMonth(t *testing.T) {
	t.Parallel()
	r := authedRouterAt(t, testStoreDB(t), fixedClock(time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)))
	setup := setUpFund(t, r)

	if got := decodeReportMonths(t, getReportMonths(t, r)); got != (reportMonthsResponse{Min: "2026-10", Max: "2026-10"}) {
		t.Errorf("empty fund: months = %+v, want the current month alone", got)
	}

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-06-20",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeReportMonths(t, getReportMonths(t, r)); got != (reportMonthsResponse{Min: "2026-06", Max: "2026-10"}) {
		t.Errorf("months = %+v, want 2026-06 through 2026-10", got)
	}
}

// The current month turns on the fund's zone, not UTC's (ADR-041).
func TestGetReportMonthsReadsTheFundsMonth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"one second before September in Jakarta", beforeSeptemberInJakarta, "2026-08"},
		{"the first second of September in Jakarta", atSeptemberInJakarta, "2026-09"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			setUpFund(t, r)
			if got := decodeReportMonths(t, getReportMonths(t, r)); got.Max != tc.want {
				t.Errorf("max = %q, want %q", got.Max, tc.want)
			}
		})
	}
}

func TestGetReportMonthsBeforeSetupIs404(t *testing.T) {
	t.Parallel()
	r := authedRouterFor(t, testStoreDB(t))
	if rec := getReportMonths(t, r); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/report/months before setup = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
