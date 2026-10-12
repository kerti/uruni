package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrIntegrity is returned by QuickCheck when SQLite reports the database
// file damaged (#505).
var ErrIntegrity = errors.New("db: integrity check failed")

// quickCheckReportLimit caps how many of SQLite's problem rows reach the
// error. quick_check can list thousands on a badly damaged file; the first
// few say what kind of damage it is, and the operator's next step - restore
// a backup - is the same either way.
const quickCheckReportLimit = 5

// QuickCheck runs PRAGMA quick_check, SQLite's O(N) structural check of the
// whole file (it skips integrity_check's index-content comparison, which is
// what keeps it cheap enough to run at boot and before every daily dump).
// A healthy file answers with exactly one row, "ok"; anything else is a list
// of problems and is returned wrapped in ErrIntegrity.
func QuickCheck(ctx context.Context, sqlDB *sql.DB) error {
	rows, err := sqlDB.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return quickCheckError("running", err)
	}
	defer func() { _ = rows.Close() }()

	var problems []string
	total := 0
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return quickCheckError("reading", err)
		}
		if line == "ok" {
			continue
		}
		total++
		if len(problems) < quickCheckReportLimit {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return quickCheckError("reading", err)
	}
	if total == 0 {
		return nil
	}
	more := ""
	if total > len(problems) {
		more = fmt.Sprintf(" and %d more", total-len(problems))
	}
	return fmt.Errorf("%w: %s%s", ErrIntegrity, strings.Join(problems, "; "), more)
}

// SQLite's primary result codes for a damaged file. Damage bad enough to stop
// the walk surfaces as one of these errors rather than as problem rows, and
// it is the same finding.
const (
	sqliteCorrupt = 11 // SQLITE_CORRUPT
	sqliteNotADB  = 26 // SQLITE_NOTADB
)

// quickCheckError wraps err in ErrIntegrity when SQLite says the file is
// damaged, and leaves anything else - a cancelled context, a busy database -
// as the plain error it is, so a caller never reports corruption that isn't.
func quickCheckError(step string, err error) error {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff { // extended codes keep the primary in the low byte
		case sqliteCorrupt, sqliteNotADB:
			return fmt.Errorf("%w: %v", ErrIntegrity, err) //nolint:errorlint // ErrIntegrity is what callers match
		}
	}
	return fmt.Errorf("db: %s quick_check: %w", step, err)
}
