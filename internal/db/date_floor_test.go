package db

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// The schema half of the plausible date window (#632): a static floor, the year
// 2000, as a CHECK on every column that holds a business date or a period. The
// ceilings are code (see the schema header).

const (
	dayFloor   = "2000-01-01"
	dayBelow   = "1999-12-31"
	monthFloor = "2000-01"
	monthBelow = "1999-12"
)

// dateColumn is one column that holds a calendar day or a month, and how to
// write one row whose only unusual value is that column's. Every write is
// otherwise valid, so a refusal can only be the floor.
type dateColumn struct {
	table, column string
	month         bool // "YYYY-MM", else "YYYY-MM-DD"
	write         func(t *testing.T, w *floorWriter, v string) error
}

func (c dateColumn) name() string { return c.table + "." + c.column }

// dateColumns is the explicit list the audit below holds the schema to.
var dateColumns = []dateColumn{
	{table: "transaction", column: "occurred_on", write: func(_ *testing.T, w *floorWriter, v string) error {
		_, err := w.q.CreateTransaction(w.ctx, store.CreateTransactionParams{
			FundID: w.f.fundID, AccountID: w.f.accountID, PurposeID: w.f.purposeID,
			Direction: "in", Amount: 1, OccurredOn: v, Kind: "normal", CreatedAt: 1,
		})
		return err
	}},
	{table: "transaction", column: "dues_period", month: true, write: func(_ *testing.T, w *floorWriter, v string) error {
		_, err := w.q.CreateTransaction(w.ctx, store.CreateTransactionParams{
			FundID: w.f.fundID, AccountID: w.f.accountID, PurposeID: w.f.purposeID,
			Direction: "in", Amount: 1, OccurredOn: "2026-08-12", Kind: "dues", MemberID: &w.f.memberID, DuesPeriod: &v, CreatedAt: 1,
		})
		return err
	}},
	{table: "reimbursement", column: "incurred_on", write: func(_ *testing.T, w *floorWriter, v string) error {
		_, err := w.q.CreateReimbursement(w.ctx, store.CreateReimbursementParams{
			FundID: w.f.fundID, MemberID: w.f.memberID, PurposeID: w.f.purposeID, Amount: 1, IncurredOn: v, CreatedAt: 1,
		})
		return err
	}},
	{table: "reimbursement", column: "waived_on", write: func(t *testing.T, w *floorWriter, v string) error {
		id := createReimbursement(t, w.db, w.f.fundID, w.f.memberID, w.f.purposeID, 1)
		_, err := w.db.Exec(`UPDATE reimbursement SET waived_on = ? WHERE id = ?`, v, id)
		return err
	}},
	{table: "incidental", column: "opened_on", write: func(t *testing.T, w *floorWriter, v string) error {
		_, err := w.q.CreateIncidental(w.ctx, store.CreateIncidentalParams{
			PurposeID: createPurpose(t, w.db, w.f.fundID, "incidental", w.next("envelope")), Occasion: "Kurban", OpenedOn: v, CreatedAt: 1,
		})
		return err
	}},
	{table: "incidental", column: "closed_on", write: func(t *testing.T, w *floorWriter, v string) error {
		id := createPurpose(t, w.db, w.f.fundID, "incidental", w.next("envelope"))
		if _, err := w.q.CreateIncidental(w.ctx, store.CreateIncidentalParams{PurposeID: id, Occasion: "Kurban", OpenedOn: "2026-05-01", CreatedAt: 1}); err != nil {
			t.Fatalf("CreateIncidental = %v, want no error", err)
		}
		_, err := w.db.Exec(`UPDATE incidental SET closed_on = ? WHERE purpose_id = ?`, v, id)
		return err
	}},
	{table: "member", column: "joined_on", write: func(_ *testing.T, w *floorWriter, v string) error {
		_, err := w.q.CreateMember(w.ctx, store.CreateMemberParams{FundID: w.f.fundID, Name: "Siti", JoinedOn: v, CreatedAt: 1})
		return err
	}},
	{table: "member", column: "inactive_on", write: func(t *testing.T, w *floorWriter, v string) error {
		id := createMember(t, w.db, w.f.fundID, w.next("member"))
		_, err := w.db.Exec(`UPDATE member SET inactive_on = ? WHERE id = ?`, v, id)
		return err
	}},
	{table: "account", column: "inactive_on", write: func(t *testing.T, w *floorWriter, v string) error {
		id := createAccount(t, w.db, w.f.fundID, "cash", w.next("account"))
		_, err := w.db.Exec(`UPDATE account SET inactive_on = ? WHERE id = ?`, v, id)
		return err
	}},
	{table: "dues_rate", column: "effective_from", month: true, write: func(t *testing.T, w *floorWriter, v string) error {
		_, err := w.q.CreateDuesRate(w.ctx, store.CreateDuesRateParams{
			TierID: createDuesTier(t, w.db, w.f.fundID, w.next("tier")), Amount: 1, EffectiveFrom: v, CreatedAt: 1,
		})
		return err
	}},
	{table: "member_tier", column: "effective_from", month: true, write: func(t *testing.T, w *floorWriter, v string) error {
		tierID := createDuesTier(t, w.db, w.f.fundID, w.next("tier"))
		_, err := w.q.CreateMemberTier(w.ctx, memberTier(w.f.fundID, w.f.memberID, &tierID, v, 0))
		return err
	}},
}

// floorWriter writes the rows dateColumns describe, one fund underneath. The
// fixture member joined on the floor itself, so a tier starting there is legal.
type floorWriter struct {
	ctx context.Context
	db  *sql.DB
	q   *store.Queries
	f   ledgerFixture
	n   int
}

func newFloorWriter(t *testing.T) *floorWriter {
	t.Helper()
	sqlDB := migratedTestDB(t)
	return &floorWriter{
		ctx: context.Background(), db: sqlDB, q: store.New(sqlDB),
		f: newLedgerFixture(t, sqlDB, "Test Fund", validSlug),
	}
}

func (w *floorWriter) next(prefix string) string {
	w.n++
	return prefix + " " + strconv.Itoa(w.n)
}

// TestEveryDateColumnIsListed is the audit that does not rely on remembering a
// column: every TEXT column named for a day (`*_on`), a dues period or a rate's
// start month must be in dateColumns, and every listed column must still exist.
func TestEveryDateColumnIsListed(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)

	rows, err := sqlDB.Query(`
		SELECT m.name, p.name
		FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name <> 'goose_db_version'
		  AND p.type = 'TEXT'
		  AND (p.name LIKE '%\_on' ESCAPE '\' OR p.name IN ('dues_period', 'effective_from'))
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
		found = append(found, table+"."+column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the schema's columns = %v", err)
	}

	var listed []string
	for _, c := range dateColumns {
		listed = append(listed, c.name())
	}
	slices.Sort(found)
	slices.Sort(listed)
	if !slices.Equal(found, listed) {
		t.Errorf("date columns in the schema = %v, dateColumns = %v; "+
			"a new date column needs the 2000 floor CHECK and an entry in dateColumns", found, listed)
	}
}

// TestDateFloorLiteralsAreTheFloor pins the literals (a CHECK cannot name a Go
// constant): one floor per column, a day or a month as needed, and nothing else
// of that shape.
func TestDateFloorLiteralsAreTheFloor(t *testing.T) {
	t.Parallel()
	body, err := migrationsFS.ReadFile("migrations/00001_schema.sql")
	if err != nil {
		t.Fatalf("reading the embedded schema = %v, want no error", err)
	}
	literal := regexp.MustCompile(`'(\d{4}-\d{2}(?:-\d{2})?)'`)
	var days, months int
	for _, line := range strings.Split(string(body), "\n") {
		code, _, _ := strings.Cut(line, "--")
		for _, m := range literal.FindAllStringSubmatch(code, -1) {
			switch m[1] {
			case dayFloor:
				days++
			case monthFloor:
				months++
			default:
				t.Errorf("schema carries a date literal %q, want only the floor %s or %s", m[1], dayFloor, monthFloor)
			}
		}
	}
	var wantDays, wantMonths int
	for _, c := range dateColumns {
		if c.month {
			wantMonths++
		} else {
			wantDays++
		}
	}
	if days != wantDays || months != wantMonths {
		t.Errorf("floor literals in the schema = %d days, %d months, want %d and %d (one per listed column)", days, months, wantDays, wantMonths)
	}
}

// TestDateColumnFloors is the per-column proof: the floor itself is accepted
// and one step below it is refused by the floor CHECK.
func TestDateColumnFloors(t *testing.T) {
	t.Parallel()
	for _, c := range dateColumns {
		t.Run(c.name(), func(t *testing.T) {
			t.Parallel()
			w := newFloorWriter(t)
			floor, below := dayFloor, dayBelow
			if c.month {
				floor, below = monthFloor, monthBelow
			}

			if err := c.write(t, w, floor); err != nil {
				t.Errorf("%s = %s (the floor): %v, want it accepted", c.name(), floor, err)
			}
			err := c.write(t, w, below)
			if err == nil {
				t.Fatalf("%s = %s (one step below the floor): nil error, want the floor CHECK to refuse it", c.name(), below)
			}
			// The message quotes the CHECK's own text, so the floor literal in it proves
			// it was the floor that refused, not another constraint on the row.
			if msg := err.Error(); !strings.Contains(msg, "CHECK constraint failed") || !strings.Contains(msg, c.column) || !strings.Contains(msg, "'"+floor+"'") {
				t.Errorf("%s = %s: error %q, want a CHECK failure naming %s and the floor %s", c.name(), below, msg, c.column, floor)
			}
		})
	}
}
