package http

import "net/http"

// DemoLogin is the public demo's shared treasurer login (ADR-045). A non-nil
// Deps.Demo is the whole of "demo mode" in this package: with it nil none of
// what follows exists.
//
// It is the credentials of a fund that is public by design, every character of
// which is in the repository, and so the one place a password is put on the wire
// to an unauthenticated caller.
type DemoLogin struct {
	Email    string
	Password string
}

// noIndex marks every response, the SPA shell, the API and the public report
// alike, as not for a search engine: a demo found through a search is the
// failure ADR-045 decision 6 exists to prevent. A header here, not a fly.toml
// rule, because this is tested and Fly's header rewriting is not. It only sets
// the header; a route that sets its own (the report) overwrites it with the same
// value.
func noIndex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// refuseInDemo refuses the two writes that would change the shared login
// (ADR-045 decision 2): either one locks every later visitor out of the
// pre-filled credentials until the next reset. Outside the demo it is a
// pass-through. It sits behind the session gate, so a signed-out caller still
// gets the 401 and learns nothing about the mode. 403, like
// wrong_current_password: she is signed in, only not allowed this. Its own code,
// so the SPA words it.
func (a *api) refuseInDemo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.demo == nil {
			next.ServeHTTP(w, r)
			return
		}
		writeAPIError(w, http.StatusForbidden, "demo_login_locked", "The demo login cannot be changed.")
	})
}
