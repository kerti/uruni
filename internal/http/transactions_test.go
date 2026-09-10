package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
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

func postTransaction(t *testing.T, r http.Handler, req transactionRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling transaction request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)))
	return rec
}

func getTransactions(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/transactions", nil))
	return rec
}

// getTransactionsQuery is getTransactions's twin for #225's own tests: every
// case below that exercises the cursor, q, member_id or dues_period query
// parameters needs a raw query string on the request, which getTransactions
// deliberately never takes (most of this package's other test files only
// want "everything posted so far", unpaged and unfiltered).
func getTransactionsQuery(t *testing.T, r http.Handler, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/transactions"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// decodeTransactionsPage decodes GET /api/transactions's envelope
// ({"transactions":[...],"next_cursor":...}, #225) and fails the test on any
// shape mismatch, the same t.Fatalf-on-decode-error idiom every other
// decode helper in this package uses.
func decodeTransactionsPage(t *testing.T, rec *httptest.ResponseRecorder) transactionsPageResponse {
	t.Helper()
	var page transactionsPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decoding GET /api/transactions response: %v (body: %s)", err, rec.Body.String())
	}
	return page
}

func TestPostTransactionsRequiresAFund(t *testing.T) {
	rec := postTransaction(t, testRouter(t), transactionRequest{
		AccountID: 1, PurposeID: 1, Direction: "in", Amount: 10_000, OccurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/transactions before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetTransactionsRequiresAFund(t *testing.T) {
	rec := getTransactions(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/transactions before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetTransactionsReturnsAnEmptyListBeforeAnyTransaction is #225's
// "empty fund -> empty list + null cursor" acceptance criterion.
func TestGetTransactionsReturnsAnEmptyListBeforeAnyTransaction(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getTransactions(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	page := decodeTransactionsPage(t, rec)
	if len(page.Transactions) != 0 {
		t.Errorf("GET /api/transactions before any post returned %d rows, want 0", len(page.Transactions))
	}
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v, want nil on an empty fund", *page.NextCursor)
	}
}

// TestPostTransactionsRoundTripsThroughGetAndMovesTheBalance is the slice's
// headline acceptance criterion: a posted transaction shows up unchanged
// through GET /api/transactions, and the fund balance moves by exactly its
// amount - no more, no less.
func TestPostTransactionsRoundTripsThroughGetAndMovesTheBalance(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)

	note := "Kas awal kegiatan 17-an"
	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 150_000, OccurredOn: "2026-08-12", Note: &note,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var posted transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	if posted.ID == 0 {
		t.Error("transaction.id is zero")
	}
	if posted.Kind != "normal" {
		t.Errorf("transaction.kind = %q, want %q", posted.Kind, "normal")
	}
	if posted.Amount != 150_000 {
		t.Errorf("transaction.amount = %d, want %d", posted.Amount, 150_000)
	}
	if posted.AccountID != setup.CashAccountID(t) {
		t.Errorf("transaction.account_id = %d, want %d", posted.AccountID, setup.CashAccountID(t))
	}
	if posted.Note == nil || *posted.Note != note {
		t.Errorf("transaction.note = %v, want %q", posted.Note, note)
	}

	list := getTransactions(t, r)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}
	got := decodeTransactionsPage(t, list).Transactions
	if len(got) != 1 {
		t.Fatalf("GET /api/transactions returned %d rows, want 1 (body: %s)", len(got), list.Body.String())
	}
	if got[0].ID != posted.ID || got[0].Amount != posted.Amount || got[0].Kind != posted.Kind ||
		got[0].Direction != posted.Direction || got[0].AccountID != posted.AccountID ||
		got[0].PurposeID != posted.PurposeID || got[0].OccurredOn != posted.OccurredOn ||
		got[0].Note == nil || posted.Note == nil || *got[0].Note != *posted.Note {
		t.Errorf("GET /api/transactions = %+v, want the same row POST /api/transactions returned (%+v)", got[0], posted)
	}

	fundBal, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal.Int64() != 150_000 {
		t.Errorf("FundBalance() = %d, want %d - the fund started at zero and this is the only posted row", fundBal.Int64(), 150_000)
	}
}

// TestPostTransactionsOutDirectionMovesTheBalanceDown checks the other
// boundary: a direction='out' entry decreases the fund balance by exactly
// its amount, mirroring the 'in' case above rather than merely summing to
// something nonzero.
func TestPostTransactionsOutDirectionMovesTheBalanceDown(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	ctx := context.Background()

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 200_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions (in) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 60_000, OccurredOn: "2026-08-05",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions (out) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	fundBal, err := l.FundBalance(ctx, setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal.Int64() != 140_000 {
		t.Errorf("FundBalance() = %d, want %d (200000 in, 60000 out)", fundBal.Int64(), 140_000)
	}
}

// TestPostTransactionsIsAdjustmentPostsAnAdjustmentKind proves the ADR-027
// intent surface: IsAdjustment on the wire selects kind='adjustment' rather
// than a raw kind field the caller could otherwise set to anything.
func TestPostTransactionsIsAdjustmentPostsAnAdjustmentKind(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-12",
		IsAdjustment: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	if posted.Kind != "adjustment" {
		t.Errorf("transaction.kind = %q, want %q", posted.Kind, "adjustment")
	}
}

// TestPostTransactionsRejectsNonPositiveAmount covers the acceptance
// criterion end to end: a non-positive amount surfaces as 400 through the
// real HTTP path, never re-checked here - PostTransaction's own check is
// what answers.
func TestPostTransactionsRejectsNonPositiveAmount(t *testing.T) {
	for _, amount := range []int64{0, -1, -50_000} {
		r := testRouter(t)
		setup := setUpFund(t, r)

		rec := postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "in", Amount: amount, OccurredOn: "2026-08-12",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/transactions (amount=%d) = %d, want %d (body: %s)", amount, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

// TestPostTransactionsRejectsAMalformedOccurredOn is the acceptance
// criterion's other half: a calendar-invalid date surfaces as 400 too.
func TestPostTransactionsRejectsAMalformedOccurredOn(t *testing.T) {
	for _, occurredOn := range []string{"2026-02-30", "not-a-date", "2026-8-12"} {
		r := testRouter(t)
		setup := setUpFund(t, r)

		rec := postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "in", Amount: 10_000, OccurredOn: occurredOn,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/transactions (occurred_on=%q) = %d, want %d (body: %s)", occurredOn, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

func TestPostTransactionsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transactions", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transactions with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestPostTransactionsRejectsAnUnrecognizedDirection is the argument-shape
// case PostTransaction itself validates: "sideways" is neither "in" nor
// "out". Asserted here to prove the handler passes the raw string through
// rather than pre-checking it against an allow-list of its own.
func TestPostTransactionsRejectsAnUnrecognizedDirection(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "sideways", Amount: 10_000, OccurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transactions (direction=sideways) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// ---- #225: keyset paging and search --------------------------------------

// seedTransactions writes n ordinary 'normal' transactions straight through
// the store (bypassing the HTTP route, same idiom fund_scope_test.go uses
// for fixtures that don't need the route's own validation), one calendar
// day apart starting at startDate, oldest first in the returned slice - the
// paging tests below need many rows with known, distinct occurred_on values
// more than they need those rows to have come through POST
// /api/transactions itself.
func seedTransactions(t *testing.T, sqlDB *sql.DB, fundID, accountID, purposeID int64, n int, startDate string) []store.Transaction {
	t.Helper()
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		t.Fatalf("parsing start date %q: %v", startDate, err)
	}
	q := store.New(sqlDB)
	ctx := context.Background()
	rows := make([]store.Transaction, 0, n)
	for i := 0; i < n; i++ {
		row, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: fundID, AccountID: accountID, PurposeID: purposeID,
			Direction: "in", Amount: int64(10_000 + i), OccurredOn: start.AddDate(0, 0, i).Format("2006-01-02"),
			Kind: "normal", CreatedAt: int64(i + 1),
		})
		if err != nil {
			t.Fatalf("seeding transaction %d: %v", i, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// TestGetTransactionsOrdersNewestFirstTiesBrokenByIDDesc is the ordering
// half of #225's "newest-first, keyset-paged" acceptance criterion: three
// rows, two sharing an occurred_on, must come back (occurred_on DESC, id
// DESC) - the later of the two same-dated rows first.
func TestGetTransactionsOrdersNewestFirstTiesBrokenByIDDesc(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	q := store.New(sqlDB)
	ctx := context.Background()

	older, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-08-05", Kind: "normal", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("seeding older: %v", err)
	}
	tie1, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-10", Kind: "normal", CreatedAt: 2,
	})
	if err != nil {
		t.Fatalf("seeding tie1: %v", err)
	}
	tie2, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 30_000, OccurredOn: "2026-08-10", Kind: "normal", CreatedAt: 3,
	})
	if err != nil {
		t.Fatalf("seeding tie2: %v", err)
	}

	got := decodeTransactionsPage(t, getTransactions(t, r)).Transactions
	if len(got) != 3 {
		t.Fatalf("GET /api/transactions returned %d rows, want 3", len(got))
	}
	wantOrder := []int64{tie2.ID, tie1.ID, older.ID}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("row %d = id %d, want %d (order %v)", i, got[i].ID, want, wantOrder)
		}
	}
}

// TestGetTransactionsPagingWalksTheWholeSetWithNoSkipOrDuplicate is #225's
// "Paging walks the whole set with no skip and no duplicate" acceptance
// criterion: 60 rows (more than two pages at 25 each) are reachable exactly
// once each by following next_cursor to the end, and the order across page
// boundaries stays newest-first.
func TestGetTransactionsPagingWalksTheWholeSetWithNoSkipOrDuplicate(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	seeded := seedTransactions(t, sqlDB, setup.Fund.ID, setup.CashAccountID(t), setup.MainPurposeID, 60, "2026-01-01")

	seen := map[int64]int{}
	var order []int64
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("more than 10 pages fetched - paging did not terminate")
		}
		rawQuery := ""
		if cursor != "" {
			rawQuery = "cursor=" + url.QueryEscape(cursor)
		}
		rec := getTransactionsQuery(t, r, rawQuery)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/transactions?%s = %d, want %d (body: %s)", rawQuery, rec.Code, http.StatusOK, rec.Body.String())
		}
		page := decodeTransactionsPage(t, rec)
		if pages < 2 && len(page.Transactions) != transactionsPageSize {
			t.Errorf("page %d = %d rows, want %d (60 rows over 25-row pages)", pages, len(page.Transactions), transactionsPageSize)
		}
		for _, row := range page.Transactions {
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
	if len(order) != len(seeded) {
		t.Fatalf("walked %d rows total, want %d", len(order), len(seeded))
	}
	for i, gotID := range order {
		want := seeded[len(seeded)-1-i].ID // seeded is oldest-first; the walk must be newest-first
		if gotID != want {
			t.Fatalf("row %d across the whole paged walk = id %d, want %d (newest-first order broken across a page boundary)", i, gotID, want)
		}
	}
}

// TestGetTransactionsPagingHandlesABackdatedInsertBetweenPageFetches is
// #225's required test: a row inserted after page 1 is fetched, dated
// older than everything on page 1 but inside page 2's date range, must
// appear on page 2 exactly once - proving the keyset cursor (occurred_on,
// id) rather than LIMIT/OFFSET is what actually runs, since an offset would
// have silently skipped or duplicated a row landing in the middle of the
// list like this one does.
func TestGetTransactionsPagingHandlesABackdatedInsertBetweenPageFetches(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	// 30 rows dated 2026-01-01 (oldest, seeded[0]) through 2026-01-30
	// (newest, seeded[29]).
	seeded := seedTransactions(t, sqlDB, setup.Fund.ID, setup.CashAccountID(t), setup.MainPurposeID, 30, "2026-01-01")

	// Page 1 is the newest 25: 2026-01-30 down through 2026-01-06.
	page1 := decodeTransactionsPage(t, getTransactionsQuery(t, r, ""))
	if len(page1.Transactions) != transactionsPageSize {
		t.Fatalf("page 1 = %d rows, want %d", len(page1.Transactions), transactionsPageSize)
	}
	if page1.NextCursor == nil {
		t.Fatal("page 1 next_cursor = nil, want a cursor - 30 rows is more than one page")
	}

	// Lands strictly inside page 2's date range (2026-01-01..2026-01-05, the
	// 5 rows page 1 did not cover) - older than every row already handed
	// out on page 1, and it did not exist when page 1 was fetched.
	q := store.New(sqlDB)
	backdated, err := q.CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 99_000, OccurredOn: "2026-01-03", Kind: "normal", CreatedAt: 1000,
	})
	if err != nil {
		t.Fatalf("inserting the backdated row: %v", err)
	}

	page2 := decodeTransactionsPage(t, getTransactionsQuery(t, r, "cursor="+url.QueryEscape(*page1.NextCursor)))

	occurrences, seenIDs := 0, map[int64]bool{}
	for _, row := range page2.Transactions {
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
	for _, want := range seeded[:5] { // the 5 rows that belonged on page 2 before the insert
		if !seenIDs[want.ID] {
			t.Errorf("row %d (occurred_on %s), originally on page 2, is missing after the backdated insert", want.ID, want.OccurredOn)
		}
	}
	if len(page2.Transactions) != 6 {
		t.Errorf("page 2 = %d rows, want 6 (the 5 original rows plus the backdated insert)", len(page2.Transactions))
	}
	if page2.NextCursor != nil {
		t.Errorf("page 2 next_cursor = %v, want nil - that was the last page", *page2.NextCursor)
	}
}

// TestGetTransactionsSearchHitsNoteCaseInsensitive covers ADR-032's "search
// over note" (Transaksi's own row in the List/Search-over table), and that
// it is case-insensitive: q is lower-case, the note is not.
func TestGetTransactionsSearchHitsNoteCaseInsensitive(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	note := "Beli Galon Aqua"
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 20_000, OccurredOn: "2026-08-01", Note: &note,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	other := "Something else entirely"
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-08-02", Note: &other,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeTransactionsPage(t, getTransactionsQuery(t, r, "q=galon"))
	if len(page.Transactions) != 1 {
		t.Fatalf("q=galon returned %d rows, want 1: %+v", len(page.Transactions), page.Transactions)
	}
	if page.Transactions[0].Note == nil || *page.Transactions[0].Note != note {
		t.Errorf("matched row note = %v, want %q", page.Transactions[0].Note, note)
	}
}

// TestGetTransactionsSearchHitsPurposeName is ADR-032's "search over
// purpose name" half.
func TestGetTransactionsSearchHitsPurposeName(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	ppRec := postPassThroughPurpose(t, r, "Kas Bidang Olahraga")
	if ppRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/pass-through-purposes = %d, want %d (body: %s)", ppRec.Code, http.StatusCreated, ppRec.Body.String())
	}
	var purpose purposeResponse
	if err := json.NewDecoder(ppRec.Body).Decode(&purpose); err != nil {
		t.Fatalf("decoding purpose response: %v", err)
	}

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: purpose.ID,
		Direction: "in", Amount: 15_000, OccurredOn: "2026-08-03",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	// On the main purpose, whose name must not match.
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 5_000, OccurredOn: "2026-08-04",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeTransactionsPage(t, getTransactionsQuery(t, r, "q=olahraga"))
	if len(page.Transactions) != 1 {
		t.Fatalf("q=olahraga returned %d rows, want 1: %+v", len(page.Transactions), page.Transactions)
	}
	if page.Transactions[0].PurposeID != purpose.ID {
		t.Errorf("matched row purpose_id = %d, want %d", page.Transactions[0].PurposeID, purpose.ID)
	}
}

// TestGetTransactionsSearchHitsMemberName is ADR-032's "search over member
// name" half - member name reaches a transaction row only through the
// LEFT JOIN member (most rows carry no member_id at all).
func TestGetTransactionsSearchHitsMemberName(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Budi Santoso"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	otherRec := postMember(t, r, memberRequest{Name: "Siti"})
	var otherMember memberResponse
	if err := json.NewDecoder(otherRec.Body).Decode(&otherMember); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	payRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if payRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", payRec.Code, http.StatusCreated, payRec.Body.String())
	}
	otherPayRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: otherMember.ID, OccurredOn: "2026-08-06",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if otherPayRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", otherPayRec.Code, http.StatusCreated, otherPayRec.Body.String())
	}

	page := decodeTransactionsPage(t, getTransactionsQuery(t, r, "q=santoso"))
	if len(page.Transactions) != 1 {
		t.Fatalf("q=santoso returned %d rows, want 1: %+v", len(page.Transactions), page.Transactions)
	}
	if page.Transactions[0].MemberID == nil || *page.Transactions[0].MemberID != member.ID {
		t.Errorf("matched row member_id = %v, want %d", page.Transactions[0].MemberID, member.ID)
	}
}

// TestGetTransactionsSearchAllDigitsMatchesExactAmount is ADR-032's "what
// was that 50.000?" case: an all-digit q also matches amount exactly, not
// as a substring of a larger figure.
func TestGetTransactionsSearchAllDigitsMatchesExactAmount(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	for _, amount := range []int64{50_000, 500_000, 5_000} {
		if rec := postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "out", Amount: amount, OccurredOn: "2026-08-01",
		}); rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/transactions (amount=%d) = %d, want %d", amount, rec.Code, http.StatusCreated)
		}
	}

	page := decodeTransactionsPage(t, getTransactionsQuery(t, r, "q=50000"))
	if len(page.Transactions) != 1 {
		t.Fatalf("q=50000 returned %d rows, want 1 - an exact amount match, not a substring of 500000: %+v", len(page.Transactions), page.Transactions)
	}
	if page.Transactions[0].Amount != 50_000 {
		t.Errorf("matched row amount = %d, want %d", page.Transactions[0].Amount, 50_000)
	}
}

// TestGetTransactionsSearchTreatsWildcardsAsLiteral is #225's "LIKE
// wildcards in q are literal": a q containing % or _ must match only notes
// containing that literal character, never act as a SQL LIKE wildcard.
func TestGetTransactionsSearchTreatsWildcardsAsLiteral(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	mustPostNote := func(note, occurredOn string) {
		t.Helper()
		if rec := postTransaction(t, r, transactionRequest{
			AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
			Direction: "out", Amount: 9_000, OccurredOn: occurredOn, Note: &note,
		}); rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
	}

	percentNote := "Diskon 10% dari toko"
	mustPostNote(percentNote, "2026-08-01")
	// If % were a wildcard rather than literal, q="10%" would also match
	// this row - it has no percent sign, just more text after "10".
	mustPostNote("Diskon 10X dari toko lain", "2026-08-02")

	page := decodeTransactionsPage(t, getTransactionsQuery(t, r, "q="+url.QueryEscape("10%")))
	if len(page.Transactions) != 1 {
		t.Fatalf(`q="10%%" returned %d rows, want 1 - a wildcard-interpreting LIKE would also match "10X": %+v`, len(page.Transactions), page.Transactions)
	}
	if page.Transactions[0].Note == nil || *page.Transactions[0].Note != percentNote {
		t.Errorf("matched row note = %v, want %q", page.Transactions[0].Note, percentNote)
	}

	underscoreNote := "Kode A_B dibeli"
	mustPostNote(underscoreNote, "2026-08-03")
	// If _ were a wildcard rather than literal, q="A_B" would also match
	// this row - _ matches any single character in SQL LIKE.
	mustPostNote("Kode AXB dibeli juga", "2026-08-04")

	page2 := decodeTransactionsPage(t, getTransactionsQuery(t, r, "q=A_B"))
	if len(page2.Transactions) != 1 {
		t.Fatalf(`q="A_B" returned %d rows, want 1 - a wildcard-interpreting LIKE would also match "AXB": %+v`, len(page2.Transactions), page2.Transactions)
	}
	if page2.Transactions[0].Note == nil || *page2.Transactions[0].Note != underscoreNote {
		t.Errorf("matched row note = %v, want %q", page2.Transactions[0].Note, underscoreNote)
	}
}

// TestGetTransactionsMemberIDAndDuesPeriodFiltersIncludeTheReversal is
// #225's member_id/dues_period filter, exercised the way its one real
// caller (Dues/MemberPayments.tsx) uses it: both the kind='dues' payment
// and the kind='adjustment' row that reverses it carry the same
// member_id/dues_period (ADR-029), so both must show under the filter -
// and rows for another member or another period must not.
func TestGetTransactionsMemberIDAndDuesPeriodFiltersIncludeTheReversal(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	otherRec := postMember(t, r, memberRequest{Name: "John"})
	var other memberResponse
	if err := json.NewDecoder(otherRec.Body).Decode(&other); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	payRec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-08-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if payRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", payRec.Code, http.StatusCreated, payRec.Body.String())
	}
	var payment []transactionResponse
	if err := json.NewDecoder(payRec.Body).Decode(&payment); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}

	// Noise this filter must exclude: another period for the same member,
	// and the same period for another member.
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-09-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-09", Amount: 25_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments (other period) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: other.ID, OccurredOn: "2026-08-06",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments (other member) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	revRec := postDuesPaymentReversal(t, r, payment[0].ID, reverseDuesPaymentRequest{OccurredOn: "2026-08-20"})
	if revRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal = %d, want %d (body: %s)", revRec.Code, http.StatusCreated, revRec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(revRec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}

	page := decodeTransactionsPage(t, getTransactionsQuery(t, r, fmt.Sprintf("member_id=%d&dues_period=2026-08", member.ID)))
	gotIDs := map[int64]bool{}
	for _, row := range page.Transactions {
		gotIDs[row.ID] = true
	}
	if !gotIDs[payment[0].ID] {
		t.Errorf("filtered list is missing the original payment (id %d): %+v", payment[0].ID, page.Transactions)
	}
	if !gotIDs[reversal.ID] {
		t.Errorf("filtered list is missing the reversal (id %d): %+v", reversal.ID, page.Transactions)
	}
	if len(page.Transactions) != 2 {
		t.Errorf("member_id+dues_period filter returned %d rows, want 2 (the payment and its reversal only): %+v", len(page.Transactions), page.Transactions)
	}
}

// TestGetTransactionsRejectsAMalformedCursor is #225's "malformed cursor ->
// 400" acceptance criterion.
func TestGetTransactionsRejectsAMalformedCursor(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	badCursors := []string{
		"not-valid-base64!!",
		base64.RawURLEncoding.EncodeToString([]byte("no-pipe-separator")),
		base64.RawURLEncoding.EncodeToString([]byte("not-a-date|1")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-08-12|not-an-id")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-08-12|0")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-02-30|1")), // calendar-invalid
	}
	for _, cursor := range badCursors {
		rec := getTransactionsQuery(t, r, "cursor="+url.QueryEscape(cursor))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/transactions?cursor=%q = %d, want %d (body: %s)", cursor, rec.Code, http.StatusBadRequest, rec.Body.String())
			continue
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("cursor %q: error code = %q, want %q", cursor, got.Code, "invalid_argument")
		}
	}
}

// TestGetTransactionsRejectsAMalformedMemberID is #225's "malformed ...
// member_id -> 400" acceptance criterion.
func TestGetTransactionsRejectsAMalformedMemberID(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getTransactionsQuery(t, r, "member_id=not-a-number")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/transactions?member_id=not-a-number = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestGetTransactionsRejectsAMalformedDuesPeriod covers the same "validate
// types -> 400 on garbage" rule for dues_period as the member_id test above.
func TestGetTransactionsRejectsAMalformedDuesPeriod(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	for _, duesPeriod := range []string{"2026-13", "not-a-period", "2026/08"} {
		rec := getTransactionsQuery(t, r, "dues_period="+url.QueryEscape(duesPeriod))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/transactions?dues_period=%q = %d, want %d (body: %s)", duesPeriod, rec.Code, http.StatusBadRequest, rec.Body.String())
			continue
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("dues_period %q: error code = %q, want %q", duesPeriod, got.Code, "invalid_argument")
		}
	}
}

// TestGetTransactionsNeverReturnsAnotherFundsRow is #225's "fund scoping
// still holds" acceptance criterion, applied to the new paged/searched
// query rather than the old unfiltered one.
func TestGetTransactionsNeverReturnsAnotherFundsRow(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// A second fund, written straight through the store - the API refuses a
	// second fund by design (ErrFundAlreadyExists), same as
	// fund_scope_test.go's own otherFundFixture.
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
	otherRow, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: otherFund.ID, AccountID: otherAccount.ID, PurposeID: otherPurpose.ID,
		Direction: "in", Amount: 999_000, OccurredOn: "2026-08-01", Kind: "normal", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(other) = %v, want no error", err)
	}

	page := decodeTransactionsPage(t, getTransactions(t, r))
	for _, row := range page.Transactions {
		if row.ID == otherRow.ID {
			t.Errorf("GET /api/transactions on our fund returned a row belonging to another fund: %+v", row)
		}
	}
}
