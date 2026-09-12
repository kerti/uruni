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

	"github.com/kerti/uruni/internal/store"
)

func postReconciliation(t *testing.T, r http.Handler, req takeReconciliationRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling reconciliation request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reconciliations", bytes.NewReader(body)))
	return rec
}

func getReconciliations(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reconciliations", nil))
	return rec
}

// getReconciliationsWithQuery is getReconciliations plus a raw query string
// (e.g. "?cursor=..."), for the paging tests below.
func getReconciliationsWithQuery(t *testing.T, r http.Handler, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reconciliations"+rawQuery, nil))
	return rec
}

func decodeReconciliationsPage(t *testing.T, rec *httptest.ResponseRecorder) reconciliationsPageResponse {
	t.Helper()
	var got reconciliationsPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding reconciliations page: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// seedReconciliations inserts n bare snapshots (no lines) directly through
// the store, oldest first, each performed_at 1 second apart starting at
// startPerformedAt - the same "seed directly, bypass the ledger" shape
// seedReimbursements uses, since these paging tests care about ordering and
// count, not about what TakeReconciliation itself validates.
func seedReconciliations(t *testing.T, sqlDB *sql.DB, fundID int64, n int, startPerformedAt int64) []store.Reconciliation {
	t.Helper()
	q := store.New(sqlDB)
	ctx := context.Background()
	rows := make([]store.Reconciliation, 0, n)
	for i := 0; i < n; i++ {
		row, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
			FundID:      fundID,
			PerformedAt: startPerformedAt + int64(i),
			CreatedAt:   startPerformedAt + int64(i),
		})
		if err != nil {
			t.Fatalf("seeding reconciliation %d: %v", i, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func getLatestReconciliation(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reconciliations/latest", nil))
	return rec
}

func getOpenReconciliationLines(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reconciliations/open-lines", nil))
	return rec
}

func getReconciliationDetail(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/reconciliations/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decodeReconciliationDetail(t *testing.T, rec *httptest.ResponseRecorder) reconciliationDetailResponse {
	t.Helper()
	var got reconciliationDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding reconciliation detail: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// lineFor finds the response line for accountID, or fails the test - every
// assertion below is about one specific line, and a missing line is itself a
// failure, mirroring internal/ledger's own lineFor helper.
func lineFor(t *testing.T, lines []reconciliationLineResponse, accountID int64) reconciliationLineResponse {
	t.Helper()
	for _, ln := range lines {
		if ln.AccountID == accountID {
			return ln
		}
	}
	t.Fatalf("no line for account %d among %+v", accountID, lines)
	return reconciliationLineResponse{}
}

func TestPostReconciliationsRequiresAFund(t *testing.T) {
	rec := postReconciliation(t, testRouter(t), takeReconciliationRequest{
		Counts: []accountCountRequest{{AccountID: 1, ActualAmount: 0, Resolution: "matched"}},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/reconciliations before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetReconciliationsRequiresAFund(t *testing.T) {
	rec := getReconciliations(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetReconciliationLatestRequiresAFund(t *testing.T) {
	rec := getLatestReconciliation(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/latest before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetReconciliationOpenLinesRequiresAFund(t *testing.T) {
	rec := getOpenReconciliationLines(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/open-lines before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetReconciliationDetailRequiresAFund(t *testing.T) {
	rec := getReconciliationDetail(t, testRouter(t), 1)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/{id} before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetReconciliationLatestBeforeAnySnapshotIs404 is the "latest when
// empty" decision this slice makes explicit: 404 "not_found," the same code
// every other "no such row yet" route in this package already answers with
// (GET /api/fund before setup, GET /api/incidentals/{id} on an unknown id) -
// not an empty 200. See latestReconciliation's own doc comment for why.
func TestGetReconciliationLatestBeforeAnySnapshotIs404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getLatestReconciliation(t, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/latest before any snapshot = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestReconciliationsLatestAndOpenLinesAreNotParsedAsIDs is the route-ordering
// acceptance criterion stated directly: with a fund set up but no snapshot
// taken, GET /api/reconciliations/latest and GET /api/reconciliations/open-lines
// must resolve to their own static handlers, not fall into the {id} route and
// fail strconv.ParseInt on "latest"/"open-lines" as invalid_argument.
func TestReconciliationsLatestAndOpenLinesAreNotParsedAsIDs(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	latest := getLatestReconciliation(t, r)
	if latest.Code == http.StatusBadRequest {
		t.Fatalf("GET /api/reconciliations/latest = %d (body: %s), want it not to be parsed as {id}", latest.Code, latest.Body.String())
	}
	if latest.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/latest = %d, want %d (no snapshot yet)", latest.Code, http.StatusNotFound)
	}
	if got := decodeError(t, latest).Code; got == "invalid_argument" {
		t.Errorf("error code = %q, want anything but invalid_argument - that would mean \"latest\" was parsed as an id", got)
	}

	openLines := getOpenReconciliationLines(t, r)
	if openLines.Code != http.StatusOK {
		t.Fatalf("GET /api/reconciliations/open-lines = %d, want %d (body: %s) - it must not be parsed as {id}",
			openLines.Code, http.StatusOK, openLines.Body.String())
	}
	var lines []reconciliationLineResponse
	if err := json.NewDecoder(openLines.Body).Decode(&lines); err != nil {
		t.Fatalf("decoding open lines: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("open lines = %+v, want empty - nothing has been reconciled yet", lines)
	}
}

// TestTakeReconciliationMatchedRoundTripsThroughListAndDetail is the plain
// happy path: a count that agrees with the recorded balance posts nothing,
// resolves as "matched," and reads back identically through both the list
// and the detail route.
func TestTakeReconciliationMatchedRoundTripsThroughListAndDetail(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed transaction = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	note := "monthly count"
	takeRec := postReconciliation(t, r, takeReconciliationRequest{
		Note: &note,
		Counts: []accountCountRequest{
			{AccountID: setup.CashAccountID(t), ActualAmount: 100_000, Resolution: "matched"},
		},
	})
	if takeRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", takeRec.Code, http.StatusCreated, takeRec.Body.String())
	}
	created := decodeReconciliationDetail(t, takeRec)
	if created.ID == 0 {
		t.Fatal("id is zero, want a real id")
	}
	if created.Note == nil || *created.Note != note {
		t.Errorf("note = %v, want %q", created.Note, note)
	}
	if len(created.Lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(created.Lines))
	}
	line := created.Lines[0]
	if line.RecordedAmount != 100_000 || line.ActualAmount != 100_000 || line.DifferenceAmount != 0 {
		t.Errorf("line = %+v, want recorded=actual=100000, difference=0", line)
	}
	if line.Resolution != "matched" {
		t.Errorf("resolution = %q, want %q", line.Resolution, "matched")
	}
	if line.AdjustmentTransactionID != nil {
		t.Errorf("adjustment_transaction_id = %v, want nil - matched posts no fix", line.AdjustmentTransactionID)
	}

	// Reads back through GET /api/reconciliations...
	list := getReconciliations(t, r)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/reconciliations = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}
	var page reconciliationsPageResponse
	if err := json.NewDecoder(list.Body).Decode(&page); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(page.Reconciliations) != 1 || page.Reconciliations[0].ID != created.ID {
		t.Fatalf("list = %+v, want exactly the one snapshot just taken", page.Reconciliations)
	}
	if page.Reconciliations[0].OpenDifferenceAmount != 0 {
		t.Errorf("open_difference_amount = %d, want 0 (matched)", page.Reconciliations[0].OpenDifferenceAmount)
	}
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v, want nil (one snapshot, one page)", *page.NextCursor)
	}

	// ...and through GET /api/reconciliations/{id}.
	detailRec := getReconciliationDetail(t, r, created.ID)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET /api/reconciliations/{id} = %d, want %d (body: %s)", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}
	detail := decodeReconciliationDetail(t, detailRec)
	if detail.ID != created.ID || len(detail.Lines) != 1 || detail.Lines[0] != line {
		t.Errorf("detail = %+v, want it to match the create response exactly", detail)
	}

	// ...and through GET /api/reconciliations/latest.
	latestRec := getLatestReconciliation(t, r)
	if latestRec.Code != http.StatusOK {
		t.Fatalf("GET /api/reconciliations/latest = %d, want %d (body: %s)", latestRec.Code, http.StatusOK, latestRec.Body.String())
	}
	var latest reconciliationResponse
	if err := json.NewDecoder(latestRec.Body).Decode(&latest); err != nil {
		t.Fatalf("decoding latest: %v", err)
	}
	if latest.ID != created.ID {
		t.Errorf("latest.ID = %d, want %d", latest.ID, created.ID)
	}
}

// TestTakeReconciliationAdjustedStoresTheGapFoundNotZero is the trust-core
// regression this slice exists to re-assert at the HTTP layer: a fix posted
// as part of the same snapshot does NOT retroactively zero that snapshot's
// own recorded difference. If the cutoff were taken after the fix landed,
// difference_amount would be stored as 0 - schema-legal, and a permanent
// false record that no gap was ever found.
func TestTakeReconciliationAdjustedStoresTheGapFoundNotZero(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	ctx := t.Context()

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed transaction = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	note := "tin was short"
	takeRec := postReconciliation(t, r, takeReconciliationRequest{
		Counts: []accountCountRequest{{
			AccountID: setup.CashAccountID(t), ActualAmount: 80_000, Resolution: "adjusted",
			Fix: &fixRequest{
				PurposeID: setup.MainPurposeID, Direction: "out", Amount: 20_000,
				OccurredOn: "2026-08-31", Note: &note,
			},
		}},
	})
	if takeRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", takeRec.Code, http.StatusCreated, takeRec.Body.String())
	}
	created := decodeReconciliationDetail(t, takeRec)
	line := lineFor(t, created.Lines, setup.CashAccountID(t))

	if line.RecordedAmount != 100_000 {
		t.Errorf("recorded_amount = %d, want 100000 (before the fix)", line.RecordedAmount)
	}
	if line.ActualAmount != 80_000 {
		t.Errorf("actual_amount = %d, want 80000", line.ActualAmount)
	}
	// The regression assertion, straight off the create response.
	if line.DifferenceAmount != -20_000 {
		t.Fatalf("difference_amount = %d, want -20000 (the gap actually found, not 0)", line.DifferenceAmount)
	}
	if line.AdjustmentTransactionID == nil {
		t.Fatal("adjustment_transaction_id is nil, want the fix's transaction id")
	}

	// Re-fetched fresh through GET /api/reconciliations/{id}: still -20000,
	// never silently rewritten to 0 by a second read.
	detailRec := getReconciliationDetail(t, r, created.ID)
	detail := decodeReconciliationDetail(t, detailRec)
	againLine := lineFor(t, detail.Lines, setup.CashAccountID(t))
	if againLine.DifferenceAmount != -20_000 {
		t.Fatalf("difference_amount on re-read = %d, want -20000 (still the gap found, not zeroed on read-back)", againLine.DifferenceAmount)
	}

	// The fix itself really moved the ledger: cash is now 80000, verified fresh
	// off the ledger rather than trusted from the response alone.
	balance, err := l.AccountBalance(ctx, setup.Fund.ID, setup.CashAccountID(t))
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if balance != 80_000 {
		t.Errorf("AccountBalance() = %d, want 80000 (the fix posted)", balance.Int64())
	}
}

// TestTakeReconciliationBackdatedFixLandsInNextSnapshotNotThisOne is the
// other half of ADR-024's cutoff ordering, asserted through HTTP: a gap left
// open in one snapshot, later explained by an ordinary backdated entry posted
// through POST /api/transactions, does not retroactively rewrite the first
// snapshot's own numbers - it only counts toward the SECOND snapshot's
// recorded_amount, because its transaction id lands above the first
// snapshot's cutoff regardless of its calendar date.
func TestTakeReconciliationBackdatedFixLandsInNextSnapshotNotThisOne(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 300_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed in = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-05",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed out = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	firstRec := postReconciliation(t, r, takeReconciliationRequest{
		Counts: []accountCountRequest{
			{AccountID: setup.CashAccountID(t), ActualAmount: 240_000, Resolution: "left_open"},
		},
	})
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reconciliations (first) = %d, want %d (body: %s)", firstRec.Code, http.StatusCreated, firstRec.Body.String())
	}
	first := decodeReconciliationDetail(t, firstRec)
	firstLine := lineFor(t, first.Lines, setup.CashAccountID(t))
	if firstLine.RecordedAmount != 250_000 || firstLine.DifferenceAmount != -10_000 {
		t.Fatalf("first line = %+v, want recorded=250000 difference=-10000", firstLine)
	}

	// A week later the missing receipt turns up: a 10000 expense from the
	// 3rd, posted now as an adjustment - backdated in occurred_on, current in
	// id - through the ordinary transactions route, not as part of a
	// reconciliation call.
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 10_000, OccurredOn: "2026-08-03", IsAdjustment: true,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("late entry = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// The first snapshot's frozen line must not have moved.
	firstAgainRec := getReconciliationDetail(t, r, first.ID)
	firstAgain := decodeReconciliationDetail(t, firstAgainRec)
	firstLineAgain := lineFor(t, firstAgain.Lines, setup.CashAccountID(t))
	if firstLineAgain.RecordedAmount != 250_000 || firstLineAgain.DifferenceAmount != -10_000 {
		t.Errorf("first snapshot after the late entry = %+v, want unchanged recorded=250000 difference=-10000", firstLineAgain)
	}

	// Counted again; this time it agrees, because the late entry now counts
	// toward today's balance.
	secondRec := postReconciliation(t, r, takeReconciliationRequest{
		Counts: []accountCountRequest{
			{AccountID: setup.CashAccountID(t), ActualAmount: 240_000, Resolution: "matched"},
		},
	})
	if secondRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reconciliations (second) = %d, want %d (body: %s)", secondRec.Code, http.StatusCreated, secondRec.Body.String())
	}
	second := decodeReconciliationDetail(t, secondRec)
	secondLine := lineFor(t, second.Lines, setup.CashAccountID(t))
	if secondLine.RecordedAmount != 240_000 || secondLine.DifferenceAmount != 0 {
		t.Errorf("second snapshot = %+v, want recorded=240000 difference=0 - the late entry now counts", secondLine)
	}

	// The first snapshot's row is still on record - the second snapshot did
	// not rewrite history, it only added a new one - but the second snapshot
	// counted this same account and it matched, so the gap itself is no
	// longer open.
	open := getOpenReconciliationLines(t, r)
	var openLines []reconciliationLineResponse
	if err := json.NewDecoder(open.Body).Decode(&openLines); err != nil {
		t.Fatalf("decoding open lines: %v", err)
	}
	if len(openLines) != 0 {
		t.Errorf("open lines = %+v, want none - the second count superseded the first", openLines)
	}
}

// TestTakeReconciliationRejectsAnotherFundsAccount is the ruling from the
// issue: a cross-fund account_id trips reconciliation_line's composite
// foreign key, which #103 already taught mapLedgerError to route through
// mapSQLiteError as a 400 - not the 500 ADR-027's own "domain bug" language
// would otherwise suggest. No pre-check is added here for something the
// ledger's own schema already refuses (ADR-027).
func TestTakeReconciliationRejectsAnotherFundsAccount(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setUpFund(t, r)

	// A second fund, created directly through the store - the app itself
	// only ever exposes one (v1), so the only way to get a genuine
	// cross-fund account id is to reach past the HTTP surface for it.
	q := store.New(sqlDB)
	ctx := t.Context()
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

	rec := postReconciliation(t, r, takeReconciliationRequest{
		Counts: []accountCountRequest{
			{AccountID: otherAccount.ID, ActualAmount: 0, Resolution: "left_open"},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/reconciliations with another fund's account = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestGetReconciliationDetailOnAnotherFundsSnapshotIs404 is #105's standing
// context applied to this slice's own detail route: GetReconciliation now
// takes fund_id alongside id, so a real snapshot id belonging to another
// fund must read as not-found through THIS app's router rather than being
// fetched and only then rejected. The positive control - the same snapshot
// read through its own fund still resolving - is TestASecondFundsReconciliationIsInvisibleToTheFirstFund
// in internal/ledger, since v1's HTTP surface only ever resolves to the one
// fund setup created and so cannot address a second fund's router of its own.
func TestGetReconciliationDetailOnAnotherFundsSnapshotIs404(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setUpFund(t, r)

	q := store.New(sqlDB)
	ctx := t.Context()
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
	otherRec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: otherFund.ID, PerformedAt: 1, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation(other) = %v, want no error", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: otherFund.ID, ReconciliationID: otherRec.ID, AccountID: otherAccount.ID,
		RecordedAmount: 0, ActualAmount: 0, DifferenceAmount: 0, Resolution: "matched",
	}); err != nil {
		t.Fatalf("CreateReconciliationLine(other) = %v, want no error", err)
	}

	// The other fund's own snapshot id, read through this app's one
	// resolved fund, must be invisible.
	rec := getReconciliationDetail(t, r, otherRec.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/{other fund's id} = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestTakeReconciliationRejectsInvalidArgumentsBeforeAnyWrite covers the
// shape checks TakeReconciliation itself makes (ADR-027) reaching the client
// as 400 invalid_argument, and proves nothing was written: the list stays
// empty after every rejected call.
func TestTakeReconciliationRejectsInvalidArgumentsBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  takeReconciliationRequest
	}{
		{"empty counts", takeReconciliationRequest{Counts: []accountCountRequest{}}},
		{"unrecognised resolution", takeReconciliationRequest{
			Counts: []accountCountRequest{{AccountID: 1, ActualAmount: 0, Resolution: "sort-of"}},
		}},
		{"matched with a nonzero difference", takeReconciliationRequest{
			// The empty ledger has recorded_amount 0; claiming "matched"
			// against a nonzero actual_amount is a lying line.
			Counts: []accountCountRequest{{AccountID: 1, ActualAmount: 50_000, Resolution: "matched"}},
		}},
		{"adjusted with no fix", takeReconciliationRequest{
			Counts: []accountCountRequest{{AccountID: 1, ActualAmount: 50_000, Resolution: "adjusted"}},
		}},
		{"negative actual_amount", takeReconciliationRequest{
			Counts: []accountCountRequest{{AccountID: 1, ActualAmount: -1, Resolution: "left_open"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)
			setup := setUpFund(t, r)
			for i := range tc.req.Counts {
				if tc.req.Counts[i].AccountID == 1 {
					tc.req.Counts[i].AccountID = setup.CashAccountID(t)
				}
			}

			rec := postReconciliation(t, r, tc.req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "invalid_argument" {
				t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
			}

			// Nothing was written: the list is still empty.
			list := getReconciliations(t, r)
			var page reconciliationsPageResponse
			if err := json.NewDecoder(list.Body).Decode(&page); err != nil {
				t.Fatalf("decoding list: %v", err)
			}
			if len(page.Reconciliations) != 0 {
				t.Errorf("GET /api/reconciliations after a rejected call = %+v, want empty - nothing should have been written", page.Reconciliations)
			}
			if page.NextCursor != nil {
				t.Errorf("next_cursor = %v, want nil (empty fund)", *page.NextCursor)
			}
		})
	}
}

func TestPostReconciliationsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reconciliations", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/reconciliations with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetReconciliationDetailRejectsANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reconciliations/abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/reconciliations/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestGetReconciliationDetailOnAnUnknownIDIs404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getReconciliationDetail(t, r, 9_999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reconciliations/9999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetReconciliationOpenLinesReturnsLeftOpenLinesWithTheirSnapshot is the
// open-lines route doing its actual job, which the route-ordering test above
// only proves is reachable. A gap the treasurer chose to sleep on is only
// actionable if the answer says which count it was found in, so
// reconciliation_id is asserted here and not merely present on the type.
func TestGetReconciliationOpenLinesReturnsLeftOpenLinesWithTheirSnapshot(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 300_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// One count that agrees on the bank account and leaves a real gap open on
	// cash: only the second belongs in open-lines.
	takeRec := postReconciliation(t, r, takeReconciliationRequest{
		Counts: []accountCountRequest{
			{AccountID: setup.BankAccountID(t), ActualAmount: 0, Resolution: "matched"},
			{AccountID: setup.CashAccountID(t), ActualAmount: 285_000, Resolution: "left_open"},
		},
	})
	if takeRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", takeRec.Code, http.StatusCreated, takeRec.Body.String())
	}
	snapshot := decodeReconciliationDetail(t, takeRec)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reconciliations/open-lines", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/reconciliations/open-lines = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var open []openReconciliationLineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &open); err != nil {
		t.Fatalf("decoding open-lines = %v, want no error (body: %s)", err, rec.Body.String())
	}

	if len(open) != 1 {
		t.Fatalf("open-lines returned %d lines, want 1 - the matched line must not appear (body: %s)", len(open), rec.Body.String())
	}
	got := open[0]
	if got.Resolution != "left_open" {
		t.Errorf("Resolution = %q, want %q", got.Resolution, "left_open")
	}
	if got.AccountID != setup.CashAccountID(t) {
		t.Errorf("AccountID = %d, want the cash account %d", got.AccountID, setup.CashAccountID(t))
	}
	if got.ReconciliationID != snapshot.ID {
		t.Errorf("ReconciliationID = %d, want the snapshot it was found in, %d", got.ReconciliationID, snapshot.ID)
	}
	if got.DifferenceAmount != -15_000 {
		t.Errorf("DifferenceAmount = %d, want -15000 (285000 counted against 300000 recorded)", got.DifferenceAmount)
	}
}

// TestGetReconciliationsOrdersNewestFirstTiesBrokenByIDDesc mirrors the same
// test on GET /api/transactions and GET /api/reimbursements: two snapshots
// performed at the same instant are ordered by id, descending.
func TestGetReconciliationsOrdersNewestFirstTiesBrokenByIDDesc(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)

	q := store.New(sqlDB)
	ctx := context.Background()
	older, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: setup.Fund.ID, PerformedAt: 1000, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("seeding older: %v", err)
	}
	sameInstant, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: setup.Fund.ID, PerformedAt: 1000, CreatedAt: 2,
	})
	if err != nil {
		t.Fatalf("seeding sameInstant: %v", err)
	}
	newest, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: setup.Fund.ID, PerformedAt: 2000, CreatedAt: 3,
	})
	if err != nil {
		t.Fatalf("seeding newest: %v", err)
	}

	page := decodeReconciliationsPage(t, getReconciliations(t, r))
	wantOrder := []int64{newest.ID, sameInstant.ID, older.ID}
	if len(page.Reconciliations) != len(wantOrder) {
		t.Fatalf("GET /api/reconciliations returned %d rows, want %d", len(page.Reconciliations), len(wantOrder))
	}
	for i, want := range wantOrder {
		if page.Reconciliations[i].ID != want {
			t.Errorf("row %d = id %d, want %d (order %v)", i, page.Reconciliations[i].ID, want, wantOrder)
		}
	}
}

// TestGetReconciliationsPagingWalksTheWholeSetWithNoSkipOrDuplicate is
// #227's "paging walks the whole set with no skip and no duplicate"
// acceptance criterion, over 60 snapshots (more than two 25-row pages).
func TestGetReconciliationsPagingWalksTheWholeSetWithNoSkipOrDuplicate(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	seeded := seedReconciliations(t, sqlDB, setup.Fund.ID, 60, 1_000_000)

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
		rec := getReconciliationsWithQuery(t, r, rawQuery)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/reconciliations%s = %d, want %d (body: %s)", rawQuery, rec.Code, http.StatusOK, rec.Body.String())
		}
		page := decodeReconciliationsPage(t, rec)
		if pages < 2 && len(page.Reconciliations) != reconciliationsPageSize {
			t.Errorf("page %d = %d rows, want %d (60 rows over 25-row pages)", pages, len(page.Reconciliations), reconciliationsPageSize)
		}
		for _, row := range page.Reconciliations {
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

// TestGetReconciliationsPagingHandlesABackdatedInsertBetweenPageFetches
// mirrors transactions_test.go's and reimbursements_test.go's own test of
// the same name: a snapshot inserted after page 1 is fetched, performed
// earlier than everything on page 1 but inside page 2's range, must appear
// on page 2 exactly once - proving the keyset cursor (performed_at, id) is
// what actually runs rather than LIMIT/OFFSET.
func TestGetReconciliationsPagingHandlesABackdatedInsertBetweenPageFetches(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	// 30 snapshots performed_at 1000..1029 (oldest, seeded[0], through
	// newest, seeded[29]).
	seeded := seedReconciliations(t, sqlDB, setup.Fund.ID, 30, 1000)

	page1 := decodeReconciliationsPage(t, getReconciliations(t, r))
	if len(page1.Reconciliations) != reconciliationsPageSize {
		t.Fatalf("page 1 = %d rows, want %d", len(page1.Reconciliations), reconciliationsPageSize)
	}
	if page1.NextCursor == nil {
		t.Fatal("page 1 next_cursor = nil, want a cursor - 30 rows is more than one page")
	}

	// Lands strictly inside page 2's range (performed_at 1000..1004, the 5
	// rows page 1 did not cover) - earlier than every row already handed
	// out on page 1, and it did not exist when page 1 was fetched.
	q := store.New(sqlDB)
	backdated, err := q.CreateReconciliation(context.Background(), store.CreateReconciliationParams{
		FundID: setup.Fund.ID, PerformedAt: 1002, CreatedAt: 99_999,
	})
	if err != nil {
		t.Fatalf("inserting the backdated row: %v", err)
	}

	page2 := decodeReconciliationsPage(t, getReconciliationsWithQuery(t, r, "?cursor="+url.QueryEscape(*page1.NextCursor)))

	occurrences, seenIDs := 0, map[int64]bool{}
	for _, row := range page2.Reconciliations {
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
			t.Errorf("row %d (performed_at %d), originally on page 2, is missing after the backdated insert", want.ID, want.PerformedAt)
		}
	}
	if len(page2.Reconciliations) != 6 {
		t.Errorf("page 2 = %d rows, want 6 (the 5 original rows plus the backdated insert)", len(page2.Reconciliations))
	}
	if page2.NextCursor != nil {
		t.Errorf("page 2 next_cursor = %v, want nil - that was the last page", *page2.NextCursor)
	}
}

// TestGetReconciliationsRejectsAMalformedCursor is #227's "malformed cursor
// -> 400" acceptance criterion.
func TestGetReconciliationsRejectsAMalformedCursor(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	for _, cursor := range []string{"not-base64!!", "0|abc", "-5|1"} {
		rec := getReconciliationsWithQuery(t, r, "?cursor="+url.QueryEscape(cursor))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/reconciliations?cursor=%q = %d, want %d (body: %s)", cursor, rec.Code, http.StatusBadRequest, rec.Body.String())
			continue
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("cursor %q: error code = %q, want %q", cursor, got.Code, "invalid_argument")
		}
	}
}

// TestGetReconciliationsOnAnEmptyFundIsAnEmptyPage covers #227's "a fund
// that has never been reconciled" case at the API layer - the empty state
// Cek kas's own copy handles is driven by this shape, not an error.
func TestGetReconciliationsOnAnEmptyFundIsAnEmptyPage(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	page := decodeReconciliationsPage(t, getReconciliations(t, r))
	if len(page.Reconciliations) != 0 {
		t.Errorf("reconciliations = %+v, want empty - nothing has been counted yet", page.Reconciliations)
	}
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v, want nil", *page.NextCursor)
	}
}

// TestGetReconciliationsOpenDifferenceAmount covers #227's per-row
// open_difference_amount: 0 for a fully matched snapshot, 0 for one whose
// gap was fully resolved (entry_added/adjusted), and the absolute sum of
// every left_open line's difference for a mixed snapshot - including a
// negative difference, to prove the sum is over ABS() and not the signed
// total.
func TestGetReconciliationsOpenDifferenceAmount(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)
	q := store.New(sqlDB)
	ctx := context.Background()

	matched, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: setup.Fund.ID, PerformedAt: 1000, CreatedAt: 1000})
	if err != nil {
		t.Fatalf("seeding matched: %v", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: setup.Fund.ID, ReconciliationID: matched.ID, AccountID: setup.CashAccountID(t),
		RecordedAmount: 100_000, ActualAmount: 100_000, DifferenceAmount: 0, Resolution: "matched",
	}); err != nil {
		t.Fatalf("seeding matched line: %v", err)
	}

	resolved, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: setup.Fund.ID, PerformedAt: 2000, CreatedAt: 2000})
	if err != nil {
		t.Fatalf("seeding resolved: %v", err)
	}
	fixTx, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 5_000, OccurredOn: "2026-08-01", Kind: "adjustment", CreatedAt: 2000,
	})
	if err != nil {
		t.Fatalf("seeding the fix transaction: %v", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: setup.Fund.ID, ReconciliationID: resolved.ID, AccountID: setup.CashAccountID(t),
		RecordedAmount: 95_000, ActualAmount: 100_000, DifferenceAmount: 5_000,
		Resolution: "adjusted", AdjustmentTransactionID: &fixTx.ID,
	}); err != nil {
		t.Fatalf("seeding adjusted line: %v", err)
	}

	mixed, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: setup.Fund.ID, PerformedAt: 3000, CreatedAt: 3000})
	if err != nil {
		t.Fatalf("seeding mixed: %v", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: setup.Fund.ID, ReconciliationID: mixed.ID, AccountID: setup.CashAccountID(t),
		RecordedAmount: 100_000, ActualAmount: 85_000, DifferenceAmount: -15_000, Resolution: "left_open",
	}); err != nil {
		t.Fatalf("seeding mixed left_open line (negative difference): %v", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: setup.Fund.ID, ReconciliationID: mixed.ID, AccountID: setup.BankAccountID(t),
		RecordedAmount: 50_000, ActualAmount: 56_000, DifferenceAmount: 6_000, Resolution: "left_open",
	}); err != nil {
		t.Fatalf("seeding mixed left_open line (positive difference): %v", err)
	}

	page := decodeReconciliationsPage(t, getReconciliations(t, r))
	byID := map[int64]int64{}
	for _, row := range page.Reconciliations {
		byID[row.ID] = row.OpenDifferenceAmount
	}
	if got := byID[matched.ID]; got != 0 {
		t.Errorf("matched.OpenDifferenceAmount = %d, want 0", got)
	}
	if got := byID[resolved.ID]; got != 0 {
		t.Errorf("resolved (adjusted).OpenDifferenceAmount = %d, want 0", got)
	}
	if got := byID[mixed.ID]; got != 21_000 {
		t.Errorf("mixed.OpenDifferenceAmount = %d, want 21000 (abs(-15000) + abs(6000))", got)
	}
}
