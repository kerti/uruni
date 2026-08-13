// Package ledger is Uruni's domain layer: derived balances, posting entries,
// transfer pairs, settling reimbursements, closing an incidental, and taking a
// reconciliation snapshot. It sits between internal/store's generated queries
// and M4's HTTP handlers, and holds every rule the schema cannot state.
//
// One package, not four. Every operation here shares one primitive — insert one
// or more "transaction" rows inside one database transaction — and one
// connection. Splitting into members/dues/incidental would draw boundaries
// around table names rather than around the one real seam, which is read versus
// write (ADR-027).
//
// Balances are never stored. They are derived by summing the ledger, every
// time, because a stored total is a second source of truth that can disagree
// with the first (CLAUDE.md rule 2). The single exception is a reconciliation
// snapshot, which stores what was true at a past moment on purpose.
package ledger

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// Ledger is the domain service. It holds the generated queries for reads and
// the underlying *sql.DB for writes, because a write needs a real *sql.Tx that
// the Querier interface alone cannot start.
type Ledger struct {
	db *sql.DB
	q  store.Querier
}

// New returns a Ledger over an already-open database. It does not migrate:
// callers run internal/db.Up themselves, exactly as cmd/ does at boot.
func New(sqlDB *sql.DB) *Ledger {
	return &Ledger{db: sqlDB, q: store.New(sqlDB)}
}

// withTx runs fn inside one database transaction, committing if it returns nil
// and rolling back otherwise.
//
// Every write goes through this, including a single-row insert. That is one
// uniform pattern instead of a per-operation judgment call about which write
// "needs" a transaction, and under ADR-004's SetMaxOpenConns(1) a transaction
// never contends with anything else the process is doing, so the uniformity
// costs nothing.
func (l *Ledger) withTx(ctx context.Context, fn func(store.Querier) error) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning a transaction: %w", err)
	}

	// A no-op once Commit has run, and the safety net on every path that
	// returns before it — including a panic.
	defer func() { _ = tx.Rollback() }()

	if err := fn(store.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}
