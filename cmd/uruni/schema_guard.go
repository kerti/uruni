package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/db"
)

// ErrSchemaMismatch is returned when the database's schema is not the one this
// binary creates. A sentinel so callers and tests branch on identity.
var ErrSchemaMismatch = errors.New("database schema does not match this version")

// schemaDiffNamesShown caps how many differing objects the refusal names:
// enough to tell a one-column drift from another vintage, few enough for one
// readable line.
const schemaDiffNamesShown = 5

// verifySchema is the ADR-025 guard. goose cannot see that the one migration
// file was edited in place, so after migrating, `serve` (and any command that
// writes) asks whether the live schema is this binary's, and refuses if not.
// The error is the one line the operator sees: it names the database, which
// objects differ (names only, never DDL) and the way across, `uruni rebuild`.
func verifySchema(ctx context.Context, sqlDB *sql.DB, dbPath string) error {
	diff, err := db.CheckSchema(ctx, sqlDB)
	if err != nil {
		return err
	}
	if diff.Matches() {
		return nil
	}
	return fmt.Errorf("%w: %s differs (%s) - "+
		"this is a schema-changing 0.x upgrade (ADR-025): with the app stopped, run `uruni rebuild --from <backup.zip>` on a backup the previous version wrote; "+
		"see the Upgrading section of SELF-HOSTING.md",
		ErrSchemaMismatch, dbPath, diff.Summary(schemaDiffNamesShown))
}
