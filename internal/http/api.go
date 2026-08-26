package http

import (
	"encoding/json"
	"errors"
	"io"
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

	r.Post("/setup", a.setupFund)
	r.Get("/fund", a.getFund)

	// The fund's structure. Read-only but for the one purpose a treasurer
	// creates herself: accounts are the two locations setup made, and the
	// other two purpose kinds are written by SetUpFund and OpenIncidental.
	r.Get("/accounts", a.listAccounts)
	r.Get("/purposes", a.listPurposes)
	r.Post("/pass-through-purposes", a.createPassThroughPurpose)

	// The roster, one block per entity. Direct-CRUD (ADR-027) - no derived
	// invariant, so these call a.queries rather than a.ledger, same split as
	// getFund above. DELETE is only ever for a duplicate added at setup; a
	// member who actually leaves gets inactive_on, which is a PATCH.
	r.Post("/members", a.createMember)
	r.Get("/members", a.listMembers)
	r.Patch("/members/{id}", a.updateMember)
	r.Delete("/members/{id}", a.deleteMember)

	r.Post("/dues-tiers", a.createDuesTier)
	r.Get("/dues-tiers", a.listDuesTiers)
	r.Patch("/dues-tiers/{id}", a.updateDuesTier)

	// Rates are created and listed under their tier, but corrected by their
	// own id: a rate is only ever reached through one tier, and {id} in the
	// nested path already means the tier.
	r.Post("/dues-tiers/{id}/rates", a.createDuesRate)
	r.Get("/dues-tiers/{id}/rates", a.listDuesRates)
	r.Patch("/dues-rates/{id}", a.updateDuesRate)
	r.Delete("/dues-rates/{id}", a.deleteDuesRate)

	// The everyday loop's write path (PRD §7.2, §7.3, §7.6) and the
	// reconcile flow's read path (PRD §7.8). A pass-through movement and a
	// correction are both ordinary POST /api/transactions calls - see
	// transactionRequest's own comment - so there is no separate route for
	// either.
	r.Post("/transactions", a.createTransaction)
	r.Get("/transactions", a.listTransactions)
	r.Post("/dues-payments", a.createDuesPayment)
	r.Get("/dues-status", a.getDuesStatus)
}

// writeJSON is writeAPIError's counterpart for a successful response: every
// route in this package that answers with a body goes through this one
// function, the same way every failure goes through writeAPIError, so the
// two shapes can't drift route by route either.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// decodeJSON decodes r's body into dst and reports whether the handler
// should continue. An empty body (io.EOF) is not itself an error here - it
// decodes to dst's zero value and lets the ledger's own ErrInvalidArgument
// checks name the missing field, per ADR-027: handlers decode and pass
// through, they do not re-check what the ledger already validates. Malformed
// JSON is a shape problem this layer alone can see, so it is the one case
// answered here rather than passed down.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return false
	}
	return true
}
