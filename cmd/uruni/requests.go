package main

import (
	"context"
	"net"
	"net/http"
	"sync"
)

// requestTracker is what lets shutdown end the requests still running when the
// drain gives up, and know they are gone before the database closes.
// http.Server gives neither: Shutdown returning on its deadline leaves handlers
// running, and Close drops connections without waiting for them.
//
// It does two things to the server it is attached to. Every request context is
// derived from one cancellable base (end cancels it), so a handler that honours
// its context (the restore's transaction does: it rolls back) returns promptly.
// And every handler is counted in and out, so wait can say when none is left.
type requestTracker struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
}

func newRequestTracker() *requestTracker {
	ctx, cancel := context.WithCancel(context.Background())
	return &requestTracker{ctx: ctx, cancel: cancel}
}

// attach wraps srv.Handler and sets srv.BaseContext. Call it before the server
// starts. The base is not serve's signal context: that is cancelled the moment
// SIGTERM arrives, which would abort the very requests the drain is waiting for.
func (t *requestTracker) attach(srv *http.Server) {
	next := srv.Handler
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !t.enter() {
			// A backstop, not a path the normal sequence reaches: wait begins only
			// after srv.Shutdown has closed the listener and the idle connections,
			// so a request arriving now means a connection raced the close.
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer t.active.Done()
		next.ServeHTTP(w, r)
	})
	srv.BaseContext = func(net.Listener) context.Context { return t.ctx }
}

// enter counts a request in, unless wait has begun. Refusing after that is what
// makes wait exact: a WaitGroup may not gain a member while a Wait is running
// from zero, and a request that slipped in behind the wait would be one nobody
// joins.
func (t *requestTracker) enter() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.active.Add(1)
	return true
}

// end cancels every request's context.
func (t *requestTracker) end() { t.cancel() }

// wait stops admitting requests and returns a channel that closes once every
// admitted handler has returned. The goroutine behind it outlives a timed-out
// wait, which is fine: the process is about to exit.
func (t *requestTracker) wait() <-chan struct{} {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	done := make(chan struct{})
	go func() {
		t.active.Wait()
		close(done)
	}()
	return done
}
