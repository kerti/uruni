// Package auth is the bootstrap account: argon2id password hashing and the
// one-shot registration ADR-030 decision 2 requires. It follows
// internal/ledger's shape (ADR-027): a Querier for reads, the underlying
// *sql.DB for a real *sql.Tx on writes, because Register needs the pattern
// SetUpFund proved: a row-count check and an insert in one transaction, so the
// refusal is keyed to the count, not a duplicate-identifier collision.
//
// It is not internal/ledger. Nothing here touches a fund, and ADR-030
// decision 2 says it must not: "user" is the one table with no fund_id.
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
// complexity policy and this is not one: it guards against hashing an empty or
// trivially short string into the one row a stranger could log in with. 8
// matches NIST 800-63B's minimum.
const MinPasswordLength = 8

// NormalizeEmail is the one form an email takes everywhere it is stored,
// looked up or counted (#500): trimmed, then lower-cased. Register and
// SetLogin store it, Authenticate looks it up, and the login limiter keys on
// it, so "Treasurer@Example.org " and "treasurer@example.org" are one account
// and one rate-limit key, not a wrong password and a fresh budget. Callers
// outside this package (the HTTP limiter) must use this, not their own
// TrimSpace, so the two cannot disagree.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Auth is the bootstrap-account service.
type Auth struct {
	db *sql.DB
	q  store.Querier

	// slots, slotWait, hashFn and verifyFn are password_gate.go's: the semaphore
	// every argon2id computation runs behind (#495), and the computations
	// themselves, replaceable only by a test.
	slots    chan struct{}
	slotWait time.Duration
	hashFn   func(password string) (string, error)
	verifyFn func(password, encoded string) (bool, error)
}

// New returns an Auth over an already-open database. It does not migrate:
// callers run internal/db.Up, as for internal/ledger.New.
//
// One Auth is one process-wide gate on password hashing and verifying
// (password_gate.go): share one, never one per request.
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

// withTx runs fn inside one database transaction, committing on nil and
// rolling back otherwise. Copied from internal/ledger.Ledger.withTx (ADR-027)
// rather than shared: sharing would mean importing internal/ledger for a
// six-line helper or moving it somewhere both import, and ADR-030 decision 2
// keeps this package's one table apart from the fund-scoped ones ledger owns.
func (a *Auth) withTx(ctx context.Context, fn func(store.Querier) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
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

// Register creates the one account a fresh Uruni instance will ever mint
// through this path (ADR-030 decision 2, #114). The row-count check and the
// insert run in one *sql.Tx, like SetUpFund (internal/ledger/setup.go): the
// refusal must be keyed to the row count, never a duplicate email hitting the
// UNIQUE index, because a second stranger reaching a fresh instance registers
// with a different address and would sail past a uniqueness check, after
// which resolveFund hands that account funds[0] with full write access to the
// ledger. Only a count inside the insert's transaction promises that: two
// statements outside one are not atomic just because ADR-004's
// SetMaxOpenConns(1) forbids a concurrent writer; the call could still be
// retried mid-flight by the same caller.
//
// The UNIQUE index on email stays as a backstop this check should make
// unreachable.
func (a *Auth) Register(ctx context.Context, email, password string) (store.User, error) {
	email = NormalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return store.User{}, fmt.Errorf("%w: email must be a valid address", ErrInvalidArgument)
	}
	if len(password) < MinPasswordLength {
		return store.User{}, fmt.Errorf("%w: password must be at least %d characters", ErrInvalidArgument, MinPasswordLength)
	}

	// A cheap pre-check before any hashing (#495): on an instance already set up,
	// a refused register costs one COUNT, not an argon2id computation. It is an
	// optimisation only (two requests can both pass on a fresh instance); the
	// count in the transaction stays authoritative.
	existing, err := a.q.CountUsers(ctx)
	if err != nil {
		return store.User{}, fmt.Errorf("checking for an existing account: %w", err)
	}
	if existing > 0 {
		return store.User{}, ErrAlreadyRegistered
	}

	// Hashed before the transaction opens: argon2id is deliberately slow
	// (password.go) and holding ADR-004's one write connection for it gains
	// nothing.
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
