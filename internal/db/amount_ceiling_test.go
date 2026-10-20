package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The schema half of the Rp 999.999.999.999 ceiling (#622). #499 enforces
// money.MaxAmount in code; these tests pin the CHECK that stops a row written
// around the ledger, a backup restore included, from passing it.

const ceiling = int64(money.MaxAmount)

// moneyColumn is one column that holds rupiah, and how to write one row whose
// only unusual value is that column's. Every insert is otherwise valid, so a
// refusal can only be the ceiling.
type moneyColumn struct {
	table, column string
	// signed columns are bounded on both sides: -max..max.
	signed bool
	// derivedFrom, when set, names why the column has no ceiling literal of its
	// own: it is fixed by a CHECK over other bounded columns. Such a column is
	// still a listed money column (so the audit cannot silently lose it), but the
	// boundary test does not expect it to refuse max+1.
	derivedFrom string
	// insert writes a row carrying v in this column. For a signed reconciliation
	// column the other two are chosen so that difference = actual - recorded
	// still holds and the other two stay inside the ceiling, which isolates the
	// column under test from the derived-figure CHECK.
	insert func(t *testing.T, w *ceilingWriter, v int64) error
}

// moneyColumns is the explicit list the audit below holds the schema to.
var moneyColumns = []moneyColumn{
	{table: "dues_rate", column: "amount", insert: func(t *testing.T, w *ceilingWriter, v int64) error {
		_, err := w.q.CreateDuesRate(w.ctx, store.CreateDuesRateParams{
			TierID: createDuesTier(t, w.db, w.f.fundID, w.next("tier")), Amount: v, EffectiveFrom: "2026-01", CreatedAt: 1,
		})
		return err
	}},
	{table: "reimbursement", column: "amount", insert: func(_ *testing.T, w *ceilingWriter, v int64) error {
		_, err := w.q.CreateReimbursement(w.ctx, store.CreateReimbursementParams{
			FundID: w.f.fundID, MemberID: w.f.memberID, PurposeID: w.f.purposeID, Amount: v,
			IncurredOn: "2026-08-01", CreatedAt: 1,
		})
		return err
	}},
	{table: "transaction", column: "amount", insert: func(_ *testing.T, w *ceilingWriter, v int64) error {
		_, err := w.q.CreateTransaction(w.ctx, store.CreateTransactionParams{
			FundID: w.f.fundID, AccountID: w.f.accountID, PurposeID: w.f.purposeID,
			Direction: "in", Amount: v, OccurredOn: "2026-08-12", Kind: "normal", CreatedAt: 1,
		})
		return err
	}},
	{table: "incidental", column: "target_amount", insert: func(t *testing.T, w *ceilingWriter, v int64) error {
		_, err := w.q.CreateIncidental(w.ctx, store.CreateIncidentalParams{
			PurposeID: createPurpose(t, w.db, w.f.fundID, "incidental", w.next("envelope")),
			Occasion:  "Kurban", TargetAmount: &v, OpenedOn: "2026-05-01", CreatedAt: 1,
		})
		return err
	}},
	{table: "incidental", column: "minimum_per_member", insert: func(t *testing.T, w *ceilingWriter, v int64) error {
		_, err := w.q.CreateIncidental(w.ctx, store.CreateIncidentalParams{
			PurposeID: createPurpose(t, w.db, w.f.fundID, "incidental", w.next("envelope")),
			Occasion:  "Kurban", MinimumPerMember: &v, OpenedOn: "2026-05-01", CreatedAt: 1,
		})
		return err
	}},
	{table: "reconciliation_line", column: "recorded_amount", signed: true, insert: func(t *testing.T, w *ceilingWriter, v int64) error {
		// actual = 1 (or -1), so the difference stays small whichever side v is on.
		other := int64(1)
		if v < 0 {
			other = -1
		}
		return w.line(t, v, other, other-v)
	}},
	{table: "reconciliation_line", column: "actual_amount", signed: true, insert: func(t *testing.T, w *ceilingWriter, v int64) error {
		other := int64(1)
		if v < 0 {
			other = -1
		}
		return w.line(t, other, v, v-other)
	}},
	{table: "reconciliation_line", column: "difference_amount", signed: true,
		derivedFrom: "CHECK (difference_amount = actual_amount - recorded_amount) over the two bounded operands",
		insert: func(t *testing.T, w *ceilingWriter, v int64) error {
			// difference = actual - recorded, so reach v from two figures that
			// stay inside the ceiling themselves: actual = max, recorded = max - v
			// for a positive v, and the mirror for a negative.
			if v >= 0 {
				return w.line(t, ceiling-v, ceiling, v)
			}
			return w.line(t, ceiling, ceiling+v, v)
		}},
}

// ceilingWriter inserts the rows moneyColumns describe, one fund underneath.
type ceilingWriter struct {
	ctx context.Context
	db  *sql.DB
	q   *store.Queries
	f   ledgerFixture
	n   int
}

func newCeilingWriter(t *testing.T) *ceilingWriter {
	t.Helper()
	sqlDB := migratedTestDB(t)
	return &ceilingWriter{
		ctx: context.Background(), db: sqlDB, q: store.New(sqlDB),
		f: newLedgerFixture(t, sqlDB, "Test Fund", validSlug),
	}
}

// next is a name no earlier call in this writer used.
func (w *ceilingWriter) next(prefix string) string {
	w.n++
	return fmt.Sprintf("%s %d", prefix, w.n)
}

// line inserts a reconciliation_line under a snapshot of its own, so the
// one-count-per-location UNIQUE never fires.
func (w *ceilingWriter) line(t *testing.T, recorded, actual, difference int64) error {
	t.Helper()
	rec, err := w.q.CreateReconciliation(w.ctx, store.CreateReconciliationParams{
		FundID: w.f.fundID, PerformedAt: 100, CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}
	_, err = w.q.CreateReconciliationLine(w.ctx, store.CreateReconciliationLineParams{
		FundID: w.f.fundID, ReconciliationID: rec.ID, AccountID: w.f.accountID,
		RecordedAmount: recorded, ActualAmount: actual, DifferenceAmount: difference,
		Resolution: "left_open",
	})
	return err
}

func (c moneyColumn) name() string { return c.table + "." + c.column }

// TestEveryMoneyColumnIsListed is the audit that does not rely on a column's
// name. minimum_per_member has no "amount" in it, so grepping for amount (the
// ADR-024 audit) misses it; instead, every INTEGER column that is not a key or a
// timestamp is a money column, and must be in moneyColumns. A new one added to
// the schema without being listed here fails this test, and a listed one
// without the ceiling fails TestMoneyColumnBoundaries.
func TestEveryMoneyColumnIsListed(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)

	rows, err := sqlDB.Query(`
		SELECT m.name, p.name
		FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name <> 'goose_db_version'
		  AND p.type = 'INTEGER'
		ORDER BY m.name, p.cid`)
	if err != nil {
		t.Fatalf("reading the schema's columns = %v, want no error", err)
	}
	defer func() { _ = rows.Close() }()

	var found []string
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scanning a column = %v", err)
		}
		if column == "id" || strings.HasSuffix(column, "_id") || strings.HasSuffix(column, "_at") {
			continue // a key or a timestamp, not rupiah
		}
		found = append(found, table+"."+column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the schema's columns = %v", err)
	}

	var listed []string
	for _, c := range moneyColumns {
		listed = append(listed, c.name())
	}
	slices.Sort(found)
	slices.Sort(listed)
	if !slices.Equal(found, listed) {
		t.Errorf("INTEGER columns that are neither a key nor a timestamp = %v, moneyColumns = %v; "+
			"a new money column needs the ceiling CHECK and an entry in moneyColumns, a new non-money one needs this audit taught", found, listed)
	}
}

// TestCeilingLiteralIsMoneyMaxAmount pins the SQL literal to the Go constant:
// a CHECK cannot name money.MaxAmount, so the digits in the schema are a copy,
// and this is what notices them drift apart.
func TestCeilingLiteralIsMoneyMaxAmount(t *testing.T) {
	t.Parallel()
	body, err := migrationsFS.ReadFile("migrations/00001_schema.sql")
	if err != nil {
		t.Fatalf("reading the embedded schema = %v, want no error", err)
	}
	// Any run of nine or more digits in the schema is a ceiling; no other
	// figure that long belongs in it. Comments are skipped so the header can
	// quote the number.
	long := regexp.MustCompile(`\d{9,}`)
	var literals []string
	for _, line := range strings.Split(string(body), "\n") {
		code, _, _ := strings.Cut(line, "--")
		literals = append(literals, long.FindAllString(code, -1)...)
	}
	// dues_rate, reimbursement, transaction, target and minimum take one each;
	// recorded_amount and actual_amount take two each (BETWEEN -x AND x).
	// difference_amount takes none: it is derived (see moneyColumn.derivedFrom).
	if want := 5 + 2*2; len(literals) != want {
		t.Errorf("ceiling literals in the schema = %d (%v), want %d", len(literals), literals, want)
	}
	for _, lit := range literals {
		n, err := strconv.ParseInt(lit, 10, 64)
		if err != nil || n != ceiling {
			t.Errorf("schema literal %s = %d (err %v), want money.MaxAmount = %d", lit, n, err, ceiling)
		}
	}
}

// TestMoneyColumnBoundaries is the per-column proof, at the schema, for every
// column that carries its own ceiling: the ceiling is accepted, one rupiah more is refused by the ceiling CHECK itself,
// and for a signed column so are -max (accepted) and -max-1 (refused).
func TestMoneyColumnBoundaries(t *testing.T) {
	t.Parallel()
	for _, c := range moneyColumns {
		if c.derivedFrom != "" {
			continue // no literal of its own: TestDerivedDifferenceIsBoundedByItsOperands
		}
		t.Run(c.name(), func(t *testing.T) {
			t.Parallel()
			w := newCeilingWriter(t)

			if err := c.insert(t, w, ceiling); err != nil {
				t.Errorf("%s = max (%d): %v, want it accepted", c.name(), ceiling, err)
			}
			assertCeilingRefusal(t, c, c.insert(t, w, ceiling+1), ceiling+1)

			if c.signed {
				if err := c.insert(t, w, -ceiling); err != nil {
					t.Errorf("%s = -max (%d): %v, want it accepted", c.name(), -ceiling, err)
				}
				assertCeilingRefusal(t, c, c.insert(t, w, -ceiling-1), -ceiling-1)
			}
		})
	}
}

// TestDerivedDifferenceIsBoundedByItsOperands: difference_amount has no ceiling
// of its own. A legitimate line can have recorded = -max and actual = +max, a
// difference of twice the ceiling, and the schema accepts it (and the mirror).
// What bounds the column is the equality CHECK over the two bounded operands,
// so a difference that is not actual - recorded is still refused, and an
// operand past the ceiling is refused by its own CHECK (TestMoneyColumnBoundaries).
func TestDerivedDifferenceIsBoundedByItsOperands(t *testing.T) {
	t.Parallel()
	w := newCeilingWriter(t)

	if err := w.line(t, -ceiling, ceiling, 2*ceiling); err != nil {
		t.Errorf("recorded -max, actual +max, difference 2*max: %v, want it accepted", err)
	}
	if err := w.line(t, ceiling, -ceiling, -2*ceiling); err != nil {
		t.Errorf("recorded +max, actual -max, difference -2*max: %v, want it accepted", err)
	}
	if err := w.line(t, -ceiling, ceiling, 2*ceiling+1); err == nil ||
		!strings.Contains(err.Error(), "difference_amount = actual_amount - recorded_amount") {
		t.Errorf("difference 2*max+1 against actual - recorded = 2*max: %v, want the equality CHECK to refuse it", err)
	}
}

// assertCeilingRefusal wants the refusal to be this column's ceiling CHECK, not
// some other constraint the test row happened to trip: the message names the
// column and the literal, and only a ceiling CHECK has both.
func assertCeilingRefusal(t *testing.T, c moneyColumn, err error, v int64) {
	t.Helper()
	if err == nil {
		t.Errorf("%s = %d: nil error, want the ceiling CHECK to refuse it", c.name(), v)
		return
	}
	msg := err.Error()
	if !strings.Contains(msg, "CHECK constraint failed") || !strings.Contains(msg, c.column) || !strings.Contains(msg, "999999999999") {
		t.Errorf("%s = %d: error %q, want a CHECK failure naming %s and the ceiling", c.name(), v, msg, c.column)
	}
}

// TestUnsignedColumnsStillRefuseWhatTheyRefusedBefore: the ceiling did not
// loosen the sign checks. A transaction, a claim and an envelope figure stay
// strictly positive; a dues rate may be zero.
func TestUnsignedColumnsStillRefuseWhatTheyRefusedBefore(t *testing.T) {
	t.Parallel()
	w := newCeilingWriter(t)
	for _, c := range moneyColumns {
		if c.signed {
			continue
		}
		if err := c.insert(t, w, -1); err == nil {
			t.Errorf("%s = -1: nil error, want a refusal", c.name())
		}
		zero := c.insert(t, w, 0)
		if c.table == "dues_rate" {
			if zero != nil {
				t.Errorf("%s = 0: %v, want a free tier's zero rate accepted", c.name(), zero)
			}
		} else if zero == nil {
			t.Errorf("%s = 0: nil error, want a refusal", c.name())
		}
	}
}
