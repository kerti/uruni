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

// schemaDiffNamesShown caps how many differing objects the refusal names: enough
// to tell a one-column drift from a database of another vintage, few enough to
// stay one readable line.
const schemaDiffNamesShown = 5

// verifySchema is the ADR-025 guard. goose cannot see that the one migration
// file was edited in place, so after migrating, `serve` (and any command that
// writes) asks whether the live schema is the one this binary creates, and
// refuses if not. The error is the single line the operator sees: it names the
// database, which objects differ (names only, never DDL) and where the upgrade
// notes are.
func verifySchema(ctx context.Context, sqlDB *sql.DB, dbPath string) error {
	diff, err := db.CheckSchema(ctx, sqlDB)
	if err != nil {
		return err
	}
	if diff.Matches() {
		return nil
	}
	return fmt.Errorf("%w: %s differs (%s) - "+
		"0.x upgrades can need a fresh database, so read the release notes and the Upgrading section of SELF-HOSTING.md (ADR-025) before restarting",
		ErrSchemaMismatch, dbPath, diff.Summary(schemaDiffNamesShown))
}
