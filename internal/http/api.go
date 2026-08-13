package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// api holds what every /api handler needs, so a route is a method rather than
// a closure over three captured variables repeated at each registration.
//
// Both a ledger and a plain Querier, deliberately: routes with a derived
// invariant go through the ledger, and the direct-CRUD routes ADR-027 keeps
// out of it (members, dues tiers, dues rates) call queries themselves. Which
// of the two a handler reaches for is the visible form of that boundary.
type api struct {
	ledger  *ledger.Ledger
	queries store.Querier
	logger  *slog.Logger
}

// routes registers the /api surface on the mount New creates. No handlers at
// M4.1: this slice builds the router, the request log and the error mapping
// every later handler shares, and the handlers themselves arrive one slice at
// a time from M4.2 (first-run setup) onward.
//
// What it does register is the pair below. Without them an unknown /api path
// answers with chi's plain-text "404 page not found" while every other failure
// under /api is the JSON envelope — a client would have to parse two shapes to
// learn the same thing, and a mistyped path is exactly when it is least able
// to. Registering them here rather than per-slice means the whole namespace
// answers the same way from its first handler onward.
func (a *api) routes(r chi.Router) {
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "That method is not allowed on this resource.")
	})
}
