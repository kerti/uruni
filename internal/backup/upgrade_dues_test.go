package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// The 2-to-3 step (ADR-048): a format 2 file's dues rows are bound into
// payments by what a payment's rows share (fund, member, location, day, note)
// and by time (each row at most one second after the one before it). No ledger
// row moves, so every figure the file's rows say is the same before and after.

// v2Of renders a current Document in format 2's shape: no dues_payments array,
// no dues_payment_id on any row. The test-side inverse of upgradeV2.
func v2Of(doc Document) documentV2 {
	return documentV2{
		FormatVersion: 2,
		Users:         doc.Users, Funds: doc.Funds, Accounts: doc.Accounts, Purposes: doc.Purposes,
		DuesTiers: doc.DuesTiers, DuesRates: doc.DuesRates, Members: doc.Members, MemberTiers: doc.MemberTiers,
		Transfers: doc.Transfers, Reimbursements: doc.Reimbursements, Transactions: transactionsV2Of(doc.Transactions),
		Receipts: doc.Receipts, Reconciliations: doc.Reconciliations, ReconciliationLines: doc.ReconciliationLines,
		Incidentals: doc.Incidentals, IncidentalRecipients: doc.IncidentalRecipients, Totals: doc.Totals,
	}
}

func v2Zip(t *testing.T, v2 documentV2) []byte {
	t.Helper()
	raw, err := json.Marshal(v2)
	if err != nil {
		t.Fatalf("marshaling a v2 document: %v", err)
	}
	return zipOf(t, raw, nil)
}

// duesRow is one dues row of a format 2 file, as the grouping rule sees it.
type duesRow struct {
	id, fund, member, account int64
	on                        string
	note                      *string
	at                        int64
}

func (r duesRow) tx() transactionV2 {
	member := r.member
	period := "2026-01"
	return transactionV2{
		ID: r.id, FundID: r.fund, AccountID: r.account, PurposeID: 1, Direction: "in", Amount: 10_000,
		OccurredOn: r.on, Kind: "dues", MemberID: &member, DuesPeriod: &period, Note: r.note, CreatedAt: r.at,
	}
}

func TestUpgradeV2BindsDuesRowsIntoPayments(t *testing.T) {
	t.Parallel()
	same := func(id, at int64) duesRow {
		return duesRow{id: id, fund: 1, member: 1, account: 1, on: "2026-08-12", at: at}
	}
	with := func(r duesRow, f func(*duesRow)) duesRow { f(&r); return r }
	note := func(s string) *string { return &s }

	for _, tc := range []struct {
		name string
		rows []duesRow
		want [][]int64 // the payments, as the ids of their rows
	}{
		{"one row is a payment of its own", []duesRow{same(1, 100)}, [][]int64{{1}}},
		{"rows in the same second", []duesRow{same(1, 100), same(2, 100), same(3, 100)}, [][]int64{{1, 2, 3}}},
		{"rows one second apart", []duesRow{same(1, 100), same(2, 101)}, [][]int64{{1, 2}}},
		{"rows two seconds apart", []duesRow{same(1, 100), same(2, 102)}, [][]int64{{1}, {2}}},
		{"a chain across a second boundary", []duesRow{same(1, 100), same(2, 101), same(3, 102)}, [][]int64{{1, 2, 3}}},
		{"a chain that breaks after the second row", []duesRow{same(1, 100), same(2, 101), same(3, 103)}, [][]int64{{1, 2}, {3}}},
		{"a different member", []duesRow{same(1, 100), with(same(2, 100), func(r *duesRow) { r.member = 2 })}, [][]int64{{1}, {2}}},
		{"a different location", []duesRow{same(1, 100), with(same(2, 100), func(r *duesRow) { r.account = 2 })}, [][]int64{{1}, {2}}},
		{"a different day", []duesRow{same(1, 100), with(same(2, 100), func(r *duesRow) { r.on = "2026-08-13" })}, [][]int64{{1}, {2}}},
		{"a different fund", []duesRow{same(1, 100), with(same(2, 100), func(r *duesRow) { r.fund = 2 })}, [][]int64{{1}, {2}}},
		{"a different note", []duesRow{with(same(1, 100), func(r *duesRow) { r.note = note("a") }), with(same(2, 100), func(r *duesRow) { r.note = note("b") })}, [][]int64{{1}, {2}}},
		{"the same note", []duesRow{with(same(1, 100), func(r *duesRow) { r.note = note("a") }), with(same(2, 100), func(r *duesRow) { r.note = note("a") })}, [][]int64{{1, 2}}},
		{"no note and an empty note are not the same", []duesRow{same(1, 100), with(same(2, 100), func(r *duesRow) { r.note = note("") })}, [][]int64{{1}, {2}}},
		{"two nulls are the same", []duesRow{same(1, 100), same(2, 101)}, [][]int64{{1, 2}}},
		// Ids are not time: the chain follows created_at, whatever the ids say.
		{"ids against the clock", []duesRow{same(9, 100), same(3, 101), same(5, 300)}, [][]int64{{3, 9}, {5}}},
		{"interleaved with another member's payment", []duesRow{
			same(1, 100), with(same(2, 100), func(r *duesRow) { r.member = 2 }), same(3, 101), with(same(4, 101), func(r *duesRow) { r.member = 2 }),
		}, [][]int64{{1, 3}, {2, 4}}},
		{"alike but apart in time are two payments", []duesRow{same(1, 100), same(2, 100+60)}, [][]int64{{1}, {2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v2 := documentV2{FormatVersion: 2}
			for _, r := range tc.rows {
				v2.Transactions = append(v2.Transactions, r.tx())
			}
			doc, err := upgradeV2(v2)
			if err != nil {
				t.Fatalf("upgradeV2() = %v, want no error", err)
			}

			got := map[int64][]int64{}
			for _, tx := range doc.Transactions {
				if tx.DuesPaymentID == nil {
					t.Fatalf("dues row %d has no payment, want every dues row bound", tx.ID)
				}
				got[*tx.DuesPaymentID] = append(got[*tx.DuesPaymentID], tx.ID)
			}
			var gotGroups [][]int64
			for _, ids := range got {
				slices.Sort(ids)
				gotGroups = append(gotGroups, ids)
			}
			sortGroups(gotGroups)
			want := slices.Clone(tc.want)
			sortGroups(want)
			if !reflect.DeepEqual(gotGroups, want) {
				t.Errorf("payments = %v, want %v", gotGroups, want)
			}
			if len(doc.DuesPayments) != len(want) {
				t.Errorf("dues_payments = %d, want %d", len(doc.DuesPayments), len(want))
			}
			byID := map[int64]DuesPayment{}
			for _, p := range doc.DuesPayments {
				byID[p.ID] = p
			}
			for _, tx := range doc.Transactions {
				p, ok := byID[*tx.DuesPaymentID]
				if !ok || p.FundID != tx.FundID {
					t.Errorf("row %d names payment %d = %+v, want one of its own fund", tx.ID, *tx.DuesPaymentID, p)
				}
			}
		})
	}
}

func sortGroups(g [][]int64) {
	slices.SortFunc(g, func(a, b []int64) int {
		switch {
		case a[0] < b[0]:
			return -1
		case a[0] > b[0]:
			return 1
		}
		return 0
	})
}

// A payment's header takes its earliest row's time; ids are minted from 1 in a
// fixed order, so one file always upgrades to one document, whatever order its
// array is in; only dues rows get a payment.
func TestUpgradeV2HeadersAreDeterministicAndOnlyDuesRowsAreBound(t *testing.T) {
	t.Parallel()
	a := duesRow{id: 7, fund: 1, member: 1, account: 1, on: "2026-08-12", at: 205}
	b := duesRow{id: 2, fund: 1, member: 1, account: 1, on: "2026-08-12", at: 204} // same payment as a, earlier
	c := duesRow{id: 9, fund: 1, member: 2, account: 1, on: "2026-08-12", at: 100}
	opening := transactionV2{ID: 1, FundID: 1, AccountID: 1, PurposeID: 1, Direction: "in", Amount: 5, OccurredOn: "2026-01-01", Kind: "opening", CreatedAt: 50}
	paid := b.tx()
	reversal := transactionV2{
		ID: 11, FundID: 1, AccountID: 1, PurposeID: 1, Direction: "out", Amount: 10_000, OccurredOn: "2026-08-12", Kind: "adjustment",
		MemberID: paid.MemberID, DuesPeriod: paid.DuesPeriod, ReversesTransactionID: &paid.ID, CreatedAt: 300,
	}

	build := func(txns ...transactionV2) Document {
		doc, err := upgradeV2(documentV2{FormatVersion: 2, Transactions: txns})
		if err != nil {
			t.Fatalf("upgradeV2() = %v, want no error", err)
		}
		return doc
	}
	one := build(opening, a.tx(), b.tx(), c.tx(), reversal)
	two := build(reversal, c.tx(), b.tx(), opening, a.tx())

	want := []DuesPayment{{ID: 1, FundID: 1, CreatedAt: 100}, {ID: 2, FundID: 1, CreatedAt: 204}}
	for name, doc := range map[string]Document{"as written": one, "shuffled": two} {
		if !reflect.DeepEqual(doc.DuesPayments, want) {
			t.Errorf("%s: dues_payments = %+v, want %+v (earliest row's time, by that time)", name, doc.DuesPayments, want)
		}
		for _, tx := range doc.Transactions {
			switch tx.Kind {
			case "dues":
				wantPayment := int64(2)
				if tx.ID == c.id {
					wantPayment = 1
				}
				if tx.DuesPaymentID == nil || *tx.DuesPaymentID != wantPayment {
					t.Errorf("%s: dues row %d payment = %v, want %d", name, tx.ID, tx.DuesPaymentID, wantPayment)
				}
			default:
				if tx.DuesPaymentID != nil {
					t.Errorf("%s: %s row %d has payment %d, want none", name, tx.Kind, tx.ID, *tx.DuesPaymentID)
				}
			}
		}
	}
}

func TestUpgradeV2OfAFileWithNoDuesRowsHasNoPaymentsButAnArray(t *testing.T) {
	t.Parallel()
	doc, err := upgradeV2(documentV2{FormatVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if doc.DuesPayments == nil || len(doc.DuesPayments) != 0 {
		t.Errorf("dues_payments = %#v, want an empty array, not null", doc.DuesPayments)
	}
	if doc.FormatVersion != FormatVersion {
		t.Errorf("FormatVersion = %d, want %d", doc.FormatVersion, FormatVersion)
	}
}

// Format 2 never had the field: a v2 file carrying it, or the array, is not a v2
// file (strict decoding, as for every older shape).
func TestParseUploadRefusesAVersion2FileWithADuesPayment(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"format_version":2,"dues_payments":[]}`,
		`{"format_version":2,"transactions":[{"id":1,"dues_payment_id":1}]}`,
	} {
		if _, err := parseUploadBytes(t, zipOf(t, []byte(raw), nil)); !errors.Is(err, ErrMalformedBackup) {
			t.Errorf("ParseUpload(%s) = %v, want ErrMalformedBackup", raw, err)
		}
	}
}

// The last golden of format 2 (ADR-048's predecessor), checked in beside the
// current one: it goes through ParseUpload and Restore as a stored backup of
// the last release would.
func TestAFormat2GoldenIsUpgradedAndRestores(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/golden-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var peek struct {
		FormatVersion int64 `json:"format_version"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil || peek.FormatVersion != 2 {
		t.Fatalf("testdata/golden-v2.json format_version = %d, %v, want 2", peek.FormatVersion, err)
	}

	parsed, err := parseUploadBytes(t, zipOf(t, raw, map[string][]byte{receiptFilename: realJPEGBytes(t)}))
	if err != nil {
		t.Fatalf("ParseUpload(format 2 golden) = %v, want no error", err)
	}
	doc := parsed.Document
	if doc.FormatVersion != FormatVersion {
		t.Errorf("upgraded FormatVersion = %d, want %d", doc.FormatVersion, FormatVersion)
	}
	dues := 0
	for _, tx := range doc.Transactions {
		if (tx.Kind == "dues") != (tx.DuesPaymentID != nil) {
			t.Errorf("row %d kind %q payment %v: want a payment exactly on dues rows", tx.ID, tx.Kind, tx.DuesPaymentID)
		}
		if tx.Kind == "dues" {
			dues++
		}
	}
	if dues == 0 || len(doc.DuesPayments) != dues {
		t.Errorf("dues rows = %d, payments = %d, want a payment for each of the golden's lone dues rows", dues, len(doc.DuesPayments))
	}

	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)
	assertBalancesMatch(t, destDB, doc)
}

// The step moves no money and no status: a format 2 file written from a ledger
// full of multi-month payments restores to the same balances, dues status, dues
// grid and monthly report as the database it came from, and the payments the
// step finds are the ones the ledger wrote.
func TestUpgradeV2ChangesNoFigureAndFindsTheRealPayments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	l := ledger.New(srcDB)

	_, base := exportFixture(t, srcDB, t.TempDir())
	var andi, budi, cash int64
	for _, m := range base.Members {
		switch m.Name {
		case "Andi":
			andi = m.ID
		case "Budi":
			budi = m.ID
		}
	}
	cash = base.Accounts[0].ID
	pay := func(member int64, on string, periods ...string) []int64 {
		t.Helper()
		rows := make([]ledger.PeriodAmount, len(periods))
		for i, p := range periods {
			rows[i] = ledger.PeriodAmount{DuesPeriod: p, Amount: money.Amount(50_000)}
		}
		posted, err := l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
			FundID: base.Funds[0].ID, AccountID: cash, MemberID: member, OccurredOn: on, Periods: rows,
		})
		if err != nil {
			t.Fatalf("PostDuesPayments(%v) = %v", periods, err)
		}
		ids := make([]int64, len(posted))
		for i, p := range posted {
			ids[i] = p.ID
		}
		return ids
	}
	pay(andi, "2026-06-10", "2026-04", "2026-05", "2026-06")
	toReverse := pay(andi, "2026-07-01", "2026-07", "2026-08")
	pay(budi, "2026-03-02", "2026-02", "2026-03")
	pay(budi, "2026-04-02", "2026-04")
	if _, err := l.ReverseTransaction(ctx, ledger.ReverseTransactionParams{FundID: base.Funds[0].ID, TransactionID: toReverse[1]}); err != nil {
		t.Fatalf("ReverseTransaction() = %v, want no error", err)
	}

	_, src := exportFixture(t, srcDB, t.TempDir())
	parsed, err := parseUploadBytes(t, v2Zip(t, v2Of(src)))
	if err != nil {
		t.Fatalf("ParseUpload(format 2) = %v, want no error", err)
	}

	// The payments found are the payments written: the same rows, grouped alike.
	partition := func(doc Document) map[int64]string {
		groups := map[int64][]int64{}
		for _, tx := range doc.Transactions {
			if tx.DuesPaymentID != nil {
				groups[*tx.DuesPaymentID] = append(groups[*tx.DuesPaymentID], tx.ID)
			}
		}
		byRow := map[int64]string{}
		for _, ids := range groups {
			slices.Sort(ids)
			label, _ := json.Marshal(ids)
			for _, id := range ids {
				byRow[id] = string(label)
			}
		}
		return byRow
	}
	if got, want := partition(parsed.Document), partition(src); !reflect.DeepEqual(got, want) {
		t.Errorf("payments found by the step:\n got  %v\n want %v", got, want)
	}
	if len(parsed.Document.DuesPayments) != len(src.DuesPayments) {
		t.Errorf("dues_payments = %d, want the ledger's %d", len(parsed.Document.DuesPayments), len(src.DuesPayments))
	}

	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)
	assertBalancesMatch(t, destDB, src)

	dest := ledger.New(destDB)
	fundID := src.Funds[0].ID
	if a, b := mustBalance(t, l, fundID), mustBalance(t, dest, fundID); a != b {
		t.Errorf("fund balance after the upgrade = %d, before = %d", b, a)
	}
	for period := "2025-11"; period <= "2027-03"; period = nextMonth(period) {
		before, err := l.DuesStatusForPeriod(ctx, fundID, period)
		if err != nil {
			t.Fatal(err)
		}
		after, err := dest.DuesStatusForPeriod(ctx, fundID, period)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Errorf("dues status for %s changed:\n before %+v\n after  %+v", period, before, after)
		}
	}
	for _, end := range []string{"2026-06", "2026-09", "2026-12"} {
		before, err := l.DuesGrid(ctx, fundID, end)
		if err != nil {
			t.Fatal(err)
		}
		after, err := dest.DuesGrid(ctx, fundID, end)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Errorf("dues grid ending %s changed:\n before %+v\n after  %+v", end, before, after)
		}
	}
	for _, month := range []string{"2026-02", "2026-03", "2026-04", "2026-06", "2026-07", "2026-08"} {
		before, err := l.MonthlyReport(ctx, ledger.ReportParams{FundID: fundID, Month: month})
		if err != nil {
			t.Fatal(err)
		}
		after, err := dest.MonthlyReport(ctx, ledger.ReportParams{FundID: fundID, Month: month})
		if err != nil {
			t.Fatal(err)
		}
		if before.Balance != after.Balance || !reflect.DeepEqual(before.PurposeBalances, after.PurposeBalances) ||
			!reflect.DeepEqual(before.Walk, after.Walk) || !reflect.DeepEqual(before.Dues, after.Dues) {
			t.Errorf("monthly report for %s changed:\n before %+v\n after  %+v", month, before, after)
		}
	}
}

func mustBalance(t *testing.T, l *ledger.Ledger, fundID int64) money.Amount {
	t.Helper()
	b, err := l.FundBalance(context.Background(), fundID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
