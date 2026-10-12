// Package auth is the bootstrap account: argon2id password hashing and the
// one-shot registration ADR-030 decision 2 requires. It follows
// internal/ledger's own shape (ADR-027) deliberately - a Querier for reads,
// the underlying *sql.DB for a real *sql.Tx on writes - because Register
// needs exactly the pattern SetUpFund already proved: a row-count check and
// an insert inside one transaction, so the refusal is keyed to the count
// and not to a duplicate-identifier collision.
//
// This is not internal/ledger itself. Nothing here touches a fund, and
// ADR-030 decision 2 is explicit that it must not: "user" is the one table
// in the schema with no fund_id at all.
package auth

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// MinPasswordLength is the one floor Register enforces. PRD/ADR-007 name no
// complexity policy - this is not one either, just a guard against hashing
// an empty or trivially short string into the one row a stranger could then
// log in with. 8 matches NIST 800-63B's own stated minimum.
const MinPasswordLength = 8

// NormalizeEmail is the one form an email takes everywhere it is stored,
// looked up or counted (#500): trimmed, then lower-cased. Register and
// SetLogin store it, Authenticate looks it up, and the login limiter keys on
// it, so "Treasurer@Example.org " and "treasurer@example.org" are one account
// and one rate-limit key rather than a wrong password and a fresh budget.
// Callers outside this package (the HTTP limiter) must use this, not their
// own TrimSpace, so the two can never disagree.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Auth is the bootstrap-account service.
type Auth struct {
	db *sql.DB
	q  store.Querier

	// slots, slotWait, hashFn and verifyFn are password_gate.go's: the
	// semaphore every argon2id computation runs behind (#495), and the
	// computations themselves, replaceable only by a test.
	slots    chan struct{}
	slotWait time.Duration
	hashFn   func(password string) (string, error)
	verifyFn func(password, encoded string) (bool, error)
}

// New returns an Auth over an already-open database. It does not migrate:
// callers run internal/db.Up themselves, exactly as internal/ledger.New
// documents for itself.
//
// One Auth is one process-wide gate on password hashing and verifying
// (password_gate.go): build one and share it, never one per request.
func New(sqlDB *sql.DB, opts ...Option) *Auth {
	a := &Auth{
		db:       sqlDB,
		q:        store.New(sqlDB),
		slots:    make(chan struct{}, defaultMaxConcurrentHashes()),
		slotWait: defaultSlotWait,
		hashFn:   hashPassword,
		verifyFn: verifyPassword,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// withTx runs fn inside one database transaction, committing if it returns
// nil and rolling back otherwise. Copied from internal/ledger.Ledger.withTx
// (ADR-027) rather than shared, because sharing it would mean either this
// package importing internal/ledger for a six-line helper or the helper
// moving somewhere both packages import - and ADR-030 decision 2 is explicit
// that this package's one table stands apart from every fund-scoped one
// ledger owns, so the two are kept as separate a boundary as the schema
// already draws.
func (a *Auth) withTx(ctx context.Context, fn func(store.Querier) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning a transaction: %w", err)
	}

	// A no-op once Commit has run, and the safety net on every path that
	// returns before it - including a panic.
	defer func() { _ = tx.Rollback() }()

	if err := fn(store.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// Register creates the one account a fresh Uruni instance will ever mint
// through this path (ADR-030 decision 2, issue #114). The row-count check
// and the insert run inside one *sql.Tx, exactly like SetUpFund
// (internal/ledger/setup.go): the refusal has to be keyed to the row count,
// never to a duplicate email colliding with the schema's UNIQUE index,
// because a second stranger to reach a fresh instance registers with a
// *different* address and would otherwise sail past a uniqueness check, at
// which point resolveFund hands that account funds[0] with full write
// access to the treasurer's ledger. Only a count taken inside the same
// transaction as the insert can promise that - two sequential statements
// outside one transaction are not atomic just because ADR-004's
// SetMaxOpenConns(1) makes a *concurrent* second writer impossible; nothing
// stops this call from being retried mid-flight by the same caller.
//
// The UNIQUE index on email stays in the schema regardless, as a backstop
// this check should make it impossible to ever reach.
func (a *Auth) Register(ctx context.Context, email, password string) (store.User, error) {
	email = NormalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return store.User{}, fmt.Errorf("%w: email must be a valid address", ErrInvalidArgument)
	}
	if len(password) < MinPasswordLength {
		return store.User{}, fmt.Errorf("%w: password must be at least %d characters", ErrInvalidArgument, MinPasswordLength)
	}

	// A cheap pre-check, before any hashing (#495): on an instance that is
	// already set up, a refused register must cost one COUNT, not one
	// argon2id computation. It is only an optimisation - two requests can
	// both pass it on a fresh instance - so the count inside the transaction
	// below stays the authoritative check.
	existing, err := a.q.CountUsers(ctx)
	if err != nil {
		return store.User{}, fmt.Errorf("checking for an existing account: %w", err)
	}
	if existing > 0 {
		return store.User{}, ErrAlreadyRegistered
	}

	// Hashed before the transaction opens: argon2id is deliberately slow -
	// that cost is the entire point of the parameters in password.go - and
	// nothing is gained by holding ADR-004's one write connection for the
	// time that work takes.
	hash, err := a.hash(ctx, password)
	if err != nil {
		return store.User{}, fmt.Errorf("hashing password: %w", err)
	}

	var user store.User
	err = a.withTx(ctx, func(q store.Querier) error {
		count, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("checking for an existing account: %w", err)
		}
		if count > 0 {
			return ErrAlreadyRegistered
		}

		user, err = q.CreateUser(ctx, store.CreateUserParams{
			Email:        email,
			PasswordHash: hash,
			CreatedAt:    time.Now().Unix(),
		})
		if err != nil {
			return fmt.Errorf("creating user: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.User{}, fmt.Errorf("registering: %w", err)
	}
	return user, nil
}
