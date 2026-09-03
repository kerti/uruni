package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/auth"
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

	// auth and sessionManager are M5's addition (issue #114): the bootstrap
	// account and the session cookie that logs it straight in. Both are nil
	// only in the sense that no handler before this milestone reached for
	// them - every handler in this file still goes through ledger or
	// queries exactly as before.
	auth           *auth.Auth
	sessionManager *scs.SessionManager

	// loginLimiter is POST /api/login's rate limiter (issue #115) - one
	// instance per api, so it accumulates across requests for the life of
	// the process (and, in a test, for the life of the one router that
	// test built) rather than resetting per call.
	loginLimiter *rateLimiter
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

	// Scoped to /api rather than the whole router (ADR-030): a cookie has no
	// business being parsed or issued for a static asset request or the SSR
	// public report, and this is the one mount every session-aware route
	// already funnels through.
	r.Use(a.sessionManager.LoadAndSave)

	// The four routes a stranger can reach with no session at all (#116).
	// Nothing joins this group without an explicit reason on the spot -
	// every other handler in this file, setup included, sits behind
	// sessionRequired in the group below.
	r.Group(func(r chi.Router) {
		// The bootstrap account (#114). Unauthenticated by design - there is
		// no session yet to gate it with, and it refuses itself the moment
		// any account exists (auth.ErrAlreadyRegistered).
		r.Post("/register", a.register)

		// The everyday login (#115), alongside register above - also
		// unauthenticated by design, and for the same reason: there is no
		// session yet for a session-gated route to check.
		r.Post("/login", a.login)

		// The one read a booting, logged-out client needs before it has
		// anything to authenticate with: cookies are httpOnly, so this is
		// the SPA's only way to tell whether to render register or login.
		r.Get("/session", a.getSession)

		// Destroying a session a caller may or may not still have. Public
		// for the same reason getSession is: a caller with an already-
		// expired cookie is asking for exactly what logout gives, and a 401
		// there would be hostile for no gain.
		r.Post("/logout", a.logout)
	})

	// Every other route in the surface, now that a session proves "you are
	// the treasurer" (ADR-030 decision 2). sessionRequired runs first so
	// nothing below it is ever reached without one.
	r.Group(func(r chi.Router) {
		r.Use(a.sessionRequired)

		// POST /setup sits *inside* the gate - ADR-030's explicit ruling,
		// and the one placement worth stating outright because the opposite
		// reads as obvious and is wrong. Registration closes after first
		// use, but between the treasurer registering and finishing setup
		// there is a window in which a public /setup would let a stranger
		// create and name her own fund - and ErrFundAlreadyExists would make
		// that permanent, report_slug and all, for the treasurer who
		// registered first. Gating it here is what closes that window.
		r.Post("/setup", a.setupFund)
		r.Get("/fund", a.getFund)
		r.Patch("/fund", a.updateFund)

		// The fund's structure. Accounts are whatever the treasurer named at
		// setup (#78) plus anything added or corrected afterward - direct-CRUD
		// (ADR-027), the same POST/PATCH/DELETE shape as members below. Purposes
		// stay read-only here but for the one kind a treasurer creates herself
		// (pass-through); the other two kinds are written by SetUpFund and
		// OpenIncidental.
		r.Post("/accounts", a.createAccount)
		r.Get("/accounts", a.listAccounts)
		r.Patch("/accounts/{id}", a.updateAccount)
		r.Delete("/accounts/{id}", a.deleteAccount)
		// An account's starting figure (PRD §7.1) - PostOpeningBalance has been
		// built and tested since M3 but had no route until now (#134).
		r.Post("/accounts/{id}/opening-balance", a.postAccountOpeningBalance)
		r.Get("/purposes", a.listPurposes)
		r.Post("/pass-through-purposes", a.createPassThroughPurpose)
		r.Patch("/purposes/{id}", a.updatePassThroughPurpose)

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
		// Moving money between two accounts without changing what the fund
		// holds in total (PRD §6). No GET: a transfer's two legs are ordinary
		// transaction rows and already surface through GET /api/transactions.
		r.Post("/transfers", a.createTransfer)

		// A member fronting their own money (PRD §7.4). Recording the claim
		// moves nothing - only settling posts a ledger row, which is why the
		// recorded balance still matches the wallet while a claim is
		// outstanding. There is deliberately no waive route: PRD §7.4 never
		// asks for one.
		// PATCH is both the correction and the waive (#103): waiving sets one
		// column, so pairing it with the ordinary correction is what makes
		// un-waiving free, and keeps the block the same POST/GET/PATCH/DELETE
		// shape as members. Both are refused once the claim is settled.
		r.Post("/reimbursements", a.createReimbursement)
		r.Get("/reimbursements", a.listReimbursements)
		r.Patch("/reimbursements/{id}", a.updateReimbursement)
		r.Delete("/reimbursements/{id}", a.deleteReimbursement)
		r.Post("/reimbursements/{id}/settle", a.settleReimbursement)

		r.Post("/dues-payments", a.createDuesPayment)
		r.Post("/dues-payments/{id}/reversal", a.reverseDuesPayment)
		r.Get("/dues-status", a.getDuesStatus)
		// Which periods one member still owes (#186), split out of #146 so
		// the record-a-dues-payment screen has a server-side answer instead
		// of guessing a window and firing one request per month against the
		// fund-wide route above.
		r.Get("/members/{id}/outstanding-dues", a.getOutstandingDues)

		// A one-off collection for an occasion, tracked separately from the
		// general fund and closed when it's over (PRD §7.5). Addressed by
		// purpose id, matching how an incidental is addressed everywhere else
		// in the domain. There is deliberately no contribute route: a
		// contribution is an ordinary transaction tagged to the envelope's
		// purpose, posted through POST /api/transactions above (#67).
		r.Post("/incidentals", a.openIncidental)
		r.Get("/incidentals", a.listIncidentals)
		r.Get("/incidentals/{purposeID}", a.getIncidental)
		r.Post("/incidentals/{purposeID}/close", a.closeIncidental)

		// Counting the real money and comparing it to the recorded balance (PRD
		// section 7.7's home banner, section 7.8's reconcile flow). "latest" and
		// "open-lines" are literal path segments, not ids - they are registered
		// ahead of the {id} route below so chi's router resolves them as their own
		// static routes rather than the {id} wildcard swallowing "latest" as an
		// id chi then fails to parse as an integer.
		r.Post("/reconciliations", a.takeReconciliation)
		r.Get("/reconciliations", a.listReconciliations)
		r.Get("/reconciliations/latest", a.latestReconciliation)
		r.Get("/reconciliations/open-lines", a.listOpenReconciliationLines)
		r.Get("/reconciliations/{id}", a.getReconciliation)

		// The composed view-model for the home screen (PRD section 7.7): the
		// fund total, every account's balance and every purpose's balance, in one
		// round trip. See getBalances's own comment for why this is the one route
		// in M4 built by composing ledger reads rather than wrapping a single
		// ledger call.
		r.Get("/balances", a.getBalances)
	})
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
