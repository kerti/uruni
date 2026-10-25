package ledger

import (
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// refuseWrite names the constraint failure of an insert or an update whose
// input the caller supplied: the schema's CHECK, UNIQUE and FOREIGN KEY are the
// guarantee (ADR-027), and this only gives their refusals a domain name, so no
// driver error crosses the package boundary for them. The driver's own error
// stays in the chain for the log.
//
// It reads the extended result code, never the message, which is not stable
// across driver releases. Any other error, a trigger's RAISE included, passes
// through unchanged.
func refuseWrite(err error) error {
	switch constraintCode(err) {
	case sqlite3.SQLITE_CONSTRAINT_CHECK:
		return fmt.Errorf("%w: %w", ErrDataConstraint, err)
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE:
		return fmt.Errorf("%w: %w", ErrValueTaken, err)
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		// On a write the reference is a field the caller named, so the row it names
		// does not exist.
		return fmt.Errorf("%w: %w", ErrReferenceMissing, err)
	}
	return err
}

// refuseDelete is refuseWrite for a DELETE. SQLite has one result code for a
// foreign key, with two opposite meanings: on a write the row named does not
// exist; on a delete the row exists and other rows still name it.
func refuseDelete(err error) error {
	if constraintCode(err) == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		return fmt.Errorf("%w: %w", ErrReferencedByOtherRecords, err)
	}
	return refuseWrite(err)
}

// constraintCode is the driver's extended result code of err, or 0 when err is
// not a driver error.
func constraintCode(err error) int {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()
	}
	return 0
}
