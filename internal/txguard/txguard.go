// Package txguard turns a deadlock into an error.
//
// The pool holds one connection (ADR-004). A function that has opened a
// transaction owns that connection, so any call inside it that goes to the pool
// instead of the transaction (a nested transaction, a read through the
// service's pool-bound Querier) waits forever for a connection its own caller
// holds. Nothing fails; the request hangs.
//
// The defence is two parts that must travel together: a context mark, set by
// whoever opens a transaction and handed on to the code running inside it, and
// a pool wrapper (Pool) that refuses a marked context. Marking only the
// context given to BeginTx does nothing, since the code inside the transaction
// uses the context its caller captured, not that one.
package txguard

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNested is what a pool call or a second transaction on a marked context
// returns instead of blocking on the connection its own caller holds.
var ErrNested = errors.New("a pool call inside an open transaction would deadlock on the single connection: use the transaction's own queries")

type markKey struct{}

// Mark returns ctx marked as running inside an open transaction. Hand the
// result to everything that runs inside it.
func Mark(ctx context.Context) context.Context {
	return context.WithValue(ctx, markKey{}, struct{}{})
}

// Marked reports whether ctx runs inside a transaction opened with Mark.
func Marked(ctx context.Context) bool {
	_, ok := ctx.Value(markKey{}).(struct{})
	return ok
}

// Pool is a *sql.DB that refuses a marked context. It satisfies store.DBTX, so
// store.New(txguard.Wrap(db)) is a Querier whose every call fails fast inside a
// transaction. The transaction's own Querier is built over the *sql.Tx and is
// not wrapped.
type Pool struct {
	db *sql.DB
}

// Wrap returns db guarded against marked contexts.
func Wrap(db *sql.DB) *Pool {
	return &Pool{db: db}
}

// ExecContext returns ErrNested on a marked context and otherwise delegates to
// the pool, as PrepareContext and QueryContext do.
func (p *Pool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if Marked(ctx) {
		return nil, ErrNested
	}
	return p.db.ExecContext(ctx, query, args...)
}

// PrepareContext refuses a marked context with ErrNested; see ExecContext.
func (p *Pool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if Marked(ctx) {
		return nil, ErrNested
	}
	return p.db.PrepareContext(ctx, query)
}

// QueryContext refuses a marked context with ErrNested; see ExecContext.
func (p *Pool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if Marked(ctx) {
		return nil, ErrNested
	}
	return p.db.QueryContext(ctx, query, args...)
}

// QueryRowContext cannot return an error: *sql.Row carries one only from the
// database/sql internals. On a marked context it hands the pool a context that
// is already done and whose Err is ErrNested, so database/sql refuses before it
// asks for a connection and Row.Scan returns ErrNested. The trade-off is that
// this context's Err is not Canceled or DeadlineExceeded as the context package
// documents, which is harmless here because only database/sql ever sees it, and
// it buys errors.Is(err, ErrNested) where context.WithCancelCause would surface
// a bare "context canceled".
func (p *Pool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if Marked(ctx) {
		ctx = refused{ctx}
	}
	return p.db.QueryRowContext(ctx, query, args...)
}

// refused is a context that is done from the start, with ErrNested as its Err.
type refused struct{ context.Context }

var doneChan = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

func (refused) Done() <-chan struct{} { return doneChan }
func (refused) Err() error            { return ErrNested }
