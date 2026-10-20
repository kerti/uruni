package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/money"
)

// ErrStaleBackup is the gate's refusal when the backup and the database hold a
// different number of rows in a table both have: the backup is older than the
// database (entries would be lost), or holds more (another instance, or a
// database that was reset).
var ErrStaleBackup = errors.New("the backup does not match the database it would replace")

// ErrOtherInstance is the gate's refusal when the backup names a different
// fund or a different login than the database: it is another instance's.
var ErrOtherInstance = errors.New("the backup belongs to a different instance than this database")

// ErrCannotCompare is the gate failing closed: a table the gate must be able to
// read in the old database could not be.
var ErrCannotCompare = errors.New("the backup cannot be compared with this database")

// gateTable is one table both the backup and the database hold: the backup's
// row count against the database's. Names are the schema's, quoted wherever
// they are put in SQL ("transaction" and "user" are keywords).
type gateTable struct {
	name  string
	count int64
	// must is true for the four tables the gate refuses to run without: a
	// comparison that skips the ledger, the funds, the login or the members is
	// not one.
	must bool
}

// gateTables lists what the backup holds, in Document's order. session and
// goose's table are left out on purpose: a session is never exported and
// goose's table is bookkeeping, so neither can say anything about the data.
func gateTables(doc backup.Document) []gateTable {
	return []gateTable{
		{"user", int64(len(doc.Users)), true},
		{"fund", int64(len(doc.Funds)), true},
		{"account", int64(len(doc.Accounts)), false},
		{"purpose", int64(len(doc.Purposes)), false},
		{"dues_tier", int64(len(doc.DuesTiers)), false},
		{"dues_rate", int64(len(doc.DuesRates)), false},
		{"member", int64(len(doc.Members)), true},
		{"member_tier", int64(len(doc.MemberTiers)), false},
		{"transfer", int64(len(doc.Transfers)), false},
		{"reimbursement", int64(len(doc.Reimbursements)), false},
		{"transaction", int64(len(doc.Transactions)), true},
		{"receipt", int64(len(doc.Receipts)), false},
		{"reconciliation", int64(len(doc.Reconciliations)), false},
		{"reconciliation_line", int64(len(doc.ReconciliationLines)), false},
		{"incidental", int64(len(doc.Incidentals)), false},
		{"incidental_recipient", int64(len(doc.IncidentalRecipients)), false},
	}
}

// gateReport is what the gate looked at, printed whether it passed or not: an
// operator told "it matched" deserves to know what "matched" covered.
type gateReport struct {
	compared    []string
	notCompared []string
}

// checkBackupMatchesDatabase is the staleness and identity gate. It reads the
// old database with raw SQL and quoted identifiers, never the store: the
// queries in internal/store are written against this binary's schema, and the
// old file is by definition not that. Read-only.
//
//   - Identity first: every fund's (id, report_slug) and the login's
//     (email, password_hash) must be the backup's. A different slug or login
//     is another instance's backup, whatever the counts say.
//   - Then row counts, equal in both directions, for every table both sides
//     hold except session and goose's table.
//   - It fails closed: if transaction, fund, user or member cannot be read, it
//     refuses (ErrCannotCompare) rather than skip them.
//
// What it cannot see is reported, not hidden: a rename or an edit that leaves
// every count alone (a member's name, a note) is undetectable.
func checkBackupMatchesDatabase(ctx context.Context, sqlDB *sql.DB, doc backup.Document) (gateReport, error) {
	var report gateReport

	existing, err := tableNames(ctx, sqlDB)
	if err != nil {
		return report, fmt.Errorf("%w: reading the table list: %v", ErrCannotCompare, err) //nolint:errorlint
	}

	counts := make(map[string]int64)
	for _, t := range gateTables(doc) {
		if !existing[t.name] {
			if t.must {
				return report, fmt.Errorf("%w: it has no %q table", ErrCannotCompare, t.name)
			}
			report.notCompared = append(report.notCompared, t.name+" (not in the database)")
			continue
		}
		n, err := countRows(ctx, sqlDB, t.name)
		if err != nil {
			if t.must {
				return report, fmt.Errorf("%w: reading %q: %v", ErrCannotCompare, t.name, err) //nolint:errorlint
			}
			report.notCompared = append(report.notCompared, t.name+" (could not be read)")
			continue
		}
		counts[t.name] = n
		report.compared = append(report.compared, t.name+" count")
	}

	identity, err := identityMismatches(ctx, sqlDB, doc)
	if err != nil {
		return report, err
	}
	report.compared = append(report.compared, "fund id and report link", "login email and password")
	report.notCompared = append(report.notCompared, "names, notes and amounts (an edit that adds or removes no row is undetectable)")
	if len(identity) > 0 {
		return report, fmt.Errorf("%w: %s - if this is the right database, move it aside and run rebuild again to build a new one from the backup",
			ErrOtherInstance, strings.Join(identity, "; "))
	}

	var diffs []string
	for _, t := range gateTables(doc) {
		have, ok := counts[t.name]
		switch {
		case !ok || have == t.count:
		case have > t.count:
			diffs = append(diffs, fmt.Sprintf("%s: the database has %d, the backup %d (the backup is older, %d would be lost)", t.name, have, t.count, have-t.count))
		default:
			diffs = append(diffs, fmt.Sprintf("%s: the database has %d, the backup %d (the backup holds more than this database)", t.name, have, t.count))
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		return report, fmt.Errorf("%w: %s - download a fresh backup from the old version, with the app stopped for good after it, and rebuild from that",
			ErrStaleBackup, strings.Join(diffs, "; "))
	}
	return report, nil
}

// identityMismatches compares the funds' (id, report_slug) and the login's
// (email, password_hash) with the backup's, as sets. A column that cannot be
// read fails closed.
func identityMismatches(ctx context.Context, sqlDB *sql.DB, doc backup.Document) ([]string, error) {
	var out []string

	haveFunds, err := pairSet(ctx, sqlDB, `SELECT CAST(id AS TEXT), report_slug FROM "fund"`)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the funds' report links: %v", ErrCannotCompare, err) //nolint:errorlint
	}
	wantFunds := make(map[string]bool, len(doc.Funds))
	for _, f := range doc.Funds {
		wantFunds[fmt.Sprintf("%d|%s", f.ID, f.ReportSlug)] = true
	}
	if !sameSet(haveFunds, wantFunds) {
		out = append(out, "the funds' ids or report links differ")
	}

	haveUsers, err := pairSet(ctx, sqlDB, `SELECT email, password_hash FROM "user"`)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the login: %v", ErrCannotCompare, err) //nolint:errorlint
	}
	wantUsers := make(map[string]bool, len(doc.Users))
	for _, u := range doc.Users {
		wantUsers[u.Email+"|"+u.PasswordHash] = true
	}
	if !sameSet(haveUsers, wantUsers) {
		out = append(out, "the login differs (another email, or the password was changed after the backup)")
	}
	return out, nil
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// pairSet runs a two-column query and returns its rows as "a|b" keys.
func pairSet(ctx context.Context, sqlDB *sql.DB, query string) (map[string]bool, error) {
	rows, err := sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	set := make(map[string]bool)
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		set[a+"|"+b] = true
	}
	return set, rows.Err()
}

func tableNames(ctx context.Context, sqlDB *sql.DB) (map[string]bool, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := make(map[string]bool)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names[n] = true
	}
	return names, rows.Err()
}

// countRows counts a table by name. The name comes from gateTables, never from
// input, and is quoted because "transaction" and "user" are keywords.
func countRows(ctx context.Context, sqlDB *sql.DB, table string) (int64, error) {
	var n int64
	err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&n)
	return n, err
}

// rawLedgerSum is the old database's whole ledger summed in SQL: money in
// minus money out, every fund. Best effort for the operator's cross-check: a
// schema it cannot read, or a sum that would overflow int64 (SQLite raises
// rather than wrap), is ok == false and printed as n/a.
func rawLedgerSum(ctx context.Context, sqlDB *sql.DB) (sum int64, ok bool) {
	// The sign CASE is deliberately hand-written against "transaction", not read from
	// ledger_entry: an independent cross-check, and an old database may predate the view.
	err := sqlDB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) FROM "transaction"`).Scan(&sum)
	return sum, err == nil
}

// backupTotal is the backup's own total: the sum of its per-fund balances, in
// checked integer arithmetic.
func backupTotal(doc backup.Document) (int64, error) {
	var total money.Amount
	for _, f := range doc.Totals.Funds {
		var err error
		if total, err = total.Add(money.FromDB(f.Balance)); err != nil {
			return 0, fmt.Errorf("summing the backup's fund totals: %w", err)
		}
	}
	return total.Int64(), nil
}

// formatRupiah renders whole rupiah for the operator's terminal: "Rp 1.250.000",
// "-Rp 40.000". Integer only; it works on the decimal digits, so the most
// negative int64 needs no negation.
func formatRupiah(v int64) string {
	digits := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-Rp " + b.String()
	}
	return "Rp " + b.String()
}
