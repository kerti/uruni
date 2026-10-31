package http

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// transactionRequest is POST /api/transactions's body: one ordinary entry or
// one correction (PRD sections 7.2, 7.6). A pass-through movement is an
// ordinary transaction tagged to a pass-through purpose (#66). IsAdjustment
// selects kind='adjustment' over kind='normal', mirroring
// PostTransactionParams on the wire rather than exposing a raw kind (as
// passThroughPurposeRequest omits Kind, ADR-027).
type transactionRequest struct {
	AccountID    int64            `json:"account_id"`
	PurposeID    int64            `json:"purpose_id"`
	Direction    ledger.Direction `json:"direction"`
	Amount       int64            `json:"amount"`
	OccurredOn   string           `json:"occurred_on"`
	Note         *string          `json:"note"`
	IsAdjustment bool             `json:"is_adjustment"`

	// MemberID optionally names who a contribution into an open envelope is from
	// (ADR-034, #211), Catat's "Dari siapa? (opsional)"; meaningful only on a
	// kind='normal' 'in' row. nil for a guest or anonymous giver and every other
	// shape. PostTransaction's checks turn misuse into a named error; this
	// handler does not pre-validate (ADR-027).
	MemberID *int64 `json:"member_id"`
}

// transactionResponse is the wire shape of a transaction row, every kind the
// ledger can post (normal, adjustment, dues, opening, transfer,
// reimbursement); both routes in this file share it. No fund_id, like the
// other response types here.
//
// The fields after CreatedAt (#257) are the facts TransactionList.tsx needs
// to build a system row's display label without a request per row. Only
// toTransactionsPageResponse (GET) sets them, not toTransactionResponse
// (POST's reply): a freshly-posted row has no reconciliation line naming it
// and, for a transfer, no guaranteed sibling leg in the same round trip, so
// POST returns zero values rather than a misleadingly confident answer.
type transactionResponse struct {
	ID              int64   `json:"id"`
	AccountID       int64   `json:"account_id"`
	PurposeID       int64   `json:"purpose_id"`
	Direction       string  `json:"direction"`
	Amount          int64   `json:"amount"`
	OccurredOn      string  `json:"occurred_on"`
	Kind            string  `json:"kind"`
	MemberID        *int64  `json:"member_id"`
	DuesPeriod      *string `json:"dues_period"`
	ReimbursementID *int64  `json:"reimbursement_id"`
	TransferID      *int64  `json:"transfer_id"`
	// The row this one reverses (ADR-029, ADR-042), set only on a reversal. A
	// client has no other way to tell a reversal from an ordinary correction.
	ReversesTransactionID *int64  `json:"reverses_transaction_id"`
	Note                  *string `json:"note"`
	CreatedAt             int64   `json:"created_at"`

	// AccountName is this row's account (location); opening balance and
	// reconciliation-fix labels are built from it. Always present on a listed row
	// (account_id is NOT NULL and an account with transactions is never deleted).
	AccountName string `json:"account_name"`
	// PurposeKind is the kind of this row's stored purpose ('main',
	// 'incidental', 'pass_through'); an anonymous contribution is a 'normal'
	// 'in' row into an 'incidental' one.
	PurposeKind string `json:"purpose_kind"`
	// MemberName is the dues member for a dues payment or reversal, or the
	// settled claim's member for a reimbursement payout; never both, nil
	// otherwise. See toTransactionsPageResponse.
	MemberName *string `json:"member_name"`
	// ClaimNote is the settled claim's note (reimbursement.note), set only on a
	// kind='reimbursement' row, so the Talangan settlement label can show what
	// the claim was for though the settlement's Note is nil (#257: nothing
	// generated is ever stored).
	ClaimNote *string `json:"claim_note"`
	// TransferKind is the pair's kind ('between_accounts' or 'reclass_purpose'),
	// nil outside kind='transfer'. TransferFromName/TransferToName are that
	// kind's two location or purpose names in the transfer's from->to order,
	// whichever leg this row is, except on a purpose correction, which names
	// the corrected row's pos before and after; nil outside kind='transfer'.
	TransferKind     *string `json:"transfer_kind"`
	TransferFromName *string `json:"transfer_from_name"`
	TransferToName   *string `json:"transfer_to_name"`
	// TransferCorrectsTransactionID is this row's transfer's link (ADR-033,
	// #267): non-nil exactly on a purpose correction's two legs, nil on every
	// other transfer leg including a roll's. It tells #276's row label a
	// correction leg from a roll, which otherwise share kind='transfer',
	// transfer_kind='reclass_purpose'.
	TransferCorrectsTransactionID *int64 `json:"transfer_corrects_transaction_id"`
	// TransferReason is this row's transfer's reason (ADR-036): 'roll' or
	// 'allocation' on a purpose pair that corrects nothing, nil on a correction,
	// a between_accounts pair, every non-transfer row, and a pair that predates
	// the column (a roll). The label reads nil as a roll, so only 'allocation'
	// changes what Riwayat says.
	TransferReason *string `json:"transfer_reason"`
	// EffectivePurposeID is the tag this row's money is under now: its PurposeID
	// until a correction moves it (ADR-033), then the latest correction's
	// target. Equal to PurposeID on every uncorrected row, so "corrected?" is
	// EffectivePurposeID != PurposeID, not a second boolean.
	//
	// The list still RENDERS PurposeID (ADR-033): the ledger sums stored tags, so
	// showing the effective one would put the screen out of step with the
	// balances. This is for #276's correction dialog, which builds its pair from
	// where the money is, and for the marker that a correction exists.
	EffectivePurposeID int64 `json:"effective_purpose_id"`

	// Reversed is ledger_entry.reversed (ADR-042): a reversal points at this row.
	// The client keeps no copy of the ledger's eligibility rule, so a row reversed
	// on a page it has not fetched still reads reversed.
	Reversed bool `json:"reversed"`
	// Reversible is true exactly when Ledger.ReverseTransaction would accept the
	// row now (ledger.ReversalRefusal, the same function), so the client never
	// works out eligibility itself. False on a row the page did not compute it for
	// (POST's reply).
	Reversible bool `json:"reversible"`
	// Counted is true when a cek kas has counted this row, which picks the date a
	// reversal would take (the original's own day, or today).
	Counted bool `json:"counted"`
	// ReversesOccurredOn is the date of the row a reversal points at, set only on
	// a reversal: an unnamed reversal's label states it, and the original may sit
	// on another page.
	ReversesOccurredOn *string `json:"reverses_occurred_on"`

	// ReceiptIDs is every receipt attached to this row, oldest first (#154):
	// always present, [] rather than null when none. A freshly posted row
	// (every toTransactionResponse caller but toTransactionsPageResponse) gets
	// [], since a receipt names an existing transaction id. GET
	// /api/transactions overwrites it with a batched lookup (listTransactions).
	// A transfer's list row carries both legs' receipts (#738), oldest first.
	ReceiptIDs []int64 `json:"receipt_ids"`
}

func toTransactionResponse(t store.Transaction) transactionResponse {
	return transactionResponse{
		ID:              t.ID,
		AccountID:       t.AccountID,
		PurposeID:       t.PurposeID,
		Direction:       t.Direction,
		Amount:          t.Amount,
		OccurredOn:      t.OccurredOn,
		Kind:            t.Kind,
		MemberID:        t.MemberID,
		DuesPeriod:      t.DuesPeriod,
		ReimbursementID: t.ReimbursementID,
		TransferID:      t.TransferID,

		ReversesTransactionID: t.ReversesTransactionID,

		Note:      t.Note,
		CreatedAt: t.CreatedAt,

		ReceiptIDs: []int64{},
	}
}

// toTransactionsPageResponse is GET /api/transactions's row mapper: it adds a
// label's facts to what toTransactionResponse answers for a posted row (#257).
//
// MemberName merges row.MemberName and row.SettlementMemberName in Go, not
// SQL: sqlc 1.31.1 mistypes a SQL-side COALESCE of the two as NOT NULL (see
// ListTransactionsPage). They are mutually exclusive (a dues row's member_id
// is never on a kind='reimbursement' row, and only that kind settles a
// claim), so the if/else never chooses between two real names.
//
// TransferFromName/TransferToName pick the account pair or the purpose pair
// by row.TransferKind, for the same sqlc reason.
//
// today is the fund's calendar day, which the reversible flag needs (a counted
// row dated after it cannot be reversed, ADR-042).
func toTransactionsPageResponse(t store.ListTransactionsPageRow, today string) transactionResponse {
	resp := toTransactionResponse(store.Transaction{
		ID: t.ID, FundID: t.FundID, AccountID: t.AccountID, PurposeID: t.PurposeID,
		Direction: t.Direction, Amount: t.Amount, OccurredOn: t.OccurredOn, Kind: t.Kind,
		MemberID: t.MemberID, DuesPeriod: t.DuesPeriod, ReimbursementID: t.ReimbursementID,
		TransferID: t.TransferID, ReversesTransactionID: t.ReversesTransactionID,
		Note: t.Note, CreatedAt: t.CreatedAt,
	})

	resp.AccountName = t.AccountName
	resp.PurposeKind = t.PurposeKind
	resp.ClaimNote = t.ClaimNote
	resp.TransferKind = t.TransferKind
	resp.TransferCorrectsTransactionID = t.TransferCorrectsTransactionID
	resp.TransferReason = t.TransferReason
	resp.EffectivePurposeID = t.EffectivePurposeID

	resp.Reversed = t.Reversed != 0
	resp.Counted = t.Counted != 0
	resp.ReversesOccurredOn = t.ReversesOccurredOn
	resp.Reversible = ledger.ReversalRefusal(ledger.ReversalFacts{
		Kind:                ledger.TransactionKind(t.Kind),
		OccurredOn:          t.OccurredOn,
		IsReversal:          t.ReversesTransactionID != nil,
		Reversed:            resp.Reversed,
		IsReconciliationFix: t.IsReconciliationFix != 0,
		EffectiveClosed:     t.EffectiveClosedOn != nil,
		Counted:             resp.Counted,
	}, today) == nil

	switch {
	case t.MemberName != nil:
		resp.MemberName = t.MemberName
	case t.SettlementMemberName != nil:
		resp.MemberName = t.SettlementMemberName
	}

	if t.TransferKind != nil {
		switch *t.TransferKind {
		case string(ledger.TransferKindBetweenAccounts):
			resp.TransferFromName = t.TransferFromAccountName
			resp.TransferToName = t.TransferToAccountName
		case string(ledger.TransferKindReclassPurpose):
			resp.TransferFromName = t.TransferFromPurposeName
			resp.TransferToName = t.TransferToPurposeName
			// A correction reads as what happened to the row it corrects (#654):
			// for an 'out' row the pair's money runs from the new pos back to
			// the stored one, so the names swap.
			if t.TransferCorrectsDirection != nil && *t.TransferCorrectsDirection == string(ledger.DirectionOut) {
				resp.TransferFromName, resp.TransferToName = resp.TransferToName, resp.TransferFromName
			}
		}
	}

	return resp
}

// createTransaction is POST /api/transactions: wraps Ledger.PostTransaction.
// Handlers decode and pass through; amount > 0, direction shape and
// occurred_on validity are PostTransaction's job alone (ADR-027).
//
// Keyed (ADR-043).
func (a *api) createTransaction(w http.ResponseWriter, r *http.Request) {
	var req transactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	idem := idempotencyFrom(r)
	posted, err := a.ledger.PostTransaction(r.Context(), ledger.PostTransactionParams{
		FundID:       fund.ID,
		AccountID:    req.AccountID,
		PurposeID:    req.PurposeID,
		Direction:    req.Direction,
		Amount:       money.Amount(req.Amount),
		OccurredOn:   req.OccurredOn,
		Note:         req.Note,
		IsAdjustment: req.IsAdjustment,
		MemberID:     req.MemberID,
		Idempotency:  idem,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	markReplay(w, r, a.log(r), idem)
	writeJSON(w, http.StatusCreated, toTransactionResponse(posted))
}

// transactionsPageSize is GET /api/transactions's fixed page size (#225,
// ADR-032): 25 a page, no client-supplied limit.
const transactionsPageSize = 25

// occurredOnLayout mirrors internal/ledger's unexported const (transaction.go).
// A cursor is this route's opaque wire format, so this package validates its
// date half itself rather than reaching into ledger.
const occurredOnLayout = "2006-01-02"

// duesPeriodLayout mirrors internal/ledger's unexported const (dues.go); the
// dues_period filter is this direct-CRUD route's input (ADR-027), not
// validated by a ledger call.
const duesPeriodLayout = "2006-01"

// transactionsPageResponse is GET /api/transactions's envelope (#225): the
// page of rows plus an opaque cursor for the next, null on the last page. The
// other paged lists answer the same shape with their own rows key:
// {"transactions":[...],"next_cursor":...}.
type transactionsPageResponse struct {
	Transactions []transactionResponse `json:"transactions"`
	NextCursor   *string               `json:"next_cursor"`
}

// isAllDigits is the "what was that 50.000?" detector (#225, ADR-032): a
// trimmed q of ASCII digits also becomes an exact amount match. s is assumed
// non-empty.
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validDuesPeriod reports whether s is a real "YYYY-MM" calendar month,
// mirroring ledger's validateDuesPeriod (see duesPeriodLayout).
func validDuesPeriod(s string) bool {
	t, err := time.Parse(duesPeriodLayout, s)
	return err == nil && t.Format(duesPeriodLayout) == s
}

// transactionFilters are Riwayat -> Transaksi's four filters (#424), the
// public report's: read once for the list and once for its month summary.
type transactionFilters struct {
	memberID  *int64
	purposeID *int64
	month     string
	direction string
}

// parseTransactionFilters reads ?member_id=, ?purpose_id=, ?month= and
// ?direction=, writing the 400 itself on a malformed one.
func parseTransactionFilters(w http.ResponseWriter, r *http.Request) (transactionFilters, bool) {
	var f transactionFilters
	if raw := r.URL.Query().Get("member_id"); raw != "" {
		memberID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "member_id", "The member_id filter is not a valid number.")
			return f, false
		}
		f.memberID = &memberID
	}

	// An id that is not a positive integer is a malformed request, not an empty
	// result: a filter the treasurer cannot see is one she cannot correct.
	if raw := r.URL.Query().Get("purpose_id"); raw != "" {
		purposeID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || purposeID <= 0 {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "purpose_id", "The purpose_id filter is not a valid number.")
			return f, false
		}
		f.purposeID = &purposeID
	}

	if month := r.URL.Query().Get("month"); month != "" {
		if !validDuesPeriod(month) {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "month", "The month filter is not a valid \"YYYY-MM\" month.")
			return f, false
		}
		f.month = month
	}

	if direction := r.URL.Query().Get("direction"); direction != "" {
		if direction != string(ledger.DirectionIn) && direction != string(ledger.DirectionOut) {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "direction", "The direction filter must be \"in\" or \"out\".")
			return f, false
		}
		f.direction = direction
	}
	return f, true
}

// transactionSummaryResponse is GET /api/transactions/summary's body: the
// public report's walk for one month (ADR-038). full says whether the
// balances apply (the whole fund, or one pos); under a member or direction
// filter only in and out mean anything, and the rest are zero with no dates.
type transactionSummaryResponse struct {
	Full        bool    `json:"full"`
	StartOn     *string `json:"start_on"`
	EndOn       *string `json:"end_on"`
	Start       int64   `json:"start"`
	Openings    int64   `json:"openings"`
	In          int64   `json:"in"`
	Out         int64   `json:"out"`
	Adjustments int64   `json:"adjustments"`
	Moved       int64   `json:"moved"`
	End         int64   `json:"end"`
}

// transactionSummary is GET /api/transactions/summary (#751): Riwayat's
// month summary, Ledger.MonthWalk under the list's filters - the same
// derivation the public report prints, so the two never disagree. ?month= is
// required: a summary of "every month" would be the whole ledger, which
// Beranda already shows.
func (a *api) transactionSummary(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}
	filters, ok := parseTransactionFilters(w, r)
	if !ok {
		return
	}
	if filters.month == "" {
		writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "month", "The summary needs a \"YYYY-MM\" month.")
		return
	}

	walk, err := a.ledger.MonthWalk(r.Context(), ledger.ReportParams{
		FundID:    fund.ID,
		Month:     filters.month,
		PurposeID: filters.purposeID,
		MemberID:  filters.memberID,
		Direction: ledger.Direction(filters.direction),
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}
	resp := transactionSummaryResponse{
		Full: walk.Full, Start: walk.Start.Int64(), Openings: walk.Openings.Int64(), In: walk.In.Int64(), Out: walk.Out.Int64(),
		Adjustments: walk.Adjustments.Int64(), Moved: walk.Moved.Int64(), End: walk.End.Int64(),
	}
	if walk.Full {
		resp.StartOn, resp.EndOn = &walk.StartOn, &walk.EndOn
	}
	writeJSON(w, http.StatusOK, resp)
}

// listTransactions is GET /api/transactions (#225, ADR-032): newest-first,
// keyset-paged on (occurred_on DESC, id DESC), never LIMIT/OFFSET, because
// occurred_on is backdatable (PRD section 7.2) and an offset would skip or
// duplicate a row landing mid-list between page fetches. A direct-CRUD read
// (ADR-027), so it calls a.queries directly.
//
// One row per movement (#738): the two legs of a transfer come back as ONE row,
// chosen in SQL before the cursor cuts the page (see ListTransactionsPage), so a
// page boundary can neither split a pair nor skip one. The row is a real leg:
// the 'out' leg when both legs match the filters, otherwise whichever matched,
// so its direction, account_id, purpose_id and account_name are THAT LEG'S and
// mean nothing about the movement. A client renders a kind='transfer' row from
// transfer_kind, transfer_from_name and transfer_to_name, which are the same
// whichever leg it is. ?direction= excludes transfers: a movement between two
// places is neither in nor out.
//
// ?q= searches note, purpose name and member name (case-insensitive
// substring, see ListTransactionsPage) plus an exact amount match when q is
// all digits. ?member_id=, ?purpose_id=, ?month= ("YYYY-MM") and
// ?direction= (in|out) are Riwayat -> Transaksi's filters (#424), the public
// report's four; ?dues_period= is Dues/MemberPayments.tsx's payment history
// alone. All compose with q and the cursor.
//
// ListTransactionsPage is asked for one row more than the page size, so the
// handler can tell an empty next page from a next page without a second
// round trip; the extra row is trimmed and its (occurred_on, id) becomes
// next_cursor.
func (a *api) listTransactions(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	params := store.ListTransactionsPageParams{
		FundID:    fund.ID,
		PageLimit: transactionsPageSize + 1,
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		occurredOn, id, ok := decodeDateCursor(cursor)
		if !ok {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "cursor", "The cursor is not valid.")
			return
		}
		params.CursorOccurredOn = occurredOn
		params.CursorID = &id
	}

	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params.Q = q
		if isAllDigits(q) {
			if amount, err := strconv.ParseInt(q, 10, 64); err == nil {
				params.QAmount = &amount
			}
			// A q too long to fit int64 still searches note, purpose and member text; it
			// just never also matches an amount.
		}
	}

	filters, ok := parseTransactionFilters(w, r)
	if !ok {
		return
	}
	if filters.memberID != nil {
		params.MemberID = *filters.memberID
	}
	if filters.purposeID != nil {
		params.PurposeID = *filters.purposeID
	}
	// Set only when given: these params are untyped (sqlc's nullable
	// arguments), and an empty string is a filter, where nil is none.
	if filters.month != "" {
		params.Month = filters.month
	}
	if filters.direction != "" {
		params.Direction = filters.direction
	}

	if duesPeriod := r.URL.Query().Get("dues_period"); duesPeriod != "" {
		if !validDuesPeriod(duesPeriod) {
			writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "dues_period", "The dues_period filter is not a valid \"YYYY-MM\" period.")
			return
		}
		params.DuesPeriod = duesPeriod
	}

	rows, err := a.queries.ListTransactionsPage(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}
	today, err := ledger.FundToday(fund, a.now())
	if err != nil {
		a.log(r).Error("reading the fund's day", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	var nextCursor *string
	if len(rows) > transactionsPageSize {
		rows = rows[:transactionsPageSize]
		last := rows[len(rows)-1]
		encoded := encodeCursor(last.OccurredOn, last.ID)
		nextCursor = &encoded
	}

	// One batched, fund-scoped receipt lookup for the page (#154), never a query
	// per row; see receiptIDsForTransactions.
	// A transfer is one row here (#738) but two ledger rows, and a photo may sit on
	// either leg, so a transfer's lookup covers both of its legs.
	ids := make([]int64, 0, len(rows))
	for _, t := range rows {
		ids = append(ids, receiptParentIDs(t)...)
	}
	receiptIDs, err := a.receiptIDsForTransactions(r.Context(), fund.ID, ids)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]transactionResponse, 0, len(rows))
	for _, t := range rows {
		row := toTransactionsPageResponse(t, today)
		parents := receiptParentIDs(t)
		var merged []int64
		for _, id := range parents {
			merged = append(merged, receiptIDs[id]...)
		}
		// Receipt ids rise with upload time, so sorting two legs' lists keeps
		// receipt_ids "oldest first". One list (any other row) is already in order.
		if len(parents) > 1 {
			slices.Sort(merged)
		}
		row.ReceiptIDs = orEmptyReceiptIDs(merged)
		resp = append(resp, row)
	}
	writeJSON(w, http.StatusOK, transactionsPageResponse{Transactions: resp, NextCursor: nextCursor})
}

// receiptParentIDs is every transaction id whose receipts belong on one listed
// row: its own, and for a transfer (one row standing for two legs, #738) both
// legs', whichever leg ListTransactionsPage picked to represent it.
func receiptParentIDs(t store.ListTransactionsPageRow) []int64 {
	if t.TransferID == nil || t.TransferOutLegID == nil || t.TransferInLegID == nil {
		return []int64{t.ID}
	}
	return []int64{*t.TransferOutLegID, *t.TransferInLegID}
}

// reverseTransactionRequest is POST /api/transactions/{id}/reversal's body: a
// note and nothing else (ADR-042). account_id, purpose_id, amount, direction,
// member_id, dues_period and occurred_on are never accepted here;
// Ledger.ReverseTransaction copies the first six from the original row and
// picks the date itself. decodeJSON ignores unknown fields, so a stray
// occurred_on is dropped, never honoured.
type reverseTransactionRequest struct {
	Note *string `json:"note"`
}

// reverseTransaction is POST /api/transactions/{id}/reversal: wraps
// Ledger.ReverseTransaction; every refusal is the ledger's own named error
// (ADR-042), reaching the client through mapLedgerError. Once-only by the
// schema, so no idempotency key (ADR-043). The response is the posted reversal
// row as transactionResponse.
func (a *api) reverseTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The transaction id is not a valid number.")
		return
	}

	var req reverseTransactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	reversal, err := a.ledger.ReverseTransaction(r.Context(), ledger.ReverseTransactionParams{
		FundID:        fund.ID,
		TransactionID: id,
		Note:          req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransactionResponse(reversal))
}
