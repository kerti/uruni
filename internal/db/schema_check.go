package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
)

// ADR-025 keeps one migration file and edits it in place through 0.x. goose
// tracks applied migrations by number, so a database that already ran 00001
// never sees that file change: a release editing the schema boots on the old
// one and /healthz says ok. CheckSchema compares the live schema with the one
// this binary would create, so `serve` can refuse to start instead of handing
// the treasurer a 500.

// gooseTable is goose's bookkeeping table: the runner's, not the schema's, so
// excluded from the comparison.
const gooseTable = "goose_db_version"

// schemaObject is one row of sqlite_schema: a table, index, view or trigger.
type schemaObject struct {
	Type    string
	Name    string
	TblName string
	SQL     string
}

// label is how an object is named to the operator: "table fund", "index
// session_by_expiry". Type plus name is unique within a schema.
func (o schemaObject) label() string { return o.Type + " " + o.Name }

// SchemaDiff is the difference between the live schema and the one this binary
// creates. Each list is sorted and holds "type name" labels - names only, never
// DDL, so it is safe to put in a log line.
type SchemaDiff struct {
	// Missing objects this binary creates but the database lacks.
	Missing []string
	// Unexpected objects the database has but this binary does not create.
	Unexpected []string
	// Changed objects present in both whose definition (or owning table) differs.
	Changed []string
}

// Matches reports whether the live schema is the one this binary creates.
func (d SchemaDiff) Matches() bool {
	return len(d.Missing) == 0 && len(d.Unexpected) == 0 && len(d.Changed) == 0
}

// Summary names the differing objects in one short phrase, at most limit of
// them, e.g. "table fund changed, index x missing, and 2 more". Empty when the
// schemas match. Changed objects come first: a changed column is the usual
// shape of a stale database.
func (d SchemaDiff) Summary(limit int) string {
	var parts []string
	for _, n := range d.Changed {
		parts = append(parts, n+" changed")
	}
	for _, n := range d.Missing {
		parts = append(parts, n+" missing")
	}
	for _, n := range d.Unexpected {
		parts = append(parts, n+" unexpected")
	}
	if limit > 0 && len(parts) > limit {
		more := len(parts) - limit
		parts = append(parts[:limit], fmt.Sprintf("and %d more", more))
	}
	return strings.Join(parts, ", ")
}

// CheckSchema compares sqlDB's schema with the one the embedded migrations
// create. The expected schema is built by running those migrations into a
// throwaway in-memory database on the same driver, so "what this binary
// creates" is measured, not restated. Read-only against sqlDB.
//
// Compared: type, name, owning table and DDL text of every table, index, view
// and trigger in sqlite_schema, sorted by type and name. Left out: SQLite's
// objects (sqlite_sequence, sqlite_stat*, autoindexes whose sql is NULL) and
// goose's version table.
//
// DDL normalisation: SQLite stores each statement verbatim, so two databases
// from the same file hold identical text. The trap is that 00001_schema.sql
// has comments inside its CREATE statements, which SQLite keeps, so a
// comment-only edit would read as a schema change and refuse to boot an
// instance whose structure is right. normalizeDDL therefore drops comments and
// collapses whitespace runs, nothing else: string literals and quoted
// identifiers stay byte for byte.
func CheckSchema(ctx context.Context, sqlDB *sql.DB) (SchemaDiff, error) {
	want, err := expectedSchema(ctx)
	if err != nil {
		return SchemaDiff{}, err
	}
	got, err := readSchema(ctx, sqlDB)
	if err != nil {
		return SchemaDiff{}, err
	}
	return compareSchemas(want, got), nil
}

// expectedSchema builds the schema this binary creates in a private in-memory
// database.
func expectedSchema(ctx context.Context) ([]schemaObject, error) {
	mem, err := Open(ctx, ":memory:")
	if err != nil {
		return nil, fmt.Errorf("building the expected schema: %w", err)
	}
	defer func() { _ = mem.Close() }()

	if _, err := Up(ctx, mem, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		return nil, fmt.Errorf("building the expected schema: %w", err)
	}
	return readSchema(ctx, mem)
}

// readSchema returns the schema objects of sqlDB, minus SQLite's internals and
// goose's table, with DDL already normalised.
func readSchema(ctx context.Context, sqlDB *sql.DB) ([]schemaObject, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT type, name, tbl_name, sql FROM sqlite_schema
		  WHERE sql IS NOT NULL
		    AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		    AND name <> ? AND tbl_name <> ?
		  ORDER BY type, name`, gooseTable, gooseTable)
	if err != nil {
		return nil, fmt.Errorf("reading the database schema: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var objects []schemaObject
	for rows.Next() {
		var o schemaObject
		if err := rows.Scan(&o.Type, &o.Name, &o.TblName, &o.SQL); err != nil {
			return nil, fmt.Errorf("reading the database schema: %w", err)
		}
		o.SQL = normalizeDDL(o.SQL)
		objects = append(objects, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the database schema: %w", err)
	}
	return objects, nil
}

// compareSchemas is the pure comparison: want is what this binary creates, got
// is what the database holds.
func compareSchemas(want, got []schemaObject) SchemaDiff {
	gotBy := make(map[string]schemaObject, len(got))
	for _, o := range got {
		gotBy[o.label()] = o
	}

	var d SchemaDiff
	seen := make(map[string]bool, len(want))
	for _, w := range want {
		seen[w.label()] = true
		g, ok := gotBy[w.label()]
		switch {
		case !ok:
			d.Missing = append(d.Missing, w.label())
		case g.TblName != w.TblName || g.SQL != w.SQL:
			d.Changed = append(d.Changed, w.label())
		}
	}
	for _, g := range got {
		if !seen[g.label()] {
			d.Unexpected = append(d.Unexpected, g.label())
		}
	}

	sort.Strings(d.Missing)
	sort.Strings(d.Unexpected)
	sort.Strings(d.Changed)
	return d
}

// normalizeDDL drops SQL comments (-- to end of line, /* ... */) and collapses
// every whitespace run to one space, trimming the ends. Single-quoted strings,
// double-quoted, backtick and [bracketed] identifiers are copied untouched, so
// a '--' or double space inside a literal or CHECK string survives.
func normalizeDDL(ddl string) string {
	var b strings.Builder
	b.Grow(len(ddl))
	pendingSpace := false

	emit := func(s string) {
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteString(s)
	}

	for i := 0; i < len(ddl); {
		c := ddl[i]
		switch {
		case c == '-' && i+1 < len(ddl) && ddl[i+1] == '-':
			for i < len(ddl) && ddl[i] != '\n' {
				i++
			}
			pendingSpace = true
		case c == '/' && i+1 < len(ddl) && ddl[i+1] == '*':
			end := strings.Index(ddl[i+2:], "*/")
			if end < 0 {
				i = len(ddl)
			} else {
				i += 2 + end + 2
			}
			pendingSpace = true
		case c == '\'' || c == '"' || c == '`' || c == '[':
			closer := c
			if c == '[' {
				closer = ']'
			}
			j := i + 1
			for j < len(ddl) {
				if ddl[j] == closer {
					// A doubled quote is an escaped quote, not the end.
					if closer != ']' && j+1 < len(ddl) && ddl[j+1] == closer {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			emit(ddl[i:j])
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			pendingSpace = true
			i++
		default:
			emit(ddl[i : i+1])
			i++
		}
	}
	return b.String()
}
