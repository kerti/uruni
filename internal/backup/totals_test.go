package backup

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// totalsDoc is the smallest document CheckTotals can say something about:
// one fund, one account, two purposes (main and an incidental envelope), and
// whatever transactions the case supplies. Its totals are computed by the
// caller, by hand, so the test's expectation is never the code under test.
func totalsDoc(txns []Transaction, totals Totals) Document {
	return Document{
		Funds:        []Fund{{ID: 1}},
		Accounts:     []Account{{ID: 10, FundID: 1}},
		Purposes:     []Purpose{{ID: 20, FundID: 1, Kind: "main"}, {ID: 21, FundID: 1, Kind: "incidental"}},
		Transactions: txns,
		Totals:       totals,
	}
}

func txn(id int64, direction string, amount int64, purposeID int64) Transaction {
	return Transaction{ID: id, FundID: 1, AccountID: 10, PurposeID: purposeID, Direction: direction, Amount: amount}
}

func totals(fund, account, main, envelope int64) Totals {
	return Totals{
		Funds:    []FundTotal{{FundID: 1, Balance: fund}},
		Accounts: []AccountTotal{{AccountID: 10, Balance: account}},
		Purposes: []PurposeTotal{{PurposeID: 20, Balance: main}, {PurposeID: 21, Balance: envelope}},
	}
}

func TestCheckTotalsAcceptsADocumentThatAgreesWithItself(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		txns []Transaction
		want Totals
	}{
		{"no transactions: every total is zero", nil, totals(0, 0, 0, 0)},
		{"income only", []Transaction{txn(1, "in", 500000, 20)}, totals(500000, 500000, 500000, 0)},
		{"spending beyond the balance is a negative total, not an error",
			[]Transaction{txn(1, "in", 1000, 20), txn(2, "out", 4000, 20)}, totals(-3000, -3000, -3000, 0)},
		{"an adjusting entry nets the original out",
			[]Transaction{txn(1, "in", 50000, 20), txn(2, "out", 50000, 20)}, totals(0, 0, 0, 0)},
		{"an incidental's leftover rolled into Kas Utama moves between purposes, not out of the fund",
			[]Transaction{txn(1, "in", 30000, 21), txn(2, "out", 30000, 21), txn(3, "in", 30000, 20)}, totals(30000, 30000, 30000, 0)},
		{"money inside an open envelope counts in the fund and in the envelope",
			[]Transaction{txn(1, "in", 100, 20), txn(2, "in", 30000, 21)}, totals(30100, 30100, 100, 30000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := CheckTotals(totalsDoc(tc.txns, tc.want)); err != nil {
				t.Errorf("CheckTotals() = %v, want nil", err)
			}
		})
	}
}

func TestCheckTotalsRefusesAnyDisagreementExactly(t *testing.T) {
	t.Parallel()
	income := []Transaction{txn(1, "in", 500000, 20)}
	good := totals(500000, 500000, 500000, 0)

	tests := []struct {
		name   string
		mutate func(*Totals)
	}{
		{"fund total off by one rupiah", func(x *Totals) { x.Funds[0].Balance++ }},
		{"account total off by one rupiah", func(x *Totals) { x.Accounts[0].Balance-- }},
		{"purpose total off by one rupiah", func(x *Totals) { x.Purposes[1].Balance++ }},
		{"sign flipped", func(x *Totals) { x.Funds[0].Balance = -x.Funds[0].Balance }},
		{"a fund total left out", func(x *Totals) { x.Funds = nil }},
		{"an account total left out", func(x *Totals) { x.Accounts = nil }},
		{"a purpose total left out", func(x *Totals) { x.Purposes = x.Purposes[:1] }},
		{"a total for a fund the file does not list", func(x *Totals) { x.Funds = append(x.Funds, FundTotal{FundID: 9}) }},
		{"a total for an account the file does not list", func(x *Totals) { x.Accounts = append(x.Accounts, AccountTotal{AccountID: 99}) }},
		{"the same purpose totalled twice", func(x *Totals) { x.Purposes = append(x.Purposes, x.Purposes[0]) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x := totals(good.Funds[0].Balance, good.Accounts[0].Balance, good.Purposes[0].Balance, good.Purposes[1].Balance)
			tc.mutate(&x)
			if err := CheckTotals(totalsDoc(income, x)); !errors.Is(err, ErrTotalsMismatch) {
				t.Errorf("CheckTotals() = %v, want ErrTotalsMismatch", err)
			}
		})
	}
}

// A sum that cannot be held in an int64 is a corrupt file, said as such -
// never a wrapped-around figure that happens to compare equal.
func TestCheckTotalsRefusesAnOverflowingSum(t *testing.T) {
	t.Parallel()
	doc := totalsDoc([]Transaction{txn(1, "in", math.MaxInt64, 20), txn(2, "in", 1, 20)}, totals(0, 0, 0, 0))
	if err := CheckTotals(doc); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("CheckTotals() = %v, want ErrMalformedBackup", err)
	}
}

// The real fixture, through the real export: what BuildDocument writes is
// what CheckTotals accepts (every table, a reversal, a transfer, an envelope,
// a reconciliation).
func TestCheckTotalsAcceptsAnExportedFixture(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	buildFixture(t, sqlDB, t.TempDir())
	doc, _, err := BuildDocument(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}
	if err := CheckTotals(doc); err != nil {
		t.Errorf("CheckTotals(exported fixture) = %v, want nil", err)
	}
}

// The inspect step: ParseUpload refuses a zip whose totals disagree with its
// rows, with the shared sentinel, before any caller could ask for a password
// - and accepts the same export untampered, so the refusal is the totals and
// nothing else (the receipt is a real image, the zip otherwise identical).
func TestParseUploadChecksTheTotalsAgainstTheRows(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	if err := os.WriteFile(filepath.Join(uploadsDir, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	doc, receipts, err := BuildDocument(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}

	zipOf := func(d Document) []byte {
		b, _, _, err := BuildZip(d, receipts, uploadsDir)
		if err != nil {
			t.Fatalf("BuildZip() = %v, want no error", err)
		}
		return b
	}

	if _, err := parseUploadBytes(t, zipOf(doc)); err != nil {
		t.Fatalf("ParseUpload(untampered export) = %v, want no error", err)
	}

	for name, mutate := range map[string]func(*Document){
		"fund":    func(d *Document) { d.Totals.Funds[0].Balance++ },
		"account": func(d *Document) { d.Totals.Accounts[0].Balance++ },
		"purpose": func(d *Document) { d.Totals.Purposes[0].Balance++ },
	} {
		tampered := doc
		tampered.Totals = Totals{
			Funds:    append([]FundTotal(nil), doc.Totals.Funds...),
			Accounts: append([]AccountTotal(nil), doc.Totals.Accounts...),
			Purposes: append([]PurposeTotal(nil), doc.Totals.Purposes...),
		}
		mutate(&tampered)
		if _, err := parseUploadBytes(t, zipOf(tampered)); !errors.Is(err, ErrTotalsMismatch) {
			t.Errorf("ParseUpload(%s total off by one) = %v, want ErrTotalsMismatch", name, err)
		}
	}
}
