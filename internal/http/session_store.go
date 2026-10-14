package http

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/kerti/uruni/internal/store"
)

// sessionStore adapts #113's session table (store.Queries: CreateSession,
// UpdateSession, GetSession, DeleteSession, DeleteExpiredSessions) to scs.Store's
// context-aware interface. It is our ~30-line implementation, not scs's
// sqlite3store, whose hand-written SQL would sit outside ADR-005's
// reviewed-SQL discipline (every query lives in internal/store/queries).
type sessionStore struct {
	q store.Querier
}

// sessionLoads is the set of tokens one request found in the store. A commit
// updates those and inserts the rest (CommitCtx). It is per request and
// mutex-guarded, since a handler may touch the session from more than one
// goroutine.
type sessionLoads struct {
	mu     sync.Mutex
	tokens map[string]struct{}
}

type sessionLoadsKey struct{}

// errNoSessionLoads is CommitCtx's refusal when its context carries no
// sessionLoads: a wiring bug, surfaced as a 500 rather than guessed past.
var errNoSessionLoads = errors.New("session store: commit without a request's loaded-session record (use sessionMiddleware)")

func (l *sessionLoads) add(token string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tokens == nil {
		l.tokens = make(map[string]struct{})
	}
	l.tokens[token] = struct{}{}
}

func (l *sessionLoads) has(token string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.tokens[token]
	return ok
}

// trackSessionLoads puts a fresh sessionLoads in the request's context. It must
// run before scs's LoadAndSave, whose Load and Commit both receive a context
// derived from the request's (scs v2.9.0 session.go, data.go).
func trackSessionLoads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionLoadsKey{}, &sessionLoads{})))
	})
}

// sessionMiddleware is the one way to wire the session manager into a router:
// trackSessionLoads, then LoadAndSave. Using LoadAndSave bare would make every
// commit fail (errNoSessionLoads).
func sessionMiddleware(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return trackSessionLoads(sm.LoadAndSave(next))
	}
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
	if loads, ok := ctx.Value(sessionLoadsKey{}).(*sessionLoads); ok {
		loads.add(token)
	}
	return sess.Data, true, nil
}

// CommitCtx writes a session back under one rule (#601): a session this request
// loaded is UPDATEd only, and a token this request did not load is INSERTed
// only. scs re-sends the whole encoded session on every commit, including a
// pure sliding-idle-timeout refresh (session.go's newSessionManager sets
// IdleTimeout, which marks every loaded session Modified in scs's data.go), so
// nearly every authenticated request commits. With an UPSERT, a request in
// flight while another actor deleted the row (a restore's DeleteAllSessions,
// the sign-out of other sessions on a password or email change) put it back at
// its end. An UPDATE that matches no row leaves it deleted, and is not an
// error: the session was revoked, and the next request with the cookie is
// simply a stranger. A token that was not loaded (login, register, RenewToken)
// is fresh random, so its INSERT cannot conflict.
//
// Both arms are one statement, which keeps closed what the UPSERT was written
// for: a delete-then-insert pair interleaves under ADR-004's SetMaxOpenConns(1)
// (DELETE / DELETE / INSERT / INSERT) and the second INSERT of two concurrent
// requests on one cookie tripped session.token's primary key (7 failures in 60
// requests). Loaded tokens never insert; new ones never share a key.
//
// Which arm applies comes from the request's sessionLoads (trackSessionLoads),
// filled by FindCtx. A context without one cannot say, and guessing either way
// reopens the hole (insert-capable: resurrection; update-only: login silently
// persists nothing), so it fails with errNoSessionLoads. Every real request
// carries one: sessionMiddleware is the only wiring of LoadAndSave.
//
// The lazy sweep (#113: "never a background ticker", ADR-013) rides on this
// write. It targets only expired rows, so it stays outside what the rule above
// decides.
func (s *sessionStore) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	loads, ok := ctx.Value(sessionLoadsKey{}).(*sessionLoads)
	if !ok {
		return errNoSessionLoads
	}
	if loads.has(token) {
		// 0 rows: revoked since it was loaded; it stays that way.
		if _, err := s.q.UpdateSession(ctx, store.UpdateSessionParams{
			Token: token, Data: b, ExpiresAt: expiry.Unix(),
		}); err != nil {
			return err
		}
	} else if _, err := s.q.CreateSession(ctx, store.CreateSessionParams{
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
// production these are never called; they exist so the type compiles. Commit
// has no request context to carry sessionLoads, so it always fails.
func (s *sessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *sessionStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

func (s *sessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}
