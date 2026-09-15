package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

func getDuesPayments(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-payments"+query, nil))
	return rec
}

func decodeDuesPaymentsPage(t *testing.T, rec *httptest.ResponseRecorder) duesPaymentsPageResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var page duesPaymentsPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decoding dues payments page: %v", err)
	}
	return page
}

func TestGetDuesPaymentsRequiresAFund(t *testing.T) {
	rec := getDuesPayments(t, testRouter(t), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/dues-payments before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetDuesPaymentsEmptyFundReturnsEmptyPage is the "empty" acceptance
// criterion: a fund with no dues payments at all answers an empty list, not
// an error.
func TestGetDuesPaymentsEmptyFundReturnsEmptyPage(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, ""))
	if len(page.DuesPayments) != 0 {
		t.Errorf("GET /api/dues-payments on an empty fund = %d rows, want 0", len(page.DuesPayments))
	}
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v, want nil", *page.NextCursor)
	}
}

// TestGetDuesPaymentsOrdersNewestFirstTiesBrokenByIDDesc mirrors
// reimbursements_test.go's own test of the same name: two rows sharing an
// occurred_on must come back (occurred_on DESC, id DESC).
func TestGetDuesPaymentsOrdersNewestFirstTiesBrokenByIDDesc(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	older := postOneDuesPayment(t, r, setup, memberID, periodAt(0), "2026-08-05")
	sameDate := postOneDuesPayment(t, r, setup, memberID, periodAt(1), "2026-08-05")
	newest := postOneDuesPayment(t, r, setup, memberID, periodAt(2), "2026-08-06")

	page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, ""))
	wantOrder := []int64{newest, sameDate, older}
	if len(page.DuesPayments) != len(wantOrder) {
		t.Fatalf("GET /api/dues-payments returned %d rows, want %d", len(page.DuesPayments), len(wantOrder))
	}
	for i, want := range wantOrder {
		if page.DuesPayments[i].ID != want {
			t.Errorf("row %d = id %d, want %d (order %v)", i, page.DuesPayments[i].ID, want, wantOrder)
		}
	}
}

// TestGetDuesPaymentsPagingWalksTheWholeSetWithNoSkipOrDuplicate mirrors
// reimbursements_test.go's own paging test, over 60 dues payments (more
// than two 25-row pages). Each row gets its own dues_period (periodAt) so
// 60 distinct payments for one member need no second member or account to
// stay realistic against the schema.
func TestGetDuesPaymentsPagingWalksTheWholeSetWithNoSkipOrDuplicate(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	start, err := time.Parse("2006-01-02", "2026-01-01")
	if err != nil {
		t.Fatalf("parsing start date: %v", err)
	}
	seeded := make([]int64, 0, 60)
	for i := 0; i < 60; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		seeded = append(seeded, postOneDuesPayment(t, r, setup, memberID, periodAt(i), date))
	}

	seen := map[int64]int{}
	var order []int64
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("more than 10 pages fetched - paging did not terminate")
		}
		rawQuery := ""
		if cursor != "" {
			rawQuery = "?cursor=" + url.QueryEscape(cursor)
		}
		page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, rawQuery))
		if pages < 2 && len(page.DuesPayments) != duesPaymentsPageSize {
			t.Errorf("page %d = %d rows, want %d (60 rows over 25-row pages)", pages, len(page.DuesPayments), duesPaymentsPageSize)
		}
		for _, row := range page.DuesPayments {
			seen[row.ID]++
			order = append(order, row.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}

	if len(seen) != len(seeded) {
		t.Fatalf("paged through %d distinct ids, want %d", len(seen), len(seeded))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("id %d appeared %d times across pages, want exactly 1", id, count)
		}
	}
	for i, gotID := range order {
		want := seeded[len(seeded)-1-i] // seeded is oldest-first; the walk must be newest-first
		if gotID != want {
			t.Fatalf("row %d across the whole paged walk = id %d, want %d (newest-first order broken across a page boundary)", i, gotID, want)
		}
	}
}

// TestGetDuesPaymentsRejectsAMalformedCursor is #228's "malformed cursor ->
// 400" acceptance criterion, mirroring reimbursements_test.go's own test.
func TestGetDuesPaymentsRejectsAMalformedCursor(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	for _, cursor := range []string{"not-base64!!", "2026-13-40|1"} {
		rec := getDuesPayments(t, r, "?cursor="+url.QueryEscape(cursor))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/dues-payments?cursor=%q = %d, want %d (body: %s)", cursor, rec.Code, http.StatusBadRequest, rec.Body.String())
			continue
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("cursor %q error code = %q, want %q", cursor, got.Code, "invalid_argument")
		}
	}

	// An empty cursor param is the same as omitting it - the first page.
	if rec := getDuesPayments(t, r, "?cursor="); rec.Code != http.StatusOK {
		t.Errorf("GET /api/dues-payments?cursor= = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestGetDuesPaymentsSearchHitsMemberNameCaseInsensitiveIncludingAReversal
// covers this list's search surface: member name, case-insensitive, and a
// reversal for that member matches too since it carries the same
// member_id.
func TestGetDuesPaymentsSearchHitsMemberNameCaseInsensitiveIncludingAReversal(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Budi Santoso")
	otherMemberID := memberFor(t, r, "Siti")

	paymentID := postOneDuesPayment(t, r, setup, memberID, "2026-08", "2026-08-01")
	postOneDuesPayment(t, r, setup, otherMemberID, "2026-08", "2026-08-02")

	reversalRec := postDuesPaymentReversal(t, r, paymentID, reverseDuesPaymentRequest{OccurredOn: "2026-08-10"})
	if reversalRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal = %d, want %d (body: %s)", reversalRec.Code, http.StatusCreated, reversalRec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(reversalRec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}

	page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, "?q=budi"))
	if len(page.DuesPayments) != 2 {
		t.Fatalf("q=budi returned %d rows, want 2 (the payment and its reversal): %+v", len(page.DuesPayments), page.DuesPayments)
	}
	ids := map[int64]bool{}
	for _, row := range page.DuesPayments {
		ids[row.ID] = true
		if row.MemberID != memberID {
			t.Errorf("row %d member_id = %d, want %d", row.ID, row.MemberID, memberID)
		}
	}
	if !ids[paymentID] || !ids[reversal.ID] {
		t.Errorf("q=budi rows = %v, want both %d and %d", ids, paymentID, reversal.ID)
	}
}

// TestGetDuesPaymentsReversalAndOriginalAreLinkedBothWays is the slice's
// central acceptance criterion: a reversed payment carries
// reversed_by_transaction_id pointing at the reversal, and the reversal
// carries reverses_transaction_id pointing back plus the original's own
// occurred_on.
func TestGetDuesPaymentsReversalAndOriginalAreLinkedBothWays(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	paymentID := postOneDuesPayment(t, r, setup, memberID, "2026-08", "2026-08-01")

	reversalRec := postDuesPaymentReversal(t, r, paymentID, reverseDuesPaymentRequest{OccurredOn: "2026-08-10"})
	if reversalRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal = %d, want %d (body: %s)", reversalRec.Code, http.StatusCreated, reversalRec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(reversalRec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}

	page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, ""))
	if len(page.DuesPayments) != 2 {
		t.Fatalf("GET /api/dues-payments returned %d rows, want 2", len(page.DuesPayments))
	}

	var paymentRow, reversalRow *duesPaymentHistoryResponse
	for i := range page.DuesPayments {
		row := &page.DuesPayments[i]
		switch row.ID {
		case paymentID:
			paymentRow = row
		case reversal.ID:
			reversalRow = row
		}
	}
	if paymentRow == nil || reversalRow == nil {
		t.Fatalf("did not find both rows among %+v", page.DuesPayments)
	}

	if paymentRow.IsReversal {
		t.Error("the original payment's is_reversal = true, want false")
	}
	if paymentRow.ReversedByTransactionID == nil || *paymentRow.ReversedByTransactionID != reversal.ID {
		t.Errorf("payment reversed_by_transaction_id = %v, want %d", paymentRow.ReversedByTransactionID, reversal.ID)
	}
	if paymentRow.ReversesTransactionID != nil {
		t.Errorf("payment reverses_transaction_id = %v, want nil", *paymentRow.ReversesTransactionID)
	}

	if !reversalRow.IsReversal {
		t.Error("the reversal's is_reversal = false, want true")
	}
	if reversalRow.ReversesTransactionID == nil || *reversalRow.ReversesTransactionID != paymentID {
		t.Errorf("reversal reverses_transaction_id = %v, want %d", reversalRow.ReversesTransactionID, paymentID)
	}
	if reversalRow.ReversedByTransactionID != nil {
		t.Errorf("reversal reversed_by_transaction_id = %v, want nil", *reversalRow.ReversedByTransactionID)
	}
	if reversalRow.ReversesOccurredOn == nil || *reversalRow.ReversesOccurredOn != "2026-08-01" {
		t.Errorf("reversal reverses_occurred_on = %v, want %q (the original payment's date)", reversalRow.ReversesOccurredOn, "2026-08-01")
	}
}

// TestGetDuesPaymentsReversalsOriginalOnAnotherPageStillCarriesReversesOccurredOn
// is #228's own acceptance criterion for the case a naive join would get
// wrong: the reversal states the original's date even when that original
// has fallen off the first page.
func TestGetDuesPaymentsReversalsOriginalOnAnotherPageStillCarriesReversesOccurredOn(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	// The payment to reverse, dated first (oldest) so it is guaranteed off
	// page 1 once enough newer rows exist.
	paymentID := postOneDuesPayment(t, r, setup, memberID, "2020-01", "2020-01-01")

	// 25 more payments, each newer than the last (2026-01-01 onward), so
	// page 1 (25 rows) is entirely these and the original payment lands on
	// page 2.
	start, err := time.Parse("2006-01-02", "2026-01-01")
	if err != nil {
		t.Fatalf("parsing start date: %v", err)
	}
	for i := 0; i < 25; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		postOneDuesPayment(t, r, setup, memberID, periodAt(i+1), date)
	}

	// The reversal, dated newest of all, so it is the very first row.
	reversalRec := postDuesPaymentReversal(t, r, paymentID, reverseDuesPaymentRequest{OccurredOn: "2026-06-01"})
	if reversalRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal = %d, want %d (body: %s)", reversalRec.Code, http.StatusCreated, reversalRec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(reversalRec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}

	page1 := decodeDuesPaymentsPage(t, getDuesPayments(t, r, ""))
	if len(page1.DuesPayments) != duesPaymentsPageSize {
		t.Fatalf("page 1 = %d rows, want %d", len(page1.DuesPayments), duesPaymentsPageSize)
	}
	if page1.DuesPayments[0].ID != reversal.ID {
		t.Fatalf("page 1 row 0 = id %d, want the reversal %d (newest)", page1.DuesPayments[0].ID, reversal.ID)
	}
	for _, row := range page1.DuesPayments {
		if row.ID == paymentID {
			t.Fatalf("the original payment (id %d) is on page 1 - this test's fixture must push it to page 2", paymentID)
		}
	}
	reversalRow := page1.DuesPayments[0]
	if reversalRow.ReversesOccurredOn == nil || *reversalRow.ReversesOccurredOn != "2020-01-01" {
		t.Errorf("reversal reverses_occurred_on = %v, want %q - the original's date, even though it is on page 2", reversalRow.ReversesOccurredOn, "2020-01-01")
	}

	if page1.NextCursor == nil {
		t.Fatal("page 1 next_cursor = nil, want a cursor")
	}
	page2 := decodeDuesPaymentsPage(t, getDuesPayments(t, r, "?cursor="+url.QueryEscape(*page1.NextCursor)))
	found := false
	for _, row := range page2.DuesPayments {
		if row.ID == paymentID {
			found = true
		}
	}
	if !found {
		t.Errorf("the original payment (id %d) is missing from page 2", paymentID)
	}
}

// TestGetDuesPaymentsExcludesOrdinaryTransactionsAndAdjustments is #228's
// own scoping rule: a plain kind='normal' transaction and an ordinary
// correction (kind='adjustment' with no reverses_transaction_id) are not
// dues history.
func TestGetDuesPaymentsExcludesOrdinaryTransactionsAndAdjustments(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	paymentID := postOneDuesPayment(t, r, setup, memberID, "2026-08", "2026-08-01")

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions (normal) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 10_000, OccurredOn: "2026-08-03", IsAdjustment: true,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions (adjustment) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, ""))
	if len(page.DuesPayments) != 1 {
		t.Fatalf("GET /api/dues-payments returned %d rows, want 1 (only the dues payment)", len(page.DuesPayments))
	}
	if page.DuesPayments[0].ID != paymentID {
		t.Errorf("row id = %d, want %d", page.DuesPayments[0].ID, paymentID)
	}
}

// TestGetDuesPaymentsNeverReturnsAnotherFundsRow is #228's fund-isolation
// acceptance criterion, mirroring TestGetTransactionsNeverReturnsAnotherFundsRow.
func TestGetDuesPaymentsNeverReturnsAnotherFundsRow(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	postOneDuesPayment(t, r, setup, memberID, "2026-08", "2026-08-01")

	// A second fund, written straight through the store - the API refuses a
	// second fund by design (ErrFundAlreadyExists).
	q := store.New(sqlDB)
	ctx := context.Background()
	otherFund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund(other) = %v, want no error", err)
	}
	otherAccount, err := q.CreateAccount(ctx, store.CreateAccountParams{
		FundID: otherFund.ID, Kind: "cash", Name: "Other Cash", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateAccount(other) = %v, want no error", err)
	}
	otherPurpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: otherFund.ID, Kind: "main", Name: "Other Kas Utama", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreatePurpose(other) = %v, want no error", err)
	}
	otherMember, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: otherFund.ID, Name: "Other Member", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(other) = %v, want no error", err)
	}
	otherPeriod := "2026-08"
	otherRow, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: otherFund.ID, AccountID: otherAccount.ID, PurposeID: otherPurpose.ID,
		Direction: "in", Amount: 999_000, OccurredOn: "2026-08-01", Kind: "dues",
		MemberID: &otherMember.ID, DuesPeriod: &otherPeriod, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(other) = %v, want no error", err)
	}

	page := decodeDuesPaymentsPage(t, getDuesPayments(t, r, ""))
	for _, row := range page.DuesPayments {
		if row.ID == otherRow.ID {
			t.Errorf("GET /api/dues-payments on our fund returned a row belonging to another fund: %+v", row)
		}
	}
}

// postOneDuesPayment posts a single-period dues payment and returns the
// posted transaction id, for the tests above that only need one row and
// its id.
func postOneDuesPayment(t *testing.T, r http.Handler, setup setupResponse, memberID int64, period, occurredOn string) int64 {
	t.Helper()
	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: memberID, OccurredOn: occurredOn,
		Periods: []duesPaymentPeriod{{DuesPeriod: period, Amount: 25_000}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted []transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}
	return posted[0].ID
}

// periodAt returns a distinct "YYYY-MM" dues_period for index i, walking
// forward one calendar month at a time from 2020-01 - enough distinct
// months to cover this file's largest fixture (60 rows) without repeating,
// which keeps every seeded payment addressable by (member, period) alone.
func periodAt(i int) string {
	t := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}
