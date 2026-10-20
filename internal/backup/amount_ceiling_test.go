package backup

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// A restore writes straight to the tables, around the ledger, so the Rp
// 999.999.999.999 ceiling reaches it only through the schema's CHECKs (#622).
// These tests hand-build a backup holding one row above the ceiling, per money
// column, and want both entry points to refuse it and change nothing.

const ceilingMax = int64(money.MaxAmount)

// ceilingDoc is a small, valid backup that touches every money column once:
// a fund with two locations of value (cash and an envelope purpose), a dues
// rate, an unsettled claim, one posted entry, a snapshot with a line, and an
// envelope with a target and a minimum. Its totals are right for it.
func ceilingDoc() Document {
	one, two := int64(1), int64(2)
	target, minimum := int64(5000), int64(100)
	return Document{
		FormatVersion: FormatVersion,
		Users:         []User{},
		Funds:         []Fund{{ID: 1, Name: "Dana", Currency: "IDR", ReportSlug: "0000000000000000000slug01", CreatedAt: 1}},
		Accounts:      []Account{{ID: 1, FundID: 1, Kind: "cash", Name: "Kas", CreatedAt: 1}},
		Purposes: []Purpose{
			{ID: 1, FundID: 1, Kind: "main", Name: "Kas Utama", CreatedAt: 1},
			{ID: 2, FundID: 1, Kind: "incidental", Name: "Kurban", CreatedAt: 1},
		},
		DuesTiers:      []DuesTier{{ID: 1, FundID: 1, Name: "Tetap", CreatedAt: 1}},
		DuesRates:      []DuesRate{{ID: 1, TierID: 1, Amount: 25000, EffectiveFrom: "2026-01", CreatedAt: 1}},
		Members:        []Member{{ID: 1, FundID: 1, Name: "Jane", TierID: &one, JoinedOn: strPtr("2026-01-01"), CreatedAt: 1}},
		Transfers:      []Transfer{},
		Reimbursements: []Reimbursement{{ID: 1, FundID: 1, MemberID: 1, PurposeID: 1, Amount: 700, IncurredOn: "2026-08-01", CreatedAt: 1}},
		Transactions: []Transaction{{
			ID: 1, FundID: 1, AccountID: 1, PurposeID: 1, Direction: "in", Amount: 1000,
			OccurredOn: "2026-08-12", Kind: "normal", CreatedAt: 1,
		}},
		Receipts:        []Receipt{},
		Reconciliations: []Reconciliation{{ID: 1, FundID: 1, PerformedAt: 100, CreatedAt: 100}},
		ReconciliationLines: []ReconciliationLine{{
			ID: 1, FundID: 1, ReconciliationID: 1, AccountID: 1,
			RecordedAmount: 1000, ActualAmount: 1000, DifferenceAmount: 0, Resolution: "matched",
		}},
		Incidentals:          []Incidental{{PurposeID: two, Occasion: "Kurban", TargetAmount: &target, OpenedOn: "2026-05-01", MinimumPerMember: &minimum, CreatedAt: 1}},
		IncidentalRecipients: []IncidentalRecipient{},
		Totals: Totals{
			Funds:    []FundTotal{{FundID: 1, Balance: 1000}},
			Accounts: []AccountTotal{{AccountID: 1, Balance: 1000}},
			Purposes: []PurposeTotal{{PurposeID: 1, Balance: 1000}, {PurposeID: 2, Balance: 0}},
		},
	}
}

// ceilingCases sets one money column of ceilingDoc to v. The reconciliation
// ones keep difference = actual - recorded true so only the ceiling can refuse.
var ceilingCases = []struct {
	name, restoring string
	// derived marks a column with no ceiling of its own (difference_amount, fixed
	// by actual - recorded): it has no refusal case, and its accept case is the
	// widest line the operands allow, difference = 2 * max.
	derived bool
	set     func(d *Document, v int64)
}{
	{"dues_rate.amount", "dues rate", false, func(d *Document, v int64) { d.DuesRates[0].Amount = v }},
	{"reimbursement.amount", "reimbursement", false, func(d *Document, v int64) { d.Reimbursements[0].Amount = v }},
	{"transaction.amount", "transaction", false, func(d *Document, v int64) { d.Transactions[0].Amount = v }},
	{"incidental.target_amount", "incidental", false, func(d *Document, v int64) { d.Incidentals[0].TargetAmount = &v }},
	{"incidental.minimum_per_member", "incidental", false, func(d *Document, v int64) { d.Incidentals[0].MinimumPerMember = &v }},
	{"reconciliation_line.recorded_amount", "reconciliation line", false, func(d *Document, v int64) {
		l := &d.ReconciliationLines[0]
		l.RecordedAmount, l.ActualAmount, l.DifferenceAmount = v, v, 0
	}},
	{"reconciliation_line.actual_amount", "reconciliation line", false, func(d *Document, v int64) {
		l := &d.ReconciliationLines[0]
		l.RecordedAmount, l.ActualAmount, l.DifferenceAmount = 1, v, v-1
	}},
	{"reconciliation_line.difference_amount", "reconciliation line", true, func(d *Document, _ int64) {
		l := &d.ReconciliationLines[0]
		l.RecordedAmount, l.ActualAmount, l.DifferenceAmount = -ceilingMax, ceilingMax, 2*ceilingMax
	}},
}

// withTotals keeps the totals block true after a case moved the posted entry,
// so a control that should restore is not refused for the wrong reason.
func withTotals(d Document) Document {
	a := d.Transactions[0].Amount
	d.Totals = Totals{
		Funds:    []FundTotal{{FundID: 1, Balance: a}},
		Accounts: []AccountTotal{{AccountID: 1, Balance: a}},
		Purposes: []PurposeTotal{{PurposeID: 1, Balance: a}, {PurposeID: 2, Balance: 0}},
	}
	return d
}

// TestRestoreAcceptsEveryMoneyColumnAtTheCeiling is the control for the
// refusals below: the same document with the column at exactly the ceiling
// restores, so a refusal at ceiling+1 is the ceiling and nothing else. The
// derived difference_amount is accepted at 2 * max, its widest legitimate value.
func TestRestoreAcceptsEveryMoneyColumnAtTheCeiling(t *testing.T) {
	t.Parallel()
	for _, tc := range ceilingCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			destDB := newTestDB(t)
			seedOneUser(t, destDB)
			doc := ceilingDoc()
			tc.set(&doc, ceilingMax)
			doc = withTotals(doc)

			err := Restore(context.Background(), destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: doc}, time.Unix(1800000000, 0))
			if err != nil {
				t.Fatalf("Restore() with %s = %d: %v, want it restored", tc.name, ceilingMax, err)
			}
		})
	}
}

// TestRestoreRefusesAMoneyColumnAboveTheCeilingAndRollsBack: one rupiah more,
// in any money column, is refused by the schema's CHECK, and the live fund the
// restore was about to replace is still there, untouched.
func TestRestoreRefusesAMoneyColumnAboveTheCeilingAndRollsBack(t *testing.T) {
	t.Parallel()
	for _, tc := range ceilingCases {
		if tc.derived {
			continue // no ceiling of its own: its accept case is above
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			destDB := newTestDB(t)
			seedOneUser(t, destDB)
			live := seedLiveFund(t, destDB)
			doc := ceilingDoc()
			tc.set(&doc, ceilingMax+1)
			doc = withTotals(doc)

			err := Restore(context.Background(), destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: doc}, time.Unix(1800000000, 0))
			assertRefusedByTheCeiling(t, tc.name, tc.restoring, err)
			assertLiveFundSurvived(t, destDB, live)
		})
	}
}

// TestRestoreForRebuildRefusesAMoneyColumnAboveTheCeilingAndRollsBack: the same
// refusal on the rebuild's entry point, over an empty scratch database: no
// fund, and not the login either, is left behind.
func TestRestoreForRebuildRefusesAMoneyColumnAboveTheCeilingAndRollsBack(t *testing.T) {
	t.Parallel()
	for _, tc := range ceilingCases {
		if tc.derived {
			continue // no ceiling of its own: its accept case is above
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scratch := newTestDB(t)
			doc := ceilingDoc()
			//nolint:gosec // not a credential - a fixed test fixture value
			doc.Users = []User{{ID: 7, Email: "treasurer@example.org", PasswordHash: "argon2id$fake$hash", CreatedAt: 5}}
			tc.set(&doc, ceilingMax+1)
			doc = withTotals(doc)

			_, err := RestoreForRebuild(context.Background(), scratch, t.TempDir(), ParsedUpload{Document: doc})
			assertRefusedByTheCeiling(t, tc.name, tc.restoring, err)

			q := store.New(scratch)
			if n, _ := q.CountUsers(context.Background()); n != 0 {
				t.Errorf("a refused rebuild left %d login(s)", n)
			}
			if funds, _ := q.ListFunds(context.Background()); len(funds) != 0 {
				t.Errorf("a refused rebuild left %d fund(s)", len(funds))
			}
		})
	}
}

// seedLiveFund gives the destination a fund the restore would replace.
func seedLiveFund(t *testing.T, destDB *sql.DB) store.Fund {
	t.Helper()
	f, err := store.New(destDB).CreateFund(context.Background(), store.CreateFundParams{
		Name: "Live fund", Currency: "IDR", ReportSlug: "0000000000000000live01", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	return f
}

func assertLiveFundSurvived(t *testing.T, destDB *sql.DB, live store.Fund) {
	t.Helper()
	funds, err := store.New(destDB).ListFunds(context.Background())
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 1 || funds[0].ID != live.ID || funds[0].Name != live.Name {
		t.Errorf("ListFunds() = %v, want only the live fund %q - a refused restore changes nothing", funds, live.Name)
	}
}

// assertRefusedByTheCeiling wants a CHECK failure on the money column's own
// ceiling, wrapped with the row it was restoring.
func assertRefusedByTheCeiling(t *testing.T, column, restoring string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("restore with %s = max+1: nil error, want the schema's CHECK to refuse it", column)
	}
	msg := err.Error()
	for _, want := range []string{"backup: restoring " + restoring, "CHECK constraint failed", "999999999999"} {
		if !strings.Contains(msg, want) {
			t.Errorf("restore with %s = max+1: error %q, want it to contain %q", column, msg, want)
		}
	}
}
