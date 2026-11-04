package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// ADR-048: GET /api/transactions lists a dues payment as ONE item, and a dues
// reversal group as ONE item, grouped in the query before the cursor cuts the
// page. These tests hold the list to that from every side - the shape of an
// item, the page boundary, the filters, receipts - and hold the ledger still:
// nothing here changes a balance.

type duesGroupWorld struct {
	r     http.Handler
	sqlDB *sql.DB
	setup setupResponse
	ani   memberResponse
	budi  memberResponse
}

func newDuesGroupWorld(t *testing.T) duesGroupWorld {
	t.Helper()
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	w := duesGroupWorld{r: r, sqlDB: sqlDB, setup: setUpFund(t, r)}
	mk := func(name string) memberResponse {
		var m memberResponse
		if err := json.NewDecoder(postMember(t, r, memberRequest{Name: name}).Body).Decode(&m); err != nil {
			t.Fatalf("decoding member %s: %v", name, err)
		}
		return m
	}
	w.ani, w.budi = mk("Ani"), mk("Budi")
	return w
}

// pay posts one dues payment for member on date, amount a month, the periods in
// the order given, and returns the rows posted in that order.
func (w duesGroupWorld) pay(t *testing.T, member memberResponse, on string, amount int64, periods ...string) []transactionResponse {
	t.Helper()
	req := duesPaymentRequest{AccountID: w.setup.CashAccountID(t), MemberID: member.ID, OccurredOn: on}
	for _, p := range periods {
		req.Periods = append(req.Periods, duesPaymentPeriod{DuesPeriod: p, Amount: amount})
	}
	rec := postDuesPayment(t, w.r, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted []transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding dues payment response: %v", err)
	}
	return posted
}

// list follows next_cursor from the first page of query to the end.
func (w duesGroupWorld) list(t *testing.T, query string) []transactionResponse {
	t.Helper()
	var rows []transactionResponse
	cursor := ""
	for pages := 0; ; pages++ {
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
			return rows
		}
		cursor = *page.NextCursor
	}
}

func (w duesGroupWorld) reverse(t *testing.T, id int64) {
	t.Helper()
	if rec := postReversal(t, w.r, id, reverseTransactionRequest{}); rec.Code != http.StatusCreated {
		t.Fatalf("POST reversal of %d = %d, want %d (body: %s)", id, rec.Code, http.StatusCreated, rec.Body.String())
	}
}

// legacyReversal writes the reversal of ONE dues row straight through the store,
// the way data from before ADR-048 holds one: a payment reversed month by month.
func (w duesGroupWorld) legacyReversal(t *testing.T, original transactionResponse, on string) int64 {
	t.Helper()
	originalID := original.ID
	row, err := store.New(w.sqlDB).CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: w.setup.Fund.ID, AccountID: original.AccountID, PurposeID: original.PurposeID,
		Direction: "out", Amount: original.Amount, OccurredOn: on, Kind: "adjustment",
		MemberID: original.MemberID, DuesPeriod: original.DuesPeriod, ReversesTransactionID: &originalID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("writing the legacy reversal of %d: %v", original.ID, err)
	}
	return row.ID
}

func itemsOfKind(rows []transactionResponse, kind string) []transactionResponse {
	var out []transactionResponse
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func periodsOf(item transactionResponse) []string {
	out := make([]string, 0, len(item.Periods))
	for _, p := range item.Periods {
		out = append(out, p.DuesPeriod)
	}
	return out
}

func TestListShowsAMultiMonthPaymentAsOneItem(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)

	// Posted out of order, so the anchor (the earliest month) is not the first row
	// posted and not the lowest id: nothing may lean on id order.
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-08", "2026-06", "2026-07")
	var june transactionResponse
	for _, p := range posted {
		if *p.DuesPeriod == "2026-06" {
			june = p
		}
	}

	rows := w.list(t, "")
	dues := itemsOfKind(rows, "dues")
	if len(dues) != 1 {
		t.Fatalf("list holds %d dues items, want 1 for a three-month payment (rows: %+v)", len(dues), rows)
	}
	item := dues[0]
	if item.Amount != 75_000 {
		t.Errorf("Amount = %d, want 75000, the sum of the months", item.Amount)
	}
	if item.ID != june.ID {
		t.Errorf("ID = %d, want the anchor row, the June row %d", item.ID, june.ID)
	}
	if want := []string{"2026-06", "2026-07", "2026-08"}; !slices.Equal(periodsOf(item), want) {
		t.Errorf("periods = %v, want %v, oldest first", periodsOf(item), want)
	}
	for _, p := range item.Periods {
		if p.Amount != 25_000 || p.Reversed {
			t.Errorf("period %+v, want 25000 and standing", p)
		}
	}
	if item.DuesPaymentID == nil || posted[0].DuesPaymentID == nil || *item.DuesPaymentID != *posted[0].DuesPaymentID {
		t.Errorf("DuesPaymentID = %v, want the payment's %v", item.DuesPaymentID, posted[0].DuesPaymentID)
	}
	if item.ReversalState != "none" || item.Reversed || !item.Reversible {
		t.Errorf("state %q reversed %v reversible %v, want none false true", item.ReversalState, item.Reversed, item.Reversible)
	}
	if item.MemberName == nil || *item.MemberName != "Ani" || item.OccurredOn != "2026-08-12" {
		t.Errorf("member %v on %s, want Ani on 2026-08-12", item.MemberName, item.OccurredOn)
	}
}

func TestListShowsASingleMonthPaymentAsAGroupOfOne(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-08")

	dues := itemsOfKind(w.list(t, ""), "dues")
	if len(dues) != 1 {
		t.Fatalf("list holds %d dues items, want 1", len(dues))
	}
	item := dues[0]
	if item.ID != posted[0].ID || item.Amount != 25_000 || len(item.Periods) != 1 || item.Periods[0].DuesPeriod != "2026-08" {
		t.Errorf("item = id %d amount %d periods %+v, want the row itself with one period", item.ID, item.Amount, item.Periods)
	}
	if item.DuesPaymentID == nil {
		t.Error("DuesPaymentID = nil, want the payment's, a single month included")
	}
}

func TestListKeepsTwoSameDayPaymentsApart(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	// Same member, same day, same location, same note: only the record tells them apart.
	a := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-06", "2026-07")
	b := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-08", "2026-09")

	dues := itemsOfKind(w.list(t, ""), "dues")
	if len(dues) != 2 {
		t.Fatalf("list holds %d dues items, want 2 payments (items: %+v)", len(dues), dues)
	}
	seen := map[int64][]string{}
	for _, d := range dues {
		if d.Amount != 50_000 {
			t.Errorf("item amount = %d, want 50000", d.Amount)
		}
		seen[*d.DuesPaymentID] = periodsOf(d)
	}
	if !slices.Equal(seen[*a[0].DuesPaymentID], []string{"2026-06", "2026-07"}) || !slices.Equal(seen[*b[0].DuesPaymentID], []string{"2026-08", "2026-09"}) {
		t.Errorf("periods by payment = %v, want each payment its own months", seen)
	}
}

func TestListShowsAReversalGroupAsOneItem(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-08", "2026-06", "2026-07")
	var june transactionResponse
	for _, p := range posted {
		if *p.DuesPeriod == "2026-06" {
			june = p
		}
	}
	// Reversing from the August row reverses the whole payment.
	w.reverse(t, posted[0].ID)

	rows := w.list(t, "")
	if len(rows) != 2 {
		t.Fatalf("list holds %d items, want 2: the payment and its reversal group (rows: %+v)", len(rows), rows)
	}
	payment, reversal := itemsOfKind(rows, "dues")[0], itemsOfKind(rows, "adjustment")[0]

	if payment.ReversalState != "all" || !payment.Reversed || payment.Reversible {
		t.Errorf("payment state %q reversed %v reversible %v, want all true false", payment.ReversalState, payment.Reversed, payment.Reversible)
	}
	for _, p := range payment.Periods {
		if !p.Reversed {
			t.Errorf("period %+v of a fully reversed payment, want reversed", p)
		}
	}
	if reversal.Amount != 75_000 || !slices.Equal(periodsOf(reversal), []string{"2026-06", "2026-07", "2026-08"}) {
		t.Errorf("reversal group = amount %d periods %v, want 75000 and all three months", reversal.Amount, periodsOf(reversal))
	}
	if reversal.DuesPaymentID == nil || *reversal.DuesPaymentID != *posted[0].DuesPaymentID {
		t.Errorf("reversal group DuesPaymentID = %v, want the reversed payment's %d", reversal.DuesPaymentID, *posted[0].DuesPaymentID)
	}
	if reversal.ReversesTransactionID == nil || *reversal.ReversesTransactionID != june.ID {
		t.Errorf("reversal group ReversesTransactionID = %v, want its anchor's original, the June row %d", reversal.ReversesTransactionID, june.ID)
	}
	if reversal.Direction != "out" || reversal.Reversible || reversal.Reversed || reversal.ReversalState != "none" {
		t.Errorf("reversal group direction %q reversible %v reversed %v state %q, want out false false none",
			reversal.Direction, reversal.Reversible, reversal.Reversed, reversal.ReversalState)
	}
	for _, p := range reversal.Periods {
		if p.Reversed {
			t.Errorf("reversal group period %+v, want never reversed", p)
		}
	}
}

// A payment reversed month by month (data from before ADR-048) reads partial and
// can still be reversed; batalkan takes the rest, and the two reversals dated
// alike read as one group, which is true.
func TestListShowsAPartlyReversedPaymentHonestly(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	var july transactionResponse
	for _, p := range posted {
		if *p.DuesPeriod == "2026-07" {
			july = p
		}
	}
	w.legacyReversal(t, july, "2026-08-12")

	rows := w.list(t, "")
	if len(rows) != 2 {
		t.Fatalf("list holds %d items, want the payment and the one-month reversal (rows: %+v)", len(rows), rows)
	}
	payment, reversal := itemsOfKind(rows, "dues")[0], itemsOfKind(rows, "adjustment")[0]
	if payment.ReversalState != "partial" || payment.Reversed || !payment.Reversible {
		t.Errorf("payment state %q reversed %v reversible %v, want partial false true", payment.ReversalState, payment.Reversed, payment.Reversible)
	}
	if payment.Amount != 75_000 || len(payment.Periods) != 3 {
		t.Errorf("payment amount %d periods %d, want the whole 75000 over 3 months", payment.Amount, len(payment.Periods))
	}
	for _, p := range payment.Periods {
		if p.Reversed != (p.DuesPeriod == "2026-07") {
			t.Errorf("period %+v, want only July reversed", p)
		}
	}
	if reversal.Amount != 25_000 || !slices.Equal(periodsOf(reversal), []string{"2026-07"}) {
		t.Errorf("reversal = amount %d periods %v, want 25000 and July only", reversal.Amount, periodsOf(reversal))
	}

	// Batalkan takes the rest.
	w.reverse(t, payment.ID)
	rows = w.list(t, "")
	payment = itemsOfKind(rows, "dues")[0]
	if payment.ReversalState != "all" || payment.Reversible {
		t.Errorf("after batalkan: state %q reversible %v, want all false", payment.ReversalState, payment.Reversible)
	}
	reversals := itemsOfKind(rows, "adjustment")
	if len(reversals) != 1 || reversals[0].Amount != 75_000 || len(reversals[0].Periods) != 3 {
		t.Errorf("after batalkan: reversal items %+v, want one group of the three months (same payment, same day)", reversals)
	}
}

// The page boundary can never split a group, repeat one or omit one. The HTTP
// page is 25, so the walk below sits a payment exactly on that edge; the second
// test pushes the same query through a page of two.
func TestAPaymentOnThePageBoundaryIsWholeAndOnceOnly(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	// 23 older plain rows, then a three-month payment, then a newer plain row:
	// the payment is item 2, 24 and 25 are plain rows, and one more row follows.
	fill := oneRowWorld{purposeMoveWorld: purposeMoveWorld{setup: w.setup}, sqlDB: w.sqlDB, r: w.r}
	fill.fill(t, 23, "2026-07-01", w.setup.MainPurposeID)
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	fill.fill(t, 2, "2026-09-01", w.setup.MainPurposeID)
	fill.fill(t, 1, "2026-06-01", w.setup.MainPurposeID)

	page1 := decodeTransactionsPage(t, getTransactionsQuery(t, w.r, ""))
	if len(page1.Transactions) != 25 || page1.NextCursor == nil {
		t.Fatalf("page 1 holds %d items, next %v, want 25 and a next page", len(page1.Transactions), page1.NextCursor)
	}
	all := w.list(t, "")
	if len(all) != 2+1+23+1 {
		t.Fatalf("walk holds %d items, want 27: 26 plain rows and one payment", len(all))
	}
	dues := itemsOfKind(all, "dues")
	if len(dues) != 1 || dues[0].Amount != 75_000 || len(dues[0].Periods) != 3 || *dues[0].DuesPaymentID != *posted[0].DuesPaymentID {
		t.Errorf("payment items = %+v, want exactly one, whole", dues)
	}
	ids := map[int64]bool{}
	for _, r := range all {
		if ids[r.ID] {
			t.Errorf("item %d appears twice across the pages", r.ID)
		}
		ids[r.ID] = true
	}
}

func TestAGroupStraddlingASmallPageIsNeverSplit(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	fill := oneRowWorld{purposeMoveWorld: purposeMoveWorld{setup: w.setup}, sqlDB: w.sqlDB, r: w.r}
	// The newest-first order, all on distinct dates so only the anchor decides a
	// group's place: plain (Sep 3), a three-month payment (Aug 12), plain (Aug 1),
	// a two-month payment (Jul 20) reversed whole on its own day, plain (Jun 1).
	fill.fill(t, 1, "2026-09-03", w.setup.MainPurposeID)
	w.pay(t, w.ani, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	fill.fill(t, 1, "2026-08-01", w.setup.MainPurposeID)
	two := w.pay(t, w.budi, "2026-07-20", 10_000, "2026-05", "2026-06")
	w.reverse(t, two[0].ID)
	fill.fill(t, 1, "2026-06-01", w.setup.MainPurposeID)

	// The item sequence a page of any size must reproduce.
	want := w.list(t, "")
	if len(want) != 6 {
		t.Fatalf("full list holds %d items, want 6: 3 plain, 2 payments, 1 reversal group (items: %+v)", len(want), want)
	}

	q := store.New(w.sqlDB)
	for _, size := range []int64{1, 2, 3, 4} {
		var got []store.ListTransactionsPageRow
		var cursorOn interface{}
		var cursorID *int64
		for pages := 0; ; pages++ {
			if pages > 20 {
				t.Fatalf("size %d: paging did not terminate", size)
			}
			rows, err := q.ListTransactionsPage(context.Background(), store.ListTransactionsPageParams{
				FundID: w.setup.Fund.ID, PageLimit: size + 1, CursorOccurredOn: cursorOn, CursorID: cursorID,
			})
			if err != nil {
				t.Fatalf("ListTransactionsPage() = %v", err)
			}
			more := int64(len(rows)) > size
			if more {
				rows = rows[:size]
			}
			got = append(got, rows...)
			if !more {
				break
			}
			last := rows[len(rows)-1]
			cursorOn, cursorID = last.OccurredOn, &last.ID
		}

		if len(got) != len(want) {
			t.Fatalf("page size %d: %d items, want %d", size, len(got), len(want))
		}
		var summed int64
		for i, g := range got {
			if g.ID != want[i].ID {
				t.Errorf("page size %d: item %d is row %d, want %d", size, i, g.ID, want[i].ID)
			}
			if g.GroupAmount != want[i].Amount {
				t.Errorf("page size %d: item %d amount %d, want %d", size, i, g.GroupAmount, want[i].Amount)
			}
			summed += g.GroupAmount
		}
		// 3 plain rows (1.000, 1.001, 1.002 by fill), two payments, one reversal
		// group: every rupiah of every row exactly once, none repeated or dropped.
		if wantSum := int64(1_000+1_000+1_000) + 75_000 + 20_000 + 20_000; summed != wantSum {
			t.Errorf("page size %d: items sum to %d, want %d", size, summed, wantSum)
		}
	}
}

func TestFiltersKeepAGroupWhole(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	w.pay(t, w.budi, "2026-09-02", 10_000, "2026-09")
	postTransaction(t, w.r, transactionRequest{
		AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-08-20",
	})

	cases := []struct {
		name  string
		query string
		// want is the payment's (75.000, 3 months) presence; other is whether the
		// other items (Budi's payment, the expense) are expected.
		wantAni bool
	}{
		{"no filter", "", true},
		{"month of the payment", "month=2026-08", true},
		{"another month", "month=2026-09", false},
		{"member", "member_id=" + strconv.FormatInt(w.ani.ID, 10), true},
		{"another member", "member_id=" + strconv.FormatInt(w.budi.ID, 10), false},
		{"a month the payment covers (dues_period)", "dues_period=2026-07", true},
		{"a month the payment does not cover", "dues_period=2026-05", false},
		{"direction in", "direction=in", true},
		{"direction out", "direction=out", false},
		{"purpose", "purpose_id=" + strconv.FormatInt(w.setup.MainPurposeID, 10), true},
		{"location", "account_id=" + strconv.FormatInt(w.setup.CashAccountID(t), 10), true},
		{"another location", "account_id=" + strconv.FormatInt(w.setup.BankAccountID(t), 10), false},
		{"search by member name", "q=ani", true},
		{"search by the sum shown", "q=75000", true},
		{"search by one month's amount", "q=25000", true},
		{"search by an amount nobody paid", "q=50000", false},
	}
	for _, tc := range cases {
		rows := w.list(t, tc.query)
		var ani []transactionResponse
		for _, r := range rows {
			if r.Kind == "dues" && r.MemberName != nil && *r.MemberName == "Ani" {
				ani = append(ani, r)
			}
		}
		if tc.wantAni {
			if len(ani) != 1 || ani[0].Amount != 75_000 || len(ani[0].Periods) != 3 || *ani[0].DuesPaymentID != *posted[0].DuesPaymentID {
				t.Errorf("%s: Ani's payment items = %+v, want exactly one, whole", tc.name, ani)
			}
		} else if len(ani) != 0 {
			t.Errorf("%s: Ani's payment items = %+v, want none", tc.name, ani)
		}
	}
}

func TestAPaymentItemCarriesEveryReceiptOfItsRows(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	posted := w.pay(t, w.ani, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	var anchor, last transactionResponse
	for _, p := range posted {
		switch *p.DuesPeriod {
		case "2026-06":
			anchor = p
		case "2026-08":
			last = p
		}
	}

	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))
	upload := func(id int64) int64 {
		rec := postReceiptFile(t, w.r, "/api/transactions/"+strconv.FormatInt(id, 10)+"/receipts", fixture)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST receipt on %d = %d, want %d (body: %s)", id, rec.Code, http.StatusCreated, rec.Body.String())
		}
		return decodeReceiptResponse(t, rec).ID
	}
	onLast := upload(last.ID)
	onAnchor := upload(anchor.ID)

	item := itemsOfKind(w.list(t, ""), "dues")[0]
	if item.ID != anchor.ID {
		t.Fatalf("item id = %d, want the anchor %d", item.ID, anchor.ID)
	}
	want := []int64{onLast, onAnchor}
	slices.Sort(want)
	if !slices.Equal(item.ReceiptIDs, want) {
		t.Errorf("receipt_ids = %v, want both photos %v, oldest first, the one on a non-anchor row included", item.ReceiptIDs, want)
	}
}

// Every row that is not a dues payment or its reversal keeps its shape: no
// payment, no periods, and a reversal state that is just its reversed flag.
func TestOrdinaryRowsKeepTheirShape(t *testing.T) {
	t.Parallel()
	w := newDuesGroupWorld(t)
	rec := postTransaction(t, w.r, transactionRequest{
		AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-01",
	})
	var posted transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction: %v", err)
	}
	if rec := postReversal(t, w.r, posted.ID, reverseTransactionRequest{}); rec.Code != http.StatusCreated {
		t.Fatalf("POST reversal = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rows := w.list(t, "")
	if len(rows) != 2 {
		t.Fatalf("list holds %d items, want the row and its reversal", len(rows))
	}
	for _, r := range rows {
		if r.DuesPaymentID != nil || len(r.Periods) != 0 || r.Periods == nil {
			t.Errorf("row %d: dues_payment_id %v periods %v, want null and []", r.ID, r.DuesPaymentID, r.Periods)
		}
		if r.Amount != 50_000 {
			t.Errorf("row %d amount = %d, want 50000", r.ID, r.Amount)
		}
	}
	for _, r := range rows {
		if r.ID == posted.ID && (r.ReversalState != "all" || !r.Reversed) {
			t.Errorf("reversed row: state %q reversed %v, want all true", r.ReversalState, r.Reversed)
		}
	}
}
