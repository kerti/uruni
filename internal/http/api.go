package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

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

	// trustedProxies is #447's addition: the peers whose X-Forwarded-For
	// clientIP believes (login.go). Empty believes none.
	trustedProxies []netip.Prefix

	// uploadsDir is #153's addition: where receipt photos are written to and
	// read from (ADR-011). `serve` has already proved it exists and is
	// writable (config.EnsureUploadsDirWritable) before this struct is ever
	// built.
	uploadsDir string

	// backupDir is #324's addition: where the daily server-side backup dumps
	// (ADR-012/ADR-013) are written to and listed from. Like uploadsDir,
	// `serve` has already proved it exists and is writable
	// (config.EnsureBackupDirWritable) before this struct is ever built.
	backupDir string

	// baseURL is URUNI_BASE_URL (no trailing slash; empty in local dev). It
	// builds the report's shareable link, fundResponse.ReportURL.
	baseURL string

	// sqlDB is #325's addition: the raw connection restore.go's confirmRestore
	// hands to internal/backup.Restore, which opens its own write
	// transaction spanning virtually every table. See router.go's New for
	// why this cannot be reached through ledger or queries alone.
	sqlDB *sql.DB

	// now is the clock every handler that reckons a calendar day or month
	// reads (#379), injectable so a test can stand on either side of a
	// Jakarta month boundary. Production is time.Now; the handler converts
	// to tz.Jakarta itself, as the report does. Instants (created_at,
	// session expiry) keep calling time.Now directly - no calendar day
	// hangs on them.
	now func() time.Time

	// restoreStage is #325's own one-slot stash between the upload/inspect
	// step and the confirm step - restore.go's own doc comment explains the
	// shape.
	restoreStage *restoreStage
}

// routes registers the /api surface on the mount New creates. No handlers at
// M4.1: this slice builds the router, the request log and the error mapping
// every later handler shares, and the handlers themselves arrive one slice at
// a time from M4.2 (first-run setup) onward.
//
// What it does register is the pair below. Without them an unknown /api path
// answers with chi's plain-text "404 page not found" while every other failure
// under /api is the JSON envelope - a client would have to parse two shapes to
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

	// Cross-origin writes are refused before the session is even loaded
	// (#481). SameSite=Lax keeps the cookie off a cross-site POST, but a
	// same-site page - a sibling subdomain under the same registrable domain
	// - still gets it; this closes that gap. See crossOriginGuard.
	r.Use(crossOriginGuard)

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
		r.Post("/fund/report-slug", a.replaceReportSlug)

		// The fund's structure. Accounts are whatever the treasurer named at
		// setup (#78) plus anything added or corrected afterward. PATCH and
		// DELETE stay direct-CRUD (ADR-027), the same shape as members below;
		// POST goes through the ledger instead (#230) because it may carry an
		// opening balance that must post inside the same transaction as the
		// account. Purposes stay read-only here but for the one kind a
		// treasurer creates herself (pass-through); the other two kinds are
		// written by SetUpFund and OpenIncidental. PATCH renames whichever
		// kind the id names except the fund's own 'main' row (#264).
		r.Post("/accounts", a.createAccount)
		r.Get("/accounts", a.listAccounts)
		r.Patch("/accounts/{id}", a.updateAccount)
		r.Delete("/accounts/{id}", a.deleteAccount)
		r.Get("/purposes", a.listPurposes)
		r.Post("/pass-through-purposes", a.createPassThroughPurpose)
		r.Patch("/purposes/{id}", a.updatePurposeName)

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
		r.Delete("/dues-tiers/{id}", a.deleteDuesTier)

		// Rates are created and listed under their tier, but corrected by their
		// own id: a rate is only ever reached through one tier, and {id} in the
		// nested path already means the tier.
		r.Post("/dues-tiers/{id}/rates", a.createDuesRate)
		r.Get("/dues-tiers/{id}/rates", a.listDuesRates)
		r.Patch("/dues-rates/{id}", a.updateDuesRate)
		r.Delete("/dues-rates/{id}", a.deleteDuesRate)

		// The everyday loop's write path (PRD section 7.2, section 7.3, section 7.6) and the
		// reconcile flow's read path (PRD section 7.8). A pass-through movement and a
		// correction are both ordinary POST /api/transactions calls - see
		// transactionRequest's own comment - so there is no separate route for
		// either.
		r.Post("/transactions", a.createTransaction)
		r.Get("/transactions", a.listTransactions)
		// Fixing a posted row's peruntukan (ADR-033, #267): a
		// reclass_purpose pair naming the row it corrects, addressed by
		// that row's own id, matching the nested-verb idiom
		// /api/incidentals/{purposeID}/close already uses. Not a second
		// write path into PostTransaction (ADR-027) - it wraps
		// Ledger.PostPurposeCorrection, which itself reuses transfer.go's
		// postTransferPairTx.
		r.Post("/transactions/{id}/purpose-correction", a.postPurposeCorrection)
		// Moving money between two accounts without changing what the fund
		// holds in total (PRD section 6). No GET: a transfer's two legs are ordinary
		// transaction rows and already surface through GET /api/transactions.
		r.Post("/transfers", a.createTransfer)
		// Moving money between purposes - Kas Utama to an envelope and back,
		// or envelope to envelope - without moving any money (ADR-036,
		// #383). The third caller of postTransferPairTx, through
		// Ledger.PostPurposeMove; its legs surface through GET
		// /api/transactions like a transfer's do.
		r.Post("/purpose-moves", a.createPurposeMove)

		// A member fronting their own money (PRD section 7.4). Recording the claim
		// moves nothing - only settling posts a ledger row, which is why the
		// recorded balance still matches the wallet while a claim is
		// outstanding. There is deliberately no waive route: PRD section 7.4 never
		// asks for one.
		// PATCH is both the correction and the waive (#103): waiving sets one
		// column, so pairing it with the ordinary correction is what makes
		// un-waiving free, and keeps the block the same POST/GET/PATCH/DELETE
		// shape as members. Both are refused once the claim is settled.
		r.Post("/reimbursements", a.createReimbursement)
		r.Get("/reimbursements", a.listReimbursements)
		r.Get("/reimbursements/{id}", a.getReimbursement)
		r.Patch("/reimbursements/{id}", a.updateReimbursement)
		r.Delete("/reimbursements/{id}", a.deleteReimbursement)
		r.Post("/reimbursements/{id}/settle", a.settleReimbursement)

		r.Post("/dues-payments", a.createDuesPayment)
		r.Post("/dues-payments/{id}/reversal", a.reverseDuesPayment)
		// Dues payment history (#228, PRD section 7.3): every posted
		// payment and reversal, newest-first, searchable by member name -
		// GET /dues-status only ever answers one period at a time, and
		// never lists what was actually posted.
		r.Get("/dues-payments", a.listDuesPayments)
		r.Get("/dues-status", a.getDuesStatus)
		// Which periods one member still owes (#186), split out of #146 so
		// the record-a-dues-payment screen has a server-side answer instead
		// of guessing a window and firing one request per month against the
		// fund-wide route above.
		r.Get("/members/{id}/outstanding-dues", a.getOutstandingDues)

		// A one-off collection for an occasion, tracked separately from the
		// general fund and closed when it's over (PRD section 7.5). Addressed by
		// purpose id, matching how an incidental is addressed everywhere else
		// in the domain. There is deliberately no contribute route: a
		// contribution is an ordinary transaction tagged to the envelope's
		// purpose, posted through POST /api/transactions above (#67).
		r.Post("/incidentals", a.openIncidental)
		r.Get("/incidentals", a.listIncidentals)
		r.Get("/incidentals/{purposeID}", a.getIncidental)
		// The minimum and the recipients (ADR-034, #211) - both editable
		// like occasion, never a posted fact, so a plain PATCH on the
		// resource itself rather than a nested-verb action route.
		r.Patch("/incidentals/{purposeID}", a.updateIncidentalParticipation)
		r.Post("/incidentals/{purposeID}/close", a.closeIncidental)
		// The way back from a closed envelope (ADR-031), matching the
		// resource's own .../close idiom rather than reimbursement's
		// PATCH-a-nullable-field shape.
		r.Post("/incidentals/{purposeID}/reopen", a.reopenIncidental)
		// The envelope's participation table (ADR-034, #211): who has
		// contributed and how much, derived from the ledger against who
		// was expected - never stored, so this is a GET, not a resource of
		// its own.
		r.Get("/incidentals/{purposeID}/participation", a.getIncidentalParticipation)

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

		// Receipt photos (PRD section 7.4, ADR-011): an attachment, not a
		// ledger fact, so it hangs off a transaction or a reimbursement
		// through its own table rather than a column on either (receipt's
		// exactly-one-parent CHECK) - two upload routes rather than one that
		// accepts either id, matching that CHECK. GET is the first route in
		// this whole surface that answers with image bytes instead of JSON;
		// it stays behind this session gate rather than joining the four
		// public routes above - a receipt "shows what the fund bought," and
		// nothing in PRD section 7.9 asks for it on the public report.
		r.Post("/transactions/{id}/receipts", a.uploadTransactionReceipt)
		r.Post("/reimbursements/{id}/receipts", a.uploadReimbursementReceipt)
		r.Get("/receipts/{id}", a.getReceipt)
		r.Delete("/receipts/{id}", a.deleteReceipt)

		// The whole fund as one zip (M6.37, #323, ADR-012): uruni.json plus
		// every receipt image. Session-gated for the same reason every
		// route in this group is - it carries the password hash alongside
		// everything else.
		r.Get("/backup", a.downloadBackup)

		// The Cadangan card's own two routes (M6.38, #324, ADR-012/013):
		// what server-side dumps already exist, and downloading one of
		// them by name. Plural and distinct from the singular /backup
		// above on purpose - that route builds a fresh zip on demand and
		// answers with it directly; these two only ever read
		// URUNI_BACKUP_DIR, never build anything themselves.
		r.Get("/backups", a.listBackups)
		r.Get("/backups/{name}", a.downloadStoredBackup)

		// Restore from an uploaded backup (M6.39, #325, ADR-012), or from one
		// of the server's own stored dumps (M6.40, #326): inspect/preview
		// (from either source), then confirm with a password re-entry -
		// restore.go's own doc comment has the three-route shape and why.
		r.Post("/restore/inspect", a.inspectRestoreUpload)
		r.Post("/restore/inspect-stored/{name}", a.inspectStoredBackup)
		r.Post("/restore/confirm", a.confirmRestore)
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
//
// The body is capped at maxJSONBodyBytes first (#447): login and register are
// reachable by anyone, and an uncapped decode reads whatever a stranger sends.
// Every JSON body in this package is read through here, so the cap is in one
// place; the multipart routes (receipts, restore) set their own.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// No copy of its own: the SPA never sends a body near this size,
			// so the treasurer would only ever see the generic error.
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The request body is too large.")
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return false
	}
	return true
}

// maxJSONBodyBytes caps a JSON request body. The largest the SPA sends - a
// transaction with its note - is a few kilobytes; 1 MiB is generous for any
// legitimate request and bounds what an anonymous one costs.
const maxJSONBodyBytes = 1 << 20
