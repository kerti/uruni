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
// a closure repeated at each registration.
//
// It holds both a ledger and a plain Querier: routes with a derived invariant
// go through the ledger, and the direct-CRUD routes ADR-027 keeps out of it
// (members, dues tiers, dues rates) call queries themselves.
type api struct {
	ledger  *ledger.Ledger
	queries store.Querier
	logger  *slog.Logger

	// auth and sessionManager: the bootstrap account and the session cookie that
	// logs it straight in.
	auth           *auth.Auth
	sessionManager *scs.SessionManager

	// loginLimiter is POST /api/login's rate limiter: one instance per api, so
	// it accumulates across requests for the life of the process.
	loginLimiter *rateLimiter

	// registerLimiter is POST /api/register's, counted per client IP (#495).
	// Separate from loginLimiter, whose key a success resets; a register success
	// must not.
	registerLimiter *rateLimiter

	// devices remembers the device cookies login has issued (#500): the only
	// callers let past login's shared keys.
	devices *deviceTokens

	// trustedProxies are the peers whose X-Forwarded-For clientIP believes
	// (login.go). Empty believes none.
	trustedProxies []netip.Prefix

	// uploadsDir is where receipt photos are written and read (ADR-011); `serve`
	// has proved it exists and is writable (config.EnsureUploadsDirWritable).
	uploadsDir string

	// backupDir is where the daily backup dumps (ADR-012/ADR-013) are written and
	// listed; `serve` has proved it exists and is writable
	// (config.EnsureBackupDirWritable).
	backupDir string

	// baseURL is URUNI_BASE_URL (no trailing slash; empty in local dev). It
	// builds the report's shareable link, fundResponse.ReportURL.
	baseURL string

	// sqlDB is the raw connection restore.go's confirmRestore hands to
	// internal/backup.Restore, which opens a write transaction spanning nearly
	// every table. See router.go's New.
	sqlDB *sql.DB

	// now is the clock every handler that reckons a calendar day or month reads
	// (#379), injectable so a test can stand on either side of a Jakarta month
	// boundary. Production is time.Now; the handler converts to tz.Jakarta.
	// Instants (created_at, session expiry) call time.Now directly.
	now func() time.Time

	// restoreStage is the one-slot stash between the upload/inspect step and the
	// confirm step; see restore.go.
	restoreStage *restoreStage
}

// routes registers the /api surface on the mount New creates: the cross-origin
// guard and session middleware, the routes a stranger can reach, and the
// session-gated routes behind them.
//
// It registers the NotFound and MethodNotAllowed pair first. Without them an
// unknown /api path answers with chi's plain-text "404 page not found" while
// every other failure under /api is the JSON envelope, so a client would
// have to parse two shapes.
func (a *api) routes(r chi.Router) {
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "That method is not allowed on this resource.")
	})

	// Cross-origin writes are refused before the session is loaded (#481).
	// SameSite=Lax keeps the cookie off a cross-site POST, but a same-site page
	// (a sibling subdomain) still gets it; see crossOriginGuard.
	r.Use(crossOriginGuard)

	// Scoped to /api rather than the whole router (ADR-030): no cookie should be
	// parsed or issued for a static asset or the SSR public report.
	r.Use(a.sessionManager.LoadAndSave)

	// The four routes a stranger can reach with no session (#116). Nothing joins
	// this group without an explicit reason; every other handler, setup
	// included, sits behind sessionRequired below.
	r.Group(func(r chi.Router) {
		// The bootstrap account (#114). Unauthenticated by design: there is no
		// session yet, and it refuses itself once any account exists
		// (auth.ErrAlreadyRegistered).
		r.Post("/register", a.register)

		// The everyday login (#115): unauthenticated by design, like register.
		r.Post("/login", a.login)

		// The one read a logged-out client needs: cookies are httpOnly, so this is
		// the SPA's only way to tell whether to render register or login.
		r.Get("/session", a.getSession)

		// Public like getSession: a caller with an expired cookie is asking for what
		// logout gives, and a 401 would be hostile for no gain.
		r.Post("/logout", a.logout)
	})

	// Every other route, behind a session that proves "you are the treasurer"
	// (ADR-030 decision 2). sessionRequired runs first.
	r.Group(func(r chi.Router) {
		r.Use(a.sessionRequired)

		// The treasurer's own login (#386): read it, change its email or its
		// password. Both writes re-ask the current password; see user.go.
		r.Get("/user", a.getUser)
		r.Post("/user/email", a.changeEmail)
		r.Post("/user/password", a.changePassword)

		// POST /setup sits inside the gate (ADR-030's ruling). Registration closes
		// after first use, but between registering and finishing setup a public
		// /setup would let a stranger create and name a fund, and
		// ErrFundAlreadyExists would make that permanent, report_slug and all.
		r.Post("/setup", a.setupFund)
		r.Get("/fund", a.getFund)
		r.Patch("/fund", a.updateFund)
		r.Post("/fund/report-slug", a.replaceReportSlug)

		// The fund's structure. PATCH and DELETE are direct-CRUD (ADR-027), like
		// members below; POST goes through the ledger (#230) because it may carry an
		// opening balance that must post in the same transaction as the account.
		// Purposes are read-only here except the one kind a treasurer creates
		// (pass-through); the other kinds are written by SetUpFund and
		// OpenIncidental. PATCH renames any kind except the fund's 'main' row (#264).
		r.Post("/accounts", a.createAccount)
		r.Get("/accounts", a.listAccounts)
		r.Patch("/accounts/{id}", a.updateAccount)
		r.Delete("/accounts/{id}", a.deleteAccount)
		r.Get("/purposes", a.listPurposes)
		r.Post("/pass-through-purposes", a.createPassThroughPurpose)
		r.Patch("/purposes/{id}", a.updatePurposeName)

		// The roster. Direct-CRUD (ADR-027): no derived invariant, so these call
		// a.queries rather than a.ledger. DELETE is only for a duplicate added at
		// setup; a member who leaves gets inactive_on, a PATCH.
		r.Post("/members", a.createMember)
		r.Get("/members", a.listMembers)
		r.Patch("/members/{id}", a.updateMember)
		r.Delete("/members/{id}", a.deleteMember)

		r.Post("/dues-tiers", a.createDuesTier)
		r.Get("/dues-tiers", a.listDuesTiers)
		r.Patch("/dues-tiers/{id}", a.updateDuesTier)
		r.Delete("/dues-tiers/{id}", a.deleteDuesTier)

		// Rates are created and listed under their tier but corrected by id: a rate
		// is only reached through one tier, so {id} in the nested path means the tier.
		r.Post("/dues-tiers/{id}/rates", a.createDuesRate)
		r.Get("/dues-tiers/{id}/rates", a.listDuesRates)
		r.Patch("/dues-rates/{id}", a.updateDuesRate)
		r.Delete("/dues-rates/{id}", a.deleteDuesRate)

		// The everyday loop's write path (PRD sections 7.2, 7.3, 7.6) and the
		// reconcile flow's read path (PRD section 7.8). A pass-through movement and a
		// correction are ordinary POST /api/transactions calls (see
		// transactionRequest), so neither has its own route.
		r.Post("/transactions", a.createTransaction)
		r.Get("/transactions", a.listTransactions)
		// Fixing a posted row's peruntukan (ADR-033, #267): a reclass_purpose pair
		// naming the row it corrects, addressed by that row's id like
		// /api/incidentals/{purposeID}/close. It wraps Ledger.PostPurposeCorrection,
		// not a second write path into PostTransaction (ADR-027).
		r.Post("/transactions/{id}/purpose-correction", a.postPurposeCorrection)
		// Moving money between accounts without changing the fund's total (PRD
		// section 6). No GET: the two legs are ordinary transaction rows and surface
		// through GET /api/transactions.
		r.Post("/transfers", a.createTransfer)
		// Moving money between purposes without moving any money (ADR-036, #383),
		// through Ledger.PostPurposeMove; its legs surface through GET
		// /api/transactions like a transfer's.
		r.Post("/purpose-moves", a.createPurposeMove)

		// A member fronting their money (PRD section 7.4). Recording the claim moves
		// nothing; only settling posts a ledger row, so the recorded balance still
		// matches the wallet while a claim is outstanding. There is no waive route:
		// PRD section 7.4 never asks for one. PATCH is both the correction and the
		// waive (#103): waiving sets one column, so pairing it with the correction
		// makes un-waiving free. Both are refused once the claim is settled.
		r.Post("/reimbursements", a.createReimbursement)
		r.Get("/reimbursements", a.listReimbursements)
		r.Get("/reimbursements/{id}", a.getReimbursement)
		r.Patch("/reimbursements/{id}", a.updateReimbursement)
		r.Delete("/reimbursements/{id}", a.deleteReimbursement)
		r.Post("/reimbursements/{id}/settle", a.settleReimbursement)

		r.Post("/dues-payments", a.createDuesPayment)
		r.Post("/dues-payments/{id}/reversal", a.reverseDuesPayment)
		// Dues payment history (#228, PRD section 7.3): every posted payment and
		// reversal, newest-first, searchable by member name. GET /dues-status
		// answers one period at a time and never lists what was posted.
		r.Get("/dues-payments", a.listDuesPayments)
		r.Get("/dues-status", a.getDuesStatus)
		// Which periods one member still owes (#186), so the record-a-dues-payment
		// screen has a server-side answer instead of one request per month.
		r.Get("/members/{id}/outstanding-dues", a.getOutstandingDues)

		// A one-off collection for an occasion, tracked apart from the general fund
		// and closed when over (PRD section 7.5). Addressed by purpose id. There is
		// no contribute route: a contribution is an ordinary transaction tagged to
		// the envelope's purpose, posted through POST /api/transactions (#67).
		r.Post("/incidentals", a.openIncidental)
		r.Get("/incidentals", a.listIncidentals)
		r.Get("/incidentals/{purposeID}", a.getIncidental)
		// The minimum and the recipients (ADR-034, #211): editable like occasion,
		// never a posted fact, so a plain PATCH on the resource.
		r.Patch("/incidentals/{purposeID}", a.updateIncidentalParticipation)
		r.Post("/incidentals/{purposeID}/close", a.closeIncidental)
		// The way back from a closed envelope (ADR-031), a nested verb like .../close.
		r.Post("/incidentals/{purposeID}/reopen", a.reopenIncidental)
		// The envelope's participation table (ADR-034, #211): who contributed and
		// how much against who was expected, derived from the ledger and never
		// stored, so a GET.
		r.Get("/incidentals/{purposeID}/participation", a.getIncidentalParticipation)

		// Counting the real money and comparing it to the recorded balance (PRD
		// section 7.7's home banner, section 7.8's reconcile flow). "latest" and
		// "open-lines" are literal segments, registered ahead of {id} so chi does not
		// swallow them as an id.
		r.Post("/reconciliations", a.takeReconciliation)
		r.Get("/reconciliations", a.listReconciliations)
		r.Get("/reconciliations/latest", a.latestReconciliation)
		r.Get("/reconciliations/open-lines", a.listOpenReconciliationLines)
		r.Get("/reconciliations/{id}", a.getReconciliation)

		// The composed view-model for the home screen (PRD section 7.7): fund total
		// and every account's and purpose's balance in one round trip. See
		// getBalances.
		r.Get("/balances", a.getBalances)

		// Receipt photos (PRD section 7.4, ADR-011): an attachment, not a ledger
		// fact, so it hangs off a transaction or a reimbursement through its own
		// table (receipt's exactly-one-parent CHECK), with two upload routes to
		// match. GET answers with image bytes and stays behind this session gate: a
		// receipt "shows what the fund bought," and PRD section 7.9 does not ask for
		// it on the public report.
		//
		// A photo is up to 10 MB, and the SPA waits two minutes for it, so the upload
		// routes extend the server's short read deadline to match (extendDeadlines).
		receiptUpload := r.With(extendDeadlines(a.logger, receiptTransferTimeout))
		receiptUpload.Post("/transactions/{id}/receipts", a.uploadTransactionReceipt)
		receiptUpload.Post("/reimbursements/{id}/receipts", a.uploadReimbursementReceipt)
		r.Get("/receipts/{id}", a.getReceipt)
		r.Delete("/receipts/{id}", a.deleteReceipt)

		// The whole fund as one zip (ADR-012): uruni.json plus every receipt image.
		// Session-gated like every route here; it carries the password hash.
		//
		// The backup and restore routes move up to 256 MB (and the zip sits in
		// memory), so they extend the server's read and write deadlines
		// (extendDeadlines); everything else keeps the short ones.
		backupTransfer := r.With(extendDeadlines(a.logger, backupTransferTimeout))
		backupTransfer.Get("/backup", a.downloadBackup)

		// The Cadangan card's two routes (ADR-012/013): what server-side dumps
		// exist, and downloading one by name. Distinct from the singular /backup,
		// which builds a fresh zip on demand; these only read URUNI_BACKUP_DIR.
		r.Get("/backups", a.listBackups)
		backupTransfer.Get("/backups/{name}", a.downloadStoredBackup)

		// Restore from an uploaded backup (ADR-012) or one of the server's stored
		// dumps: inspect/preview, then confirm with a password re-entry. See
		// restore.go for the three-route shape.
		backupTransfer.Post("/restore/inspect", a.inspectRestoreUpload)
		backupTransfer.Post("/restore/inspect-stored/{name}", a.inspectStoredBackup)
		backupTransfer.Post("/restore/confirm", a.confirmRestore)
	})
}

// writeJSON is writeAPIError's counterpart for a successful response. Every
// body-carrying route goes through it, so the two shapes cannot drift.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// decodeJSON decodes r's body into dst and reports whether the handler
// should continue. An empty body (io.EOF) decodes to dst's zero value and
// lets the ledger's ErrInvalidArgument checks name the missing field
// (ADR-027): handlers pass through what the ledger validates. Malformed JSON
// is the one case answered here.
//
// The body is capped at maxJSONBodyBytes first (#447): login and register are
// reachable by anyone. Every JSON body goes through here, so the cap is in
// one place; the multipart routes (receipts, restore) set their own.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// No copy: the SPA never sends a body near this size, so the treasurer
			// would only see the generic error.
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The request body is too large.")
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return false
	}
	return true
}

// maxJSONBodyBytes caps a JSON request body. The SPA's largest (a transaction
// with its note) is a few kilobytes; 1 MiB is generous and bounds what an
// anonymous request costs.
const maxJSONBodyBytes = 1 << 20
