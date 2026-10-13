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
// context-aware interface. It is our ~30-line implementation, not scs's
// sqlite3store, whose hand-written SQL would sit outside ADR-005's
// reviewed-SQL discipline (every query lives in internal/store/queries).
type sessionStore struct {
	q store.Querier
}

func newSessionStore(q store.Querier) *sessionStore {
	return &sessionStore{q: q}
}

// FindCtx returns a session's data only inside its idle window (GetSession's
// WHERE, #113), so an expired-but-unswept row reads as "not found", as scs's
// Store.Find contract asks.
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

// CommitCtx writes a session through one atomic UPSERT (UpsertSession). scs
// re-sends the whole encoded session on every commit, including a pure
// sliding-idle-timeout refresh (session.go's newSessionManager sets
// IdleTimeout, which marks every loaded session Modified in scs's data.go), so
// a token is rewritten on nearly every authenticated request. A
// delete-then-insert pair is not equivalent: ADR-004's SetMaxOpenConns(1)
// serialises statements but holds nothing across them, so two concurrent
// requests with the same cookie (two panels fetching, two tabs) interleave as
// DELETE / DELETE / INSERT / INSERT and the second INSERT trips session.token's
// primary key, failing a request with a 500 through scs's default ErrorFunc
// (7 failures in 60 concurrent requests before this became one statement).
//
// The lazy sweep (#113: "never a background ticker", ADR-013) rides on this
// write. It targets only other tokens' rows, so it stays outside the window
// the upsert closes.
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
// *sessionStore can be assigned to SessionManager.Store (typed scs.Store).
// scs's runtime type-assertion (data.go) prefers the Ctx forms above, so in
// production these are never called; they exist so the type compiles.
func (s *sessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *sessionStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

func (s *sessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}
