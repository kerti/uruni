// Package ledger is Uruni's domain layer: derived balances, posting entries,
// transfer pairs, settling reimbursements, closing an incidental, and taking a
// reconciliation snapshot. It sits between internal/store's generated queries
// and the HTTP handlers, and holds every rule the schema cannot state.
//
// One package, not four. Every operation shares one primitive (insert one or
// more "transaction" rows in one database transaction) and one connection.
// Splitting into members/dues/incidental would draw boundaries around table
// names, not the one real seam, read versus write (ADR-027).
//
// Balances are never stored. They are derived by summing the ledger every
// time, because a stored total is a second source of truth that can disagree
// with the first (CLAUDE.md rule 2). The one exception is a reconciliation
// snapshot, which stores what was true at a past moment on purpose.
package ledger

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// Ledger is the domain service. It holds the generated queries for reads and
// the *sql.DB for writes, as a write needs a real *sql.Tx the Querier cannot
// start.
type Ledger struct {
	db *sql.DB
	q  store.Querier
}

// New returns a Ledger over an already-open database. It does not migrate:
// callers run internal/db.Up, as cmd/ does at boot.
func New(sqlDB *sql.DB) *Ledger {
	return &Ledger{db: sqlDB, q: store.New(sqlDB)}
}

// withTx runs fn inside one database transaction, committing on nil and
// rolling back otherwise.
//
// Every write goes through this, a single-row insert included: one uniform
// pattern, not a per-operation judgment of which write "needs" a transaction,
// and under ADR-004's SetMaxOpenConns(1) a transaction never contends with
// anything else, so it costs nothing.
func (l *Ledger) withTx(ctx context.Context, fn func(store.Querier) error) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning a transaction: %w", err)
	}

	// A no-op once Commit has run, and the safety net on every path returning
	// before it, a panic included.
	defer func() { _ = tx.Rollback() }()

	if err := fn(store.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}
