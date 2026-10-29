package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// #738: one row per movement. A transfer is two ledger rows but one listed row,
// picked in SQL after the filters and before the cursor. These tests hold the
// list to that from every side - the filters, the page boundary, the cursor
// walk - and hold the ledger itself still: nothing here changes a balance.

// oneRowWorld is purposeMoveWorld with the database kept, so a test can seed
// filler rows around a pair in a chosen id order and read the raw legs back.
type oneRowWorld struct {
	purposeMoveWorld
	sqlDB *sql.DB
	r     http.Handler
}

func newOneRowWorld(t *testing.T) oneRowWorld {
	t.Helper()
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	return oneRowWorld{purposeMoveWorld: newPurposeMoveWorld(t, r), sqlDB: sqlDB, r: r}
}

// fill writes n ordinary 'in' rows on date straight through the store, tagged
// to purposeID, in ascending id order, and returns their ids. Called before and
// after a pair is posted, it decides which side of the pair's legs they sit on.
func (w oneRowWorld) fill(t *testing.T, n int, date string, purposeID int64) []int64 {
	t.Helper()
	q := store.New(w.sqlDB)
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		row, err := q.CreateTransaction(context.Background(), store.CreateTransactionParams{
			FundID: w.setup.Fund.ID, AccountID: w.setup.CashAccountID(t), PurposeID: purposeID,
			Direction: "in", Amount: int64(1_000 + i), OccurredOn: date, Kind: "normal", CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("filling row %d: %v", i, err)
		}
		ids = append(ids, row.ID)
	}
	return ids
}

// move posts a Kas Utama -> envelope purpose move on date and returns its transfer id.
func (w oneRowWorld) move(t *testing.T, date string, amount int64) int64 {
	t.Helper()
	req := w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, amount)
	req.OccurredOn = date
	rec := postPurposeMove(t, w.r, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/purpose-moves = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return decodeTransfer(t, rec).ID
}

func decodeTransfer(t *testing.T, rec *httptest.ResponseRecorder) transferResponse {
	t.Helper()
	var tr transferResponse
	if err := json.NewDecoder(rec.Body).Decode(&tr); err != nil {
		t.Fatalf("decoding transfer response: %v", err)
	}
	return tr
}

// walk follows next_cursor from the first page of query to the end and returns
// every row in the order served, failing on a runaway walk.
func (w oneRowWorld) walk(t *testing.T, query string) (rows []transactionResponse, pages int) {
	t.Helper()
	cursor := ""
	for ; ; pages++ {
		if pages > 20 {
			t.Fatal("more than 20 pages fetched - paging did not terminate")
		}
		q := query
		if cursor != "" {
			if q != "" {
				q += "&"
			}
			q += "cursor=" + url.QueryEscape(cursor)
		}
		rec := getTransactionsQuery(t, w.r, q)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/transactions?%s = %d, want %d (body: %s)", q, rec.Code, http.StatusOK, rec.Body.String())
		}
		page := decodeTransactionsPage(t, rec)
		rows = append(rows, page.Transactions...)
		if page.NextCursor == nil {
			return rows, pages + 1
		}
		cursor = *page.NextCursor
	}
}

// assertTransferOnce fails unless exactly one row in rows belongs to transferID,
// and returns it.
func assertTransferOnce(t *testing.T, rows []transactionResponse, transferID int64) transactionResponse {
	t.Helper()
	var found []transactionResponse
	for _, row := range rows {
		if row.TransferID != nil && *row.TransferID == transferID {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		t.Fatalf("rows for transfer %d = %d, want exactly 1 (rows: %+v)", transferID, len(found), found)
	}
	return found[0]
}

func countTransferRows(rows []transactionResponse) int {
	n := 0
	for _, row := range rows {
		if row.Kind == "transfer" {
			n++
		}
	}
	return n
}

func TestListShowsEachTransferOnce(t *testing.T) {
	t.Parallel()
	w := newOneRowWorld(t)

	location := postTransfer(t, w.r, transferRequest{
		PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.CashAccountID(t), ToAccountID: w.setup.BankAccountID(t),
		Amount: 100_000, OccurredOn: "2026-09-02",
	})
	if location.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d (body: %s)", location.Code, location.Body.String())
	}
	locationID := decodeTransfer(t, location).ID
	moveID := w.move(t, "2026-09-03", 20_000)

	rows, _ := w.walk(t, "")
	loc := assertTransferOnce(t, rows, locationID)
	mv := assertTransferOnce(t, rows, moveID)

	// The representative is the 'out' leg: the transfer's from side.
	if loc.Direction != "out" || loc.AccountID != w.setup.CashAccountID(t) {
		t.Errorf("location transfer row = direction %q account %d, want the out leg on cash %d", loc.Direction, loc.AccountID, w.setup.CashAccountID(t))
	}
	if mv.Direction != "out" || mv.PurposeID != w.setup.MainPurposeID {
		t.Errorf("purpose move row = direction %q purpose %d, want the out leg on Kas Utama %d", mv.Direction, mv.PurposeID, w.setup.MainPurposeID)
	}
	// Whichever leg it is, the names say the movement.
	if mv.TransferFromName == nil || *mv.TransferFromName != "Kas Utama" || mv.TransferToName == nil || *mv.TransferToName != "Bereavement" {
		t.Errorf("purpose move names = %v -> %v, want Kas Utama -> Bereavement", mv.TransferFromName, mv.TransferToName)
	}
}

// A filter is applied to every leg before the pair collapses, so filtering by
// either leg's pos finds the movement - and returns it once.
func TestListFilterByEitherLegsPurposeFindsTheTransferOnce(t *testing.T) {
	t.Parallel()
	w := newOneRowWorld(t)
	moveID := w.move(t, "2026-09-10", 120_000)

	cases := []struct {
		name      string
		purposeID int64
		wantRow   bool
		wantDir   string
	}{
		{"the out leg's pos (Kas Utama)", w.setup.MainPurposeID, true, "out"},
		{"the in leg's pos (the envelope)", w.envelope.PurposeID, true, "in"},
		{"a pos neither leg is tagged to", w.titipan.ID, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, _ := w.walk(t, "purpose_id="+strconv.FormatInt(c.purposeID, 10))
			if !c.wantRow {
				if n := countTransferRows(rows); n != 0 {
					t.Fatalf("transfer rows = %d, want 0 for a pos neither leg carries", n)
				}
				return
			}
			row := assertTransferOnce(t, rows, moveID)
			if row.Direction != c.wantDir {
				t.Errorf("representative leg direction = %q, want %q (the only leg that matched, or the out leg)", row.Direction, c.wantDir)
			}
			// Even when the in leg is the row, it names the movement from to.
			if row.TransferFromName == nil || *row.TransferFromName != "Kas Utama" || row.TransferToName == nil || *row.TransferToName != "Bereavement" {
				t.Errorf("names = %v -> %v, want Kas Utama -> Bereavement", row.TransferFromName, row.TransferToName)
			}
		})
	}

	// The same through the text search: the envelope's name is only on the in leg.
	rows, _ := w.walk(t, "q="+url.QueryEscape("bereavement"))
	assertTransferOnce(t, rows, moveID)
}

// A transfer is neither money in nor money out, so the Masuk/Keluar filter
// excludes it whichever leg would have matched.
func TestListDirectionFilterExcludesTransfers(t *testing.T) {
	t.Parallel()
	w := newOneRowWorld(t)
	moveID := w.move(t, "2026-09-10", 50_000)
	if rec := postTransaction(t, w.r, transactionRequest{
		AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID, Direction: "out", Amount: 7_000, OccurredOn: "2026-09-10",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postTransfer(t, w.r, transferRequest{
		PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.CashAccountID(t), ToAccountID: w.setup.BankAccountID(t),
		Amount: 10_000, OccurredOn: "2026-09-10",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d (body: %s)", rec.Code, rec.Body.String())
	}

	for _, direction := range []string{"in", "out"} {
		rows, _ := w.walk(t, "direction="+direction)
		if n := countTransferRows(rows); n != 0 {
			t.Errorf("direction=%s lists %d transfer rows, want 0", direction, n)
		}
		for _, row := range rows {
			if row.Direction != direction {
				t.Errorf("direction=%s lists a %q row (id %d)", direction, row.Direction, row.ID)
			}
		}
		if len(rows) == 0 {
			t.Errorf("direction=%s lists nothing, want the ordinary rows of that direction", direction)
		}
	}
	// Without the filter the movement is there, so the exclusion is the filter's doing.
	rows, _ := w.walk(t, "")
	assertTransferOnce(t, rows, moveID)
}

// The picked leg carries the other leg's receipts too, so a photo on a leg the
// list did not pick is not lost.
func TestListFilteredToTheInLegStillCarriesTheOutLegsReceipt(t *testing.T) {
	t.Parallel()
	w := newOneRowWorld(t)
	moveID := w.move(t, "2026-09-10", 120_000)

	var outLeg store.Transaction
	for _, leg := range ledgerLegs(t, w.sqlDB, w.setup.Fund.ID, moveID) {
		if leg.Direction == "out" {
			outLeg = leg
		}
	}
	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))
	rec := postReceiptFile(t, w.r, "/api/transactions/"+strconv.FormatInt(outLeg.ID, 10)+"/receipts", fixture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d (body: %s)", rec.Code, rec.Body.String())
	}
	receiptID := decodeReceiptResponse(t, rec).ID

	rows, _ := w.walk(t, "purpose_id="+strconv.FormatInt(w.envelope.PurposeID, 10))
	row := assertTransferOnce(t, rows, moveID)
	if row.Direction != "in" {
		t.Fatalf("representative direction = %q, want the in leg (only it matches the envelope)", row.Direction)
	}
	if !slices.Equal(row.ReceiptIDs, []int64{receiptID}) {
		t.Errorf("receipt_ids = %v, want [%d] from the out leg", row.ReceiptIDs, receiptID)
	}
}

// The page boundary. A pair sits on one date among rows of that date; `after`
// rows were written after it, so they list before it. Sliding `after` across
// the page size puts the boundary on either side of, and between, the pair's
// two legs: a naive per-leg page would end on the in leg and begin the next on
// the out leg. Every alignment must show the pair once and skip nothing.
func TestListPageBoundaryNeverSplitsOrSkipsAPair(t *testing.T) {
	t.Parallel()
	for after := transactionsPageSize - 3; after <= transactionsPageSize+2; after++ {
		t.Run(fmt.Sprintf("%d rows listed before the pair", after), func(t *testing.T) {
			t.Parallel()
			w := newOneRowWorld(t)
			const date = "2026-09-10"
			before := w.fill(t, 4, date, w.setup.MainPurposeID)
			moveID := w.move(t, date, 5_000)
			later := w.fill(t, after, date, w.setup.MainPurposeID)

			rows, pages := w.walk(t, "")
			assertTransferOnce(t, rows, moveID)

			// Everything the list owes: every ordinary row once, the pair once, in
			// (occurred_on, id) order, and no row twice. The world seeded two deposits.
			if want := 2 + len(before) + len(later) + 1; len(rows) != want {
				t.Errorf("walked %d rows over %d pages, want %d", len(rows), pages, want)
			}
			assertEachMovementOnceInOrder(t, w, rows, nil)
		})
	}
}

// The same boundary when the filter leaves only the in leg of the pair: the
// representative is then the in leg, the one leg the list can show.
func TestListPageBoundaryHoldsWhenOnlyTheInLegMatches(t *testing.T) {
	t.Parallel()
	for after := transactionsPageSize - 2; after <= transactionsPageSize+1; after++ {
		t.Run(fmt.Sprintf("%d envelope rows listed before the pair", after), func(t *testing.T) {
			t.Parallel()
			w := newOneRowWorld(t)
			const date = "2026-09-10"
			before := w.fill(t, 3, date, w.envelope.PurposeID)
			moveID := w.move(t, date, 5_000)
			later := w.fill(t, after, date, w.envelope.PurposeID)

			rows, _ := w.walk(t, "purpose_id="+strconv.FormatInt(w.envelope.PurposeID, 10))
			row := assertTransferOnce(t, rows, moveID)
			if row.Direction != "in" {
				t.Errorf("representative direction = %q, want the in leg", row.Direction)
			}
			if want := len(before) + len(later) + 1; len(rows) != want {
				t.Errorf("walked %d rows, want %d", len(rows), want)
			}
			assertEachMovementOnceInOrder(t, w, rows, func(row store.Transaction) bool { return row.PurposeID == w.envelope.PurposeID })
		})
	}
}

// The cursor walk over a ledger that mixes every kind of row across many dates
// returns each movement exactly once, newest first, and the ledger underneath
// is as it was: the pairs still net to zero and the fund total is the ordinary
// rows' sum, so listing one row changed no balance.
func TestListCursorWalkReturnsEveryMovementExactlyOnce(t *testing.T) {
	t.Parallel()
	w := newOneRowWorld(t)
	dates := []string{"2026-09-02", "2026-09-05", "2026-09-05", "2026-09-09", "2026-09-12"}

	var transfers []int64
	for i := 0; i < 70; i++ {
		date := dates[i%len(dates)]
		switch i % 7 {
		case 3:
			transfers = append(transfers, w.move(t, date, int64(1_000+i)))
		case 5:
			rec := postTransfer(t, w.r, transferRequest{
				PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.CashAccountID(t), ToAccountID: w.setup.BankAccountID(t),
				Amount: int64(2_000 + i), OccurredOn: date,
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/transfers = %d (body: %s)", rec.Code, rec.Body.String())
			}
			transfers = append(transfers, decodeTransfer(t, rec).ID)
		case 6:
			// An adjusting entry is an ordinary row for the list: no transfer, never collapsed.
			rec := postTransaction(t, w.r, transactionRequest{
				AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID,
				Direction: "out", Amount: int64(500 + i), OccurredOn: date, IsAdjustment: true,
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/transactions (adjustment) = %d (body: %s)", rec.Code, rec.Body.String())
			}
		default:
			w.fill(t, 1, date, w.setup.MainPurposeID)
		}
	}

	rows, pages := w.walk(t, "")
	if pages < 3 {
		t.Fatalf("walk took %d pages, want at least 3 to cross page boundaries", pages)
	}
	for _, id := range transfers {
		assertTransferOnce(t, rows, id)
	}
	assertEachMovementOnceInOrder(t, w, rows, nil)

	// The ledger is untouched: listing is display only.
	all, err := store.New(w.sqlDB).ListTransactionsByFund(context.Background(), w.setup.Fund.ID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v", err)
	}
	var total int64
	legsPerTransfer := map[int64]int{}
	for _, row := range all {
		if row.TransferID != nil {
			legsPerTransfer[*row.TransferID]++
		}
		if row.Direction == "in" {
			total += row.Amount
		} else {
			total -= row.Amount
		}
	}
	for id, n := range legsPerTransfer {
		if n != 2 {
			t.Errorf("transfer %d has %d ledger legs, want 2 (the list hides one, the ledger keeps both)", id, n)
		}
	}
	if got := decodeBalances(t, getBalances(t, w.r)).FundTotal; got != total {
		t.Errorf("fund_total = %d, want %d (the sum of every ledger row)", got, total)
	}
}

// assertEachMovementOnceInOrder holds a walked list to the whole contract: the
// movements the ledger owes a list - every ordinary row, and one row per
// transfer - each appear exactly once, newest first by (occurred_on, id).
//
// matches says which ledger rows the walked query selects (nil: all of them); a
// transfer is owed when any of its legs matches.
func assertEachMovementOnceInOrder(t *testing.T, w oneRowWorld, rows []transactionResponse, matches func(store.Transaction) bool) {
	t.Helper()
	everything, err := store.New(w.sqlDB).ListTransactionsByFund(context.Background(), w.setup.Fund.ID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v", err)
	}
	if matches == nil {
		matches = func(store.Transaction) bool { return true }
	}
	var all []store.Transaction
	for _, row := range everything {
		if matches(row) {
			all = append(all, row)
		}
	}

	// Which ledger rows each listed row may stand for.
	legsOf := map[int64][]int64{} // transfer id -> leg ids
	ordinary := map[int64]bool{}
	for _, row := range all {
		if row.TransferID != nil {
			legsOf[*row.TransferID] = append(legsOf[*row.TransferID], row.ID)
		} else {
			ordinary[row.ID] = true
		}
	}

	seenOrdinary := map[int64]int{}
	seenTransfer := map[int64]int{}
	for _, row := range rows {
		if row.TransferID != nil {
			seenTransfer[*row.TransferID]++
			if !slices.Contains(legsOf[*row.TransferID], row.ID) {
				t.Errorf("row %d is listed for transfer %d but is not one of its legs %v", row.ID, *row.TransferID, legsOf[*row.TransferID])
			}
			continue
		}
		seenOrdinary[row.ID]++
	}
	for id := range ordinary {
		if seenOrdinary[id] != 1 {
			t.Errorf("ordinary row %d listed %d times, want exactly 1", id, seenOrdinary[id])
		}
	}
	for id := range seenOrdinary {
		if !ordinary[id] {
			t.Errorf("listed row %d is not an ordinary ledger row", id)
		}
	}
	for id := range legsOf {
		if seenTransfer[id] != 1 {
			t.Errorf("transfer %d listed %d times, want exactly 1", id, seenTransfer[id])
		}
	}

	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		if prev.OccurredOn < cur.OccurredOn || (prev.OccurredOn == cur.OccurredOn && prev.ID <= cur.ID) {
			t.Fatalf("rows %d (%s, id %d) then %d (%s, id %d): not newest-first by (occurred_on, id)",
				i-1, prev.OccurredOn, prev.ID, i, cur.OccurredOn, cur.ID)
		}
	}
}
