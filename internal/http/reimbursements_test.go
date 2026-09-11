package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

func postReimbursement(t *testing.T, r http.Handler, req reimbursementRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling reimbursement request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements", bytes.NewReader(body)))
	return rec
}

func getReimbursements(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reimbursements"+query, nil))
	return rec
}

// seedReimbursements creates n unsettled claims for the same member and
// purpose, dated one calendar day apart starting at startDate - the same
// idiom seedTransactions (transactions_test.go) uses for its own paging
// tests, over CreateReimbursement instead of CreateTransaction.
func seedReimbursements(t *testing.T, sqlDB *sql.DB, fundID, memberID, purposeID int64, n int, startDate string) []store.Reimbursement {
	t.Helper()
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		t.Fatalf("parsing start date %q: %v", startDate, err)
	}
	q := store.New(sqlDB)
	ctx := context.Background()
	rows := make([]store.Reimbursement, 0, n)
	for i := 0; i < n; i++ {
		row, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
			FundID: fundID, MemberID: memberID, PurposeID: purposeID,
			Amount: int64(10_000 + i), IncurredOn: start.AddDate(0, 0, i).Format("2006-01-02"),
			CreatedAt: int64(i + 1),
		})
		if err != nil {
			t.Fatalf("seeding reimbursement %d: %v", i, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func postSettlement(t *testing.T, r http.Handler, id int64, req settleReimbursementRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling settle request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/reimbursements/%d/settle", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

// memberFor creates one member and returns its id, for the tests below that
// need a claimant and nothing else about them.
func memberFor(t *testing.T, r http.Handler, name string) int64 {
	t.Helper()
	rec := postMember(t, r, memberRequest{Name: name})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	return member.ID
}

func TestPostReimbursementsRequiresAFund(t *testing.T) {
	rec := postReimbursement(t, testRouter(t), reimbursementRequest{
		MemberID: 1, PurposeID: 1, Amount: 80_000, IncurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/reimbursements before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestReimbursementRoundTripsFromClaimToSettlement is the slice's central
// acceptance criterion: the claim itself moves no money - the fund balance
// is still zero while it is outstanding - and only settling posts the
// single 'out' row that pays it.
func TestReimbursementRoundTripsFromClaimToSettlement(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	note := "bought lightbulbs for the pos ronda"
	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10", Note: &note,
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(createRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	if claim.ID == 0 {
		t.Error("reimbursement id = 0, want a real id")
	}
	if claim.Amount != 80_000 || claim.IncurredOn != "2026-08-10" || claim.MemberID != memberID {
		t.Errorf("claim = %+v, want amount 80000 incurred 2026-08-10 for member %d", claim, memberID)
	}
	if claim.Settled {
		t.Errorf("fresh claim settled = true, want false - a claim is born owed")
	}

	balanceBefore, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if balanceBefore.Int64() != 0 {
		t.Fatalf("FundBalance() after the claim = %d, want 0 - recording a claim moves no money", balanceBefore.Int64())
	}

	settleRec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if settleRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements/{id}/settle = %d, want %d (body: %s)", settleRec.Code, http.StatusCreated, settleRec.Body.String())
	}
	var posted transactionResponse
	if err := json.NewDecoder(settleRec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding settlement response: %v", err)
	}
	if posted.Kind != "reimbursement" {
		t.Errorf("settlement kind = %q, want %q", posted.Kind, "reimbursement")
	}
	if posted.Direction != "out" {
		t.Errorf("settlement direction = %q, want %q", posted.Direction, "out")
	}
	if posted.Amount != 80_000 {
		t.Errorf("settlement amount = %d, want the claim's 80000", posted.Amount)
	}
	if posted.PurposeID != setup.MainPurposeID {
		t.Errorf("settlement purpose = %d, want the claim's %d", posted.PurposeID, setup.MainPurposeID)
	}
	// The settle date, not incurred_on - the claim keeps the truth about when
	// the member actually spent their own money.
	if posted.OccurredOn != "2026-08-20" {
		t.Errorf("settlement occurred_on = %q, want the settle date %q, not incurred_on", posted.OccurredOn, "2026-08-20")
	}
	if posted.ReimbursementID == nil || *posted.ReimbursementID != claim.ID {
		t.Errorf("settlement reimbursement_id = %v, want claim %d", posted.ReimbursementID, claim.ID)
	}

	balanceAfter, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if balanceAfter.Int64() != -80_000 {
		t.Errorf("FundBalance() after settling = %d, want -80000 - the payout is the only posted row", balanceAfter.Int64())
	}
}

// TestPostSettlementTwiceReturnsItsNamed409 covers the settled-once rule at
// the route: the second call is a conflict with its own code, not a second
// payout and not a generic 500.
func TestPostSettlementTwiceReturnsItsNamed409(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10",
	})
	var claim reimbursementResponse
	if err := json.NewDecoder(createRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}

	first := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first settle = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-21",
	})
	if second.Code != http.StatusConflict {
		t.Fatalf("second settle = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "reimbursement_already_settled" {
		t.Errorf("error code = %q, want %q", got.Code, "reimbursement_already_settled")
	}
}

// TestPostSettlementOnAWaivedClaimReturnsItsNamed409 reaches
// ErrReimbursementWaived, which no route can produce on its own: nothing on
// the wire sets waived_on (#69 adds no waive route), so the claim is waived
// through store.Queries directly - the same way an import or a future waive
// route would - and the route is asked to settle it.
func TestPostSettlementOnAWaivedClaimReturnsItsNamed409(t *testing.T) {
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	r := authedRouterFor(t, sqlDB)

	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	waivedOn := "2026-08-15"
	claim, err := q.CreateReimbursement(context.Background(), store.CreateReimbursementParams{
		FundID: setup.Fund.ID, MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10", WaivedOn: &waivedOn,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}

	rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("settling a waived claim = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "reimbursement_waived" {
		t.Errorf("error code = %q, want %q", got.Code, "reimbursement_waived")
	}
}

// TestGetReimbursementsOutstandingFiltersToUnsettledClaims is the filter's
// acceptance criterion: the unfiltered list keeps every claim as history,
// ?outstanding=true keeps only what is still owed.
func TestGetReimbursementsOutstandingFiltersToUnsettledClaims(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	var claims []reimbursementResponse
	for _, amount := range []int64{80_000, 40_000} {
		rec := postReimbursement(t, r, reimbursementRequest{
			MemberID: memberID, PurposeID: setup.MainPurposeID,
			Amount: amount, IncurredOn: "2026-08-10",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
		var claim reimbursementResponse
		if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
			t.Fatalf("decoding reimbursement response: %v", err)
		}
		claims = append(claims, claim)
	}

	settleRec := postSettlement(t, r, claims[0].ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if settleRec.Code != http.StatusCreated {
		t.Fatalf("settle = %d, want %d (body: %s)", settleRec.Code, http.StatusCreated, settleRec.Body.String())
	}

	all := decodeReimbursements(t, getReimbursements(t, r, ""))
	if len(all) != 2 {
		t.Errorf("GET /api/reimbursements returned %d claims, want 2 - a settled claim is still history", len(all))
	}
	// The full list carries the settled fact: the payout posted for claims[0]
	// but not for claims[1], and the wire says so without a second query.
	for _, claim := range all {
		want := claim.ID == claims[0].ID
		if claim.Settled != want {
			t.Errorf("GET /api/reimbursements claim %d settled = %v, want %v", claim.ID, claim.Settled, want)
		}
	}

	outstanding := decodeReimbursements(t, getReimbursements(t, r, "?outstanding=true"))
	if len(outstanding) != 1 {
		t.Fatalf("GET /api/reimbursements?outstanding=true returned %d claims, want 1", len(outstanding))
	}
	if outstanding[0].ID != claims[1].ID {
		t.Errorf("outstanding claim = %d, want the unsettled %d", outstanding[0].ID, claims[1].ID)
	}
	if outstanding[0].Settled {
		t.Errorf("outstanding claim settled = true, want false - the list is unsettled by construction")
	}

	if got := decodeReimbursements(t, getReimbursements(t, r, "?outstanding=false")); len(got) != 2 {
		t.Errorf("GET /api/reimbursements?outstanding=false returned %d claims, want 2 - explicitly asking not to filter", len(got))
	}
}

func TestGetReimbursementsRejectsAnUnparseableOutstandingFilter(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getReimbursements(t, r, "?outstanding=yes")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/reimbursements?outstanding=yes = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestGetReimbursementsOrdersNewestFirstTiesBrokenByIDDesc is #226's
// ordering half of "newest-first, keyset-paged" - the same acceptance
// criterion GET /api/transactions already carries: two rows sharing an
// incurred_on must come back (incurred_on DESC, id DESC), the later of the
// two same-dated rows first.
func TestGetReimbursementsOrdersNewestFirstTiesBrokenByIDDesc(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	q := store.New(sqlDB)
	ctx := context.Background()

	older, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: setup.Fund.ID, MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 10_000, IncurredOn: "2026-08-05", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("seeding older: %v", err)
	}
	sameDate, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: setup.Fund.ID, MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 20_000, IncurredOn: "2026-08-05", CreatedAt: 2,
	})
	if err != nil {
		t.Fatalf("seeding sameDate: %v", err)
	}
	newest, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: setup.Fund.ID, MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 30_000, IncurredOn: "2026-08-06", CreatedAt: 3,
	})
	if err != nil {
		t.Fatalf("seeding newest: %v", err)
	}

	got := decodeReimbursements(t, getReimbursements(t, r, ""))
	wantOrder := []int64{newest.ID, sameDate.ID, older.ID}
	if len(got) != len(wantOrder) {
		t.Fatalf("GET /api/reimbursements returned %d rows, want %d", len(got), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("row %d = id %d, want %d (order %v)", i, got[i].ID, want, wantOrder)
		}
	}
}

// TestGetReimbursementsPagingWalksTheWholeSetWithNoSkipOrDuplicate is #226's
// "paging walks the whole set with no skip and no duplicate" acceptance
// criterion, over 60 claims (more than two 25-row pages).
func TestGetReimbursementsPagingWalksTheWholeSetWithNoSkipOrDuplicate(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	seeded := seedReimbursements(t, sqlDB, setup.Fund.ID, memberID, setup.MainPurposeID, 60, "2026-01-01")

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
		rec := getReimbursements(t, r, rawQuery)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/reimbursements%s = %d, want %d (body: %s)", rawQuery, rec.Code, http.StatusOK, rec.Body.String())
		}
		page := decodeReimbursementsPage(t, rec)
		if pages < 2 && len(page.Reimbursements) != reimbursementsPageSize {
			t.Errorf("page %d = %d rows, want %d (60 rows over 25-row pages)", pages, len(page.Reimbursements), reimbursementsPageSize)
		}
		for _, row := range page.Reimbursements {
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
		want := seeded[len(seeded)-1-i].ID // seeded is oldest-first; the walk must be newest-first
		if gotID != want {
			t.Fatalf("row %d across the whole paged walk = id %d, want %d (newest-first order broken across a page boundary)", i, gotID, want)
		}
	}
}

// TestGetReimbursementsPagingHandlesABackdatedInsertBetweenPageFetches
// mirrors transactions_test.go's own test of the same name: a claim
// inserted after page 1 is fetched, dated older than everything on page 1
// but inside page 2's date range, must appear on page 2 exactly once -
// proving the keyset cursor (incurred_on, id) is what actually runs rather
// than LIMIT/OFFSET.
func TestGetReimbursementsPagingHandlesABackdatedInsertBetweenPageFetches(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	// 30 claims dated 2026-01-01 (oldest, seeded[0]) through 2026-01-30
	// (newest, seeded[29]).
	seeded := seedReimbursements(t, sqlDB, setup.Fund.ID, memberID, setup.MainPurposeID, 30, "2026-01-01")

	page1 := decodeReimbursementsPage(t, getReimbursements(t, r, ""))
	if len(page1.Reimbursements) != reimbursementsPageSize {
		t.Fatalf("page 1 = %d rows, want %d", len(page1.Reimbursements), reimbursementsPageSize)
	}
	if page1.NextCursor == nil {
		t.Fatal("page 1 next_cursor = nil, want a cursor - 30 rows is more than one page")
	}

	// Lands strictly inside page 2's date range (2026-01-01..2026-01-05, the
	// 5 rows page 1 did not cover) - older than every row already handed
	// out on page 1, and it did not exist when page 1 was fetched.
	q := store.New(sqlDB)
	backdated, err := q.CreateReimbursement(context.Background(), store.CreateReimbursementParams{
		FundID: setup.Fund.ID, MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 99_000, IncurredOn: "2026-01-03", CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("inserting the backdated row: %v", err)
	}

	page2 := decodeReimbursementsPage(t, getReimbursements(t, r, "?cursor="+url.QueryEscape(*page1.NextCursor)))

	occurrences, seenIDs := 0, map[int64]bool{}
	for _, row := range page2.Reimbursements {
		if seenIDs[row.ID] {
			t.Errorf("id %d appears more than once on page 2", row.ID)
		}
		seenIDs[row.ID] = true
		if row.ID == backdated.ID {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Errorf("the backdated row appeared %d times on page 2, want exactly 1", occurrences)
	}
	for _, want := range seeded[:5] {
		if !seenIDs[want.ID] {
			t.Errorf("row %d (incurred_on %s), originally on page 2, is missing after the backdated insert", want.ID, want.IncurredOn)
		}
	}
	if len(page2.Reimbursements) != 6 {
		t.Errorf("page 2 = %d rows, want 6 (the 5 original rows plus the backdated insert)", len(page2.Reimbursements))
	}
	if page2.NextCursor != nil {
		t.Errorf("page 2 next_cursor = %v, want nil - that was the last page", *page2.NextCursor)
	}
}

// TestGetReimbursementsRejectsAMalformedCursor is #226's "malformed cursor
// -> 400" acceptance criterion.
func TestGetReimbursementsRejectsAMalformedCursor(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	for _, cursor := range []string{"not-base64!!", "", "2026-13-40|1"} {
		rec := getReimbursements(t, r, "?cursor="+url.QueryEscape(cursor))
		if cursor == "" {
			// An empty cursor param is the same as omitting it - the first page.
			if rec.Code != http.StatusOK {
				t.Errorf("GET /api/reimbursements?cursor= = %d, want %d", rec.Code, http.StatusOK)
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/reimbursements?cursor=%q = %d, want %d (body: %s)", cursor, rec.Code, http.StatusBadRequest, rec.Body.String())
			continue
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("cursor %q error code = %q, want %q", cursor, got.Code, "invalid_argument")
		}
	}
}

// TestGetReimbursementsSearchHitsMemberNameCaseInsensitive covers this
// list's "member name" half of ADR-032's search surface (#226).
func TestGetReimbursementsSearchHitsMemberNameCaseInsensitive(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Budi Santoso")
	otherMemberID := memberFor(t, r, "Siti")

	if rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID, Amount: 20_000, IncurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: otherMemberID, PurposeID: setup.MainPurposeID, Amount: 5_000, IncurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeReimbursementsPage(t, getReimbursements(t, r, "?q=budi"))
	if len(page.Reimbursements) != 1 {
		t.Fatalf("q=budi returned %d rows, want 1: %+v", len(page.Reimbursements), page.Reimbursements)
	}
	if page.Reimbursements[0].MemberID != memberID {
		t.Errorf("matched row member_id = %d, want %d", page.Reimbursements[0].MemberID, memberID)
	}
}

// TestGetReimbursementsSearchHitsNoteCaseInsensitive covers this list's
// "note" half of ADR-032's search surface (#226).
func TestGetReimbursementsSearchHitsNoteCaseInsensitive(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	note := "Beli Galon Aqua"
	if rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID, Amount: 20_000, IncurredOn: "2026-08-01", Note: &note,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	other := "Something else entirely"
	if rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID, Amount: 5_000, IncurredOn: "2026-08-02", Note: &other,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeReimbursementsPage(t, getReimbursements(t, r, "?q=galon"))
	if len(page.Reimbursements) != 1 {
		t.Fatalf("q=galon returned %d rows, want 1: %+v", len(page.Reimbursements), page.Reimbursements)
	}
	if page.Reimbursements[0].Note == nil || *page.Reimbursements[0].Note != note {
		t.Errorf("matched row note = %v, want %q", page.Reimbursements[0].Note, note)
	}
}

// TestGetReimbursementsOutstandingAndSearchCombine proves the two filters
// AND together rather than one silently overriding the other: of two
// outstanding claims with matching notes, only the one that also matches
// ?q= comes back, and a settled claim matching ?q= never does even though
// its note alone would match.
func TestGetReimbursementsOutstandingAndSearchCombine(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	note := "Konsumsi rapat"
	wanted := claimForWithNote(t, r, setup, memberID, note)
	other := "Konsumsi lain"
	unwanted := claimForWithNote(t, r, setup, memberID, other)
	_ = unwanted

	settledNote := "Konsumsi rapat lama"
	settled := claimForWithNote(t, r, setup, memberID, settledNote)
	if rec := postSettlement(t, r, settled.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-15",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("settle = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeReimbursementsPage(t, getReimbursements(t, r, "?outstanding=true&q=konsumsi+rapat"))
	if len(page.Reimbursements) != 1 {
		t.Fatalf("outstanding+q returned %d rows, want 1: %+v", len(page.Reimbursements), page.Reimbursements)
	}
	if page.Reimbursements[0].ID != wanted.ID {
		t.Errorf("matched row id = %d, want %d", page.Reimbursements[0].ID, wanted.ID)
	}
}

// TestGetReimbursementsOutstandingExcludesSettledAndWaived is #226's own
// check that ListReimbursementsPage's outstanding_only branch keeps
// excluding both a settled and a waived claim, the same pair
// ListOutstandingReimbursementsByFund already excluded.
func TestGetReimbursementsOutstandingExcludesSettledAndWaived(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	settled := claimFor(t, r, setup, memberID)
	if rec := postSettlement(t, r, settled.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-15",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("settle = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	waived := claimFor(t, r, setup, memberID)
	waivedOn := "2026-08-16"
	patchBody := fmt.Sprintf(`{"waived_on":%q}`, waivedOn)
	if rec := patchReimbursement(t, r, waived.ID, patchBody); rec.Code != http.StatusOK {
		t.Fatalf("waiving = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	outstanding := claimFor(t, r, setup, memberID)

	page := decodeReimbursementsPage(t, getReimbursements(t, r, "?outstanding=true"))
	if len(page.Reimbursements) != 1 {
		t.Fatalf("outstanding returned %d rows, want 1: %+v", len(page.Reimbursements), page.Reimbursements)
	}
	if page.Reimbursements[0].ID != outstanding.ID {
		t.Errorf("outstanding row id = %d, want %d", page.Reimbursements[0].ID, outstanding.ID)
	}
	if page.Reimbursements[0].Settled {
		t.Errorf("outstanding row settled = true, want false")
	}
}

// claimForWithNote is claimFor with a caller-supplied note, for the search
// tests above that need to tell claims apart by more than amount.
func claimForWithNote(t *testing.T, r http.Handler, setup setupResponse, memberID int64, note string) reimbursementResponse {
	t.Helper()
	rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10", Note: &note,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return decodeReimbursement(t, rec)
}

// TestPostReimbursementsRejectsWhatTheSchemaRefuses proves this handler
// validates nothing itself: a non-positive amount, a calendar-invalid date
// and a member_id naming no row all come back through mapSQLiteError.
func TestPostReimbursementsRejectsWhatTheSchemaRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func(setup setupResponse, memberID int64) reimbursementRequest
		code string
	}{
		{"zero amount", func(s setupResponse, m int64) reimbursementRequest {
			return reimbursementRequest{MemberID: m, PurposeID: s.MainPurposeID, Amount: 0, IncurredOn: "2026-08-10"}
		}, "check_violation"},
		{"negative amount", func(s setupResponse, m int64) reimbursementRequest {
			return reimbursementRequest{MemberID: m, PurposeID: s.MainPurposeID, Amount: -80_000, IncurredOn: "2026-08-10"}
		}, "check_violation"},
		{"malformed incurred_on", func(s setupResponse, m int64) reimbursementRequest {
			return reimbursementRequest{MemberID: m, PurposeID: s.MainPurposeID, Amount: 80_000, IncurredOn: "2026-02-30"}
		}, "check_violation"},
		{"unknown member", func(s setupResponse, _ int64) reimbursementRequest {
			return reimbursementRequest{MemberID: 9_999, PurposeID: s.MainPurposeID, Amount: 80_000, IncurredOn: "2026-08-10"}
		}, "invalid_argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)
			setup := setUpFund(t, r)
			memberID := memberFor(t, r, "Jane")

			rec := postReimbursement(t, r, tc.req(setup, memberID))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != tc.code {
				t.Errorf("error code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

// TestPostSettlementOnAnUnknownClaimIs404 is the case that reaches
// mapLedgerError's sql.ErrNoRows arm: SettleReimbursement fetches the claim
// before writing, and an id naming nothing is the client's mistake about a
// path segment, not a server failure.
func TestPostSettlementOnAnUnknownClaimIs404(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postSettlement(t, r, 9_999, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("settling an unknown claim = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostSettlementRejectsAMalformedOccurredOn(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")

	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10",
	})
	var claim reimbursementResponse
	if err := json.NewDecoder(createRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}

	rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "not-a-date",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("settle with a malformed date = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostReimbursementsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/reimbursements with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetReimbursementsRequiresAFund(t *testing.T) {
	rec := getReimbursements(t, testRouter(t), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reimbursements before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostSettlementRequiresAFund(t *testing.T) {
	rec := postSettlement(t, testRouter(t), 1, settleReimbursementRequest{
		AccountID: 1, OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("settle before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestPostSettlementRejectsANonNumericID covers the one check this handler
// owns itself: {id} is a path segment, so a non-numeric one never reaches
// the ledger to be judged there.
func TestPostSettlementRejectsANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements/abc/settle",
		strings.NewReader(`{"account_id":1,"occurred_on":"2026-08-20"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("settle with a non-numeric id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostSettlementRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements/1/settle", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("settle with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestNoWaiveRouteExists is the acceptance criterion stated as a test: PRD
// section 7.4 never asks to waive a claim, so the route is absent and stays absent.
func TestNoWaiveRouteExists(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements/1/waive", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/reimbursements/1/waive = %d, want %d - no waive route is in scope", rec.Code, http.StatusNotFound)
	}
}

// decodeReimbursements decodes GET /api/reimbursements's envelope
// ({"reimbursements":[...],"next_cursor":...}, #226) and returns just the
// rows - every caller that predates paging only ever wanted the page's
// contents, never the cursor.
func decodeReimbursements(t *testing.T, rec *httptest.ResponseRecorder) []reimbursementResponse {
	t.Helper()
	return decodeReimbursementsPage(t, rec).Reimbursements
}

// decodeReimbursementsPage decodes GET /api/reimbursements's envelope in
// full, for the paging tests below that need next_cursor - the same idiom
// decodeTransactionsPage (transactions_test.go) uses for #225.
func decodeReimbursementsPage(t *testing.T, rec *httptest.ResponseRecorder) reimbursementsPageResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var page reimbursementsPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decoding reimbursements page: %v", err)
	}
	return page
}

func patchReimbursement(t *testing.T, r http.Handler, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/reimbursements/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body)))
	return rec
}

func deleteReimbursementReq(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/reimbursements/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}

// claimFor creates one unsettled claim and returns it.
func claimFor(t *testing.T, r http.Handler, setup setupResponse, memberID int64) reimbursementResponse {
	t.Helper()
	rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	return claim
}

func decodeReimbursement(t *testing.T, rec *httptest.ResponseRecorder) reimbursementResponse {
	t.Helper()
	var claim reimbursementResponse
	if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	return claim
}

// TestPatchReimbursementCorrectsAnUnsettledClaim is the correction half of
// #103: the wrong amount, the wrong member and a note, fixed in place
// because the claim is off the ledger until it is settled.
func TestPatchReimbursementCorrectsAnUnsettledClaim(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	otherID := memberFor(t, r, "Sam")
	claim := claimFor(t, r, setup, memberID)

	rec := patchReimbursement(t, r, claim.ID, `{"amount":95000,"member_id":`+fmt.Sprint(otherID)+`,"note":"it was Sam who paid"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/reimbursements/{id} = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	updated := decodeReimbursement(t, rec)
	if updated.Amount != 95_000 {
		t.Errorf("amount = %d, want 95000", updated.Amount)
	}
	if updated.MemberID != otherID {
		t.Errorf("member_id = %d, want %d", updated.MemberID, otherID)
	}
	if updated.Note == nil || *updated.Note != "it was Sam who paid" {
		t.Errorf("note = %v, want the corrected note", updated.Note)
	}
	// Absent keys are "leave alone", not "clear it".
	if updated.IncurredOn != claim.IncurredOn {
		t.Errorf("incurred_on = %q, want the untouched %q", updated.IncurredOn, claim.IncurredOn)
	}
	if updated.WaivedOn != nil {
		t.Errorf("waived_on = %v, want nil - it was never sent", updated.WaivedOn)
	}
}

// TestPatchReimbursementWaivesAndUnwaives is the waive half: one field, so
// the member who says "saya yang tanggung" and then changes their mind is
// not stuck.
func TestPatchReimbursementWaivesAndUnwaives(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	claim := claimFor(t, r, setup, memberID)

	rec := patchReimbursement(t, r, claim.ID, `{"waived_on":"2026-08-15"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("waive = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	waived := decodeReimbursement(t, rec)
	if waived.WaivedOn == nil || *waived.WaivedOn != "2026-08-15" {
		t.Fatalf("waived_on = %v, want %q", waived.WaivedOn, "2026-08-15")
	}

	if got := decodeReimbursements(t, getReimbursements(t, r, "?outstanding=true")); len(got) != 0 {
		t.Errorf("outstanding after waiving = %d claims, want 0", len(got))
	}
	if got := decodeReimbursements(t, getReimbursements(t, r, "")); len(got) != 1 {
		t.Errorf("all claims after waiving = %d, want 1 - waiving is not deleting", len(got))
	}

	rec = patchReimbursement(t, r, claim.ID, `{"waived_on":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("un-waive = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if unwaived := decodeReimbursement(t, rec); unwaived.WaivedOn != nil {
		t.Errorf("waived_on = %v, want nil after un-waiving", unwaived.WaivedOn)
	}
	if got := decodeReimbursements(t, getReimbursements(t, r, "?outstanding=true")); len(got) != 1 {
		t.Errorf("outstanding after un-waiving = %d claims, want 1", len(got))
	}
}

// TestSettleRefusesAClaimWaivedOverHTTP closes the loop between the two
// routes: waiving through PATCH reaches the same 409 settling has always
// given a claim created waived.
func TestSettleRefusesAClaimWaivedOverHTTP(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	claim := claimFor(t, r, setup, memberID)

	if rec := patchReimbursement(t, r, claim.ID, `{"waived_on":"2026-08-15"}`); rec.Code != http.StatusOK {
		t.Fatalf("waive = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("settling a waived claim = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "reimbursement_waived" {
		t.Errorf("error code = %q, want %q", got.Code, "reimbursement_waived")
	}
}

func TestDeleteReimbursementRemovesAnUnsettledClaim(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	claim := claimFor(t, r, setup, memberID)

	rec := deleteReimbursementReq(t, r, claim.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/reimbursements/{id} = %d, want %d (body: %s)", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got := decodeReimbursements(t, getReimbursements(t, r, "")); len(got) != 0 {
		t.Errorf("claims after delete = %d, want 0", len(got))
	}
}

// TestPatchAndDeleteRefuseASettledClaim is the boundary at the route: once
// a payout references the claim, both are its named 409.
func TestPatchAndDeleteRefuseASettledClaim(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	claim := claimFor(t, r, setup, memberID)

	if rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("settle = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	for _, tc := range []struct {
		name string
		rec  func() *httptest.ResponseRecorder
	}{
		{"patch", func() *httptest.ResponseRecorder { return patchReimbursement(t, r, claim.ID, `{"amount":95000}`) }},
		{"waive", func() *httptest.ResponseRecorder {
			return patchReimbursement(t, r, claim.ID, `{"waived_on":"2026-08-25"}`)
		}},
		{"delete", func() *httptest.ResponseRecorder { return deleteReimbursementReq(t, r, claim.ID) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec()
			if rec.Code != http.StatusConflict {
				t.Fatalf("%s on a settled claim = %d, want %d (body: %s)", tc.name, rec.Code, http.StatusConflict, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "reimbursement_already_settled" {
				t.Errorf("error code = %q, want %q", got.Code, "reimbursement_already_settled")
			}
		})
	}
}

// TestPatchReimbursementUnknownMemberIs400 is the case that made
// mapLedgerError classify SQLite errors at all: the id comes from the
// request body, so a foreign-key violation is the caller's typo, not a
// server fault, and answering 500 would blame the wrong party.
func TestPatchReimbursementUnknownMemberIs400(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	claim := claimFor(t, r, setup, memberID)

	rec := patchReimbursement(t, r, claim.ID, `{"member_id":9999}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with an unknown member_id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPatchReimbursementRejectsBadArguments(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"non-positive amount", `{"amount":0}`},
		{"malformed incurred_on", `{"incurred_on":"2026-02-30"}`},
		{"malformed waived_on", `{"waived_on":"not-a-date"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)
			setup := setUpFund(t, r)
			memberID := memberFor(t, r, "Jane")
			claim := claimFor(t, r, setup, memberID)

			rec := patchReimbursement(t, r, claim.ID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PATCH %s = %d, want %d (body: %s)", tc.body, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "invalid_argument" {
				t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
			}
		})
	}
}

func TestPatchAndDeleteOnAnUnknownClaimAre404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	if rec := patchReimbursement(t, r, 9_999, `{"amount":95000}`); rec.Code != http.StatusNotFound {
		t.Errorf("PATCH on an unknown claim = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec := deleteReimbursementReq(t, r, 9_999); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE on an unknown claim = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestPatchReimbursementRejectsMalformedJSONAndIDs(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := patchReimbursement(t, r, 1, "{oops")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := decodeError(t, rec); got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}

	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/api/reimbursements/abc", strings.NewReader(`{}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s /api/reimbursements/abc = %d, want %d", method, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestPatchAndDeleteRequireAFund(t *testing.T) {
	if rec := patchReimbursement(t, testRouter(t), 1, `{"amount":95000}`); rec.Code != http.StatusNotFound {
		t.Errorf("PATCH before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if rec := deleteReimbursementReq(t, testRouter(t), 1); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestDeleteReimbursementWithAReceiptIs409 answers #103's open question:
// a claim that still has a photo attached is refused rather than taking the
// receipt down with it, the same answer deleting a member with posted
// transactions gives. Receipts have no route yet (#73), so the row is
// written through store.Queries - the path an import would use.
func TestDeleteReimbursementWithAReceiptIs409(t *testing.T) {
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	r := authedRouterFor(t, sqlDB)

	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	claim := claimFor(t, r, setup, memberID)

	if _, err := q.CreateReceipt(context.Background(), store.CreateReceiptParams{
		FundID:          setup.Fund.ID,
		ReimbursementID: &claim.ID,
		Path:            "receipts/1.jpg",
		UploadedAt:      time.Now().Unix(),
	}); err != nil {
		t.Fatalf("CreateReceipt() = %v, want no error", err)
	}

	rec := deleteReimbursementReq(t, r, claim.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting a claim with a receipt = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "referenced_by_other_records" {
		t.Errorf("error code = %q, want %q", got.Code, "referenced_by_other_records")
	}
}
