package http

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// sessionStore adapts #113's session table (store.Queries: CreateSession,
// GetSession, DeleteSession, DeleteExpiredSessions) to scs.Store's
// context-aware interface. This is our own ~30-line implementation, not
// scs's bundled sqlite3store: that package's hand-written SQL would sit
// outside ADR-005's reviewed-SQL discipline, where every query lives in
// internal/store/queries and is checked in with the rest of the schema.
type sessionStore struct {
	q store.Querier
}

func newSessionStore(q store.Querier) *sessionStore {
	return &sessionStore{q: q}
}

// FindCtx returns a session's data only while it is still inside its idle
// window - GetSession's own WHERE clause (#113) - so an expired-but-not-yet-
// swept row reads as "not found," exactly as scs's Store.Find contract asks.
func (s *sessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	sess, err := s.q.GetSession(ctx, store.GetSessionParams{Token: token, ExpiresAt: time.Now().Unix()})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return sess.Data, true, nil
}

// CommitCtx writes a session through one atomic UPSERT (store's
// UpsertSession). scs re-sends the whole encoded session on every commit,
// including a pure sliding-idle-timeout refresh where only the expiry moved
// (session.go's newSessionManager sets IdleTimeout, which marks every loaded
// session Modified in scs's own data.go) - so a token is rewritten on
// essentially every authenticated request. A delete-then-insert pair looks
// equivalent and is not: ADR-004's SetMaxOpenConns(1) serialises statements
// but holds nothing across them, so two concurrent requests carrying the
// same cookie - two panels fetching in parallel, two tabs - interleave as
// DELETE / DELETE / INSERT / INSERT and the second INSERT trips
// session.token's primary key, failing one of the two requests with a 500
// through scs's default ErrorFunc. Measured at 7 failures in 60 concurrent
// requests before this became one statement.
//
// The lazy sweep (#113's own doc comment: "never a background ticker",
// ADR-013's scope) rides on this same write rather than getting a ticker of
// its own. It targets only rows other than this token, so it stays outside
// the window the upsert closes.
func (s *sessionStore) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	if _, err := s.q.UpsertSession(ctx, store.UpsertSessionParams{
		Token: token, Data: b, ExpiresAt: expiry.Unix(),
	}); err != nil {
		return err
	}
	return s.q.DeleteExpiredSessions(ctx, time.Now().Unix())
}

func (s *sessionStore) DeleteCtx(ctx context.Context, token string) error {
	return s.q.DeleteSession(ctx, token)
}

// Find, Commit and Delete satisfy scs.Store's plain (non-ctx) signature, so
// *sessionStore can be assigned to SessionManager.Store, which is typed as
// scs.Store. scs's own runtime type-assertion (its data.go) prefers the Ctx
// forms above whenever a store implements them, so in production these
// three are never actually called - they exist only so the type compiles
// against the field it's assigned to.
func (s *sessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *sessionStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

func (s *sessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}
