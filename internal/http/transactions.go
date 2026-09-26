package http

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// transactionRequest is POST /api/transactions's body: one ordinary entry or
// one correction (PRD section 7.2, section 7.6). A pass-through movement is not a special
// shape here - it is an ordinary transaction tagged to a pass-through
// purpose (#66) - and a correction isn't either: IsAdjustment selects
// kind='adjustment' over kind='normal', mirroring PostTransactionParams's own
// field on the wire rather than exposing a raw kind (ADR-027's reasoning
// behind passThroughPurposeRequest's missing Kind applies here too).
type transactionRequest struct {
	AccountID    int64   `json:"account_id"`
	PurposeID    int64   `json:"purpose_id"`
	Direction    string  `json:"direction"`
	Amount       int64   `json:"amount"`
	OccurredOn   string  `json:"occurred_on"`
	Note         *string `json:"note"`
	IsAdjustment bool    `json:"is_adjustment"`
}

// transactionResponse is the wire shape of a transaction row - every kind the
// ledger can post (normal, adjustment, dues, opening, transfer,
// reimbursement), since GET /api/transactions lists all of them and this type
// is what both routes in this file share. No fund_id, same reasoning as the
// other response types in this package.
//
// The fields after CreatedAt (#257) are the facts TransactionList.tsx needs
// to build a system row's display label, without a second request per row -
// they are set only by toTransactionsPageResponse (GET /api/transactions'
// listing), not by toTransactionResponse (POST's own single-row reply): a
// freshly-posted row has no reconciliation line naming it yet and, for a
// transfer, no guaranteed sibling leg inside the same request/response
// round trip, so a caller reading straight off POST's reply gets these as
// their zero values rather than a misleadingly confident answer.
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
	// The dues payment this row reverses (ADR-029), set only on a reversal.
	// It is on the wire for the same reason the row exists: a client reading
	// a transaction has no other way to tell a reversal apart from an
	// ordinary correction, or to say which payment it undid.
	ReversesTransactionID *int64  `json:"reverses_transaction_id"`
	Note                  *string `json:"note"`
	CreatedAt             int64   `json:"created_at"`

	// AccountName is this row's own account (location) - the opening
	// balance and reconciliation-fix labels are built from it. Always
	// present on a listed row (account_id is NOT NULL and an account with
	// any transaction against it can never be deleted).
	AccountName string `json:"account_name"`
	// MemberName is the dues member for a dues payment or reversal, or the
	// settled claim's member for a reimbursement payout - never both, and
	// nil for every other kind. See toTransactionsPageResponse for why the
	// merge happens here rather than in SQL.
	MemberName *string `json:"member_name"`
	// ClaimNote is the settled claim's own note (reimbursement.note) - set
	// only on a kind='reimbursement' row, so the Talangan settlement label
	// can show what the claim itself was for even though the settlement's
	// own Note is always nil (#257: nothing generated is ever stored).
	ClaimNote *string `json:"claim_note"`
	// TransferKind is the pair's own kind ('between_accounts' or
	// 'reclass_purpose'), nil outside kind='transfer'. TransferFromName/
	// TransferToName are that kind's two location or two purpose names, in
	// the transfer's own from->to order regardless of whether this row is
	// the 'out' or the 'in' leg (ListTransactionsPage's own comment has the
	// full reasoning), nil outside kind='transfer'.
	TransferKind     *string `json:"transfer_kind"`
	TransferFromName *string `json:"transfer_from_name"`
	TransferToName   *string `json:"transfer_to_name"`
	// IsReconciliationFix is true exactly when a reconciliation_line names
	// this row as the entry that squared its gap (resolution 'adjusted') -
	// never true for an 'entry_added' fix (ADR-024: that entry is
	// self-explanatory, posted with the treasurer's own purpose and note)
	// or for an ordinary adjustment (ADR-024).
	IsReconciliationFix bool `json:"is_reconciliation_fix"`
	// TransferCorrectsTransactionID is this row's own transfer's link
	// (ADR-033, #267) - non-nil exactly on a purpose correction's two legs,
	// nil on every other transfer leg including a roll's. It is what lets
	// #276's row label tell a correction leg apart from a roll, both of
	// which are otherwise the same kind='transfer', transfer_kind='reclass_purpose'
	// shape.
	TransferCorrectsTransactionID *int64 `json:"transfer_corrects_transaction_id"`
	// EffectivePurposeID is the tag this row's money is under now: its own
	// PurposeID until a correction moves it (ADR-033), then the latest
	// correction's target. Equal to PurposeID on every uncorrected row, so
	// "has this been corrected?" is EffectivePurposeID != PurposeID rather
	// than a second boolean saying the same thing - which is what this
	// field replaced before either ever shipped.
	//
	// The list still RENDERS PurposeID (ADR-033): the ledger sums stored
	// tags, so a row showing its effective one would put the screen out of
	// step with the balances. This is for #276's correction dialog, which
	// must build its pair from where the money actually is, and for the
	// marker that says a correction exists.
	EffectivePurposeID int64 `json:"effective_purpose_id"`

	// ReceiptIDs is every receipt attached to this row, oldest first (#154):
	// always present, [] rather than null when there are none. A freshly
	// posted row (every toTransactionResponse caller but
	// toTransactionsPageResponse below) always gets []: a receipt names an
	// existing transaction id, so nothing could have attached to a row
	// before this response's own request created it. GET /api/transactions
	// is the one caller that overwrites this with a real, batched lookup
	// (listTransactions), because that row may already be old. A transfer's
	// two legs are ordinary rows here too, each with its own id, so each
	// leg's ReceiptIDs reflects only what was uploaded against that specific
	// leg - never the pair as a whole.
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

// toTransactionsPageResponse is GET /api/transactions's own row mapper - the
// listing that carries a label's facts, on top of everything
// toTransactionResponse already answers for a single posted row (#257).
//
// MemberName merges row.MemberName and row.SettlementMemberName in Go, not
// SQL: ListTransactionsPage's own comment documents sqlc 1.31.1 mistyping a
// SQL-side COALESCE of the same two columns as NOT NULL. The two are
// mutually exclusive by construction (a dues row's member_id is never set
// on a kind='reimbursement' row, and only that kind ever settles a claim),
// so this if/else never has to choose between two real names.
//
// TransferFromName/TransferToName pick the account pair or the purpose pair
// by row.TransferKind, for the same sqlc-typing reason: ListTransactionsPage
// exposes all four names as plain nullable columns rather than a SQL CASE
// between them.
func toTransactionsPageResponse(t store.ListTransactionsPageRow) transactionResponse {
	resp := toTransactionResponse(store.Transaction{
		ID: t.ID, FundID: t.FundID, AccountID: t.AccountID, PurposeID: t.PurposeID,
		Direction: t.Direction, Amount: t.Amount, OccurredOn: t.OccurredOn, Kind: t.Kind,
		MemberID: t.MemberID, DuesPeriod: t.DuesPeriod, ReimbursementID: t.ReimbursementID,
		TransferID: t.TransferID, ReversesTransactionID: t.ReversesTransactionID,
		Note: t.Note, CreatedAt: t.CreatedAt,
	})

	resp.AccountName = t.AccountName
	resp.ClaimNote = t.ClaimNote
	resp.TransferKind = t.TransferKind
	resp.IsReconciliationFix = t.IsReconciliationFix != 0
	resp.TransferCorrectsTransactionID = t.TransferCorrectsTransactionID
	resp.EffectivePurposeID = t.EffectivePurposeID

	switch {
	case t.MemberName != nil:
		resp.MemberName = t.MemberName
	case t.SettlementMemberName != nil:
		resp.MemberName = t.SettlementMemberName
	}

	if t.TransferKind != nil {
		switch *t.TransferKind {
		case "between_accounts":
			resp.TransferFromName = t.TransferFromAccountName
			resp.TransferToName = t.TransferToAccountName
		case "reclass_purpose":
			resp.TransferFromName = t.TransferFromPurposeName
			resp.TransferToName = t.TransferToPurposeName
		}
	}

	return resp
}

// createTransaction is POST /api/transactions: wraps Ledger.PostTransaction.
// Handlers decode and pass through - amount > 0, direction shape and
// occurred_on's calendar validity are PostTransaction's job alone, checked
// once inside the ledger rather than a second time here (ADR-027).
func (a *api) createTransaction(w http.ResponseWriter, r *http.Request) {
	var req transactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	posted, err := a.ledger.PostTransaction(r.Context(), ledger.PostTransactionParams{
		FundID:       fund.ID,
		AccountID:    req.AccountID,
		PurposeID:    req.PurposeID,
		Direction:    req.Direction,
		Amount:       money.Amount(req.Amount),
		OccurredOn:   req.OccurredOn,
		Note:         req.Note,
		IsAdjustment: req.IsAdjustment,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransactionResponse(posted))
}

// transactionsPageSize is GET /api/transactions's fixed page size (#225,
// ADR-032 "Lists: paging and search"): 25 a page, no client-supplied limit.
const transactionsPageSize = 25

// occurredOnLayout mirrors internal/ledger's own unexported const of the
// same name (transaction.go) - both read the schema's business-date shape
// (ADR-024), but this package validates a cursor's date half itself rather
// than reaching into ledger for it: a cursor is this route's own opaque
// wire format, never a value ledger validates on anyone's behalf.
const occurredOnLayout = "2006-01-02"

// duesPeriodLayout mirrors internal/ledger's own unexported const
// (dues.go). Same reasoning as occurredOnLayout above: the dues_period
// query filter is this direct-CRUD route's own input (ADR-027), not
// something routed through a ledger call that would validate it there.
const duesPeriodLayout = "2006-01"

// transactionsPageResponse is GET /api/transactions's envelope (#225): the
// page of rows plus an opaque cursor for the next one, null once there is
// no further page. No list route in this package returned an envelope
// before this - every other GET here still answers a bare, unpaginated
// array - so {"transactions":[...],"next_cursor":...} is the shape this
// slice chose, there being no existing convention to follow.
type transactionsPageResponse struct {
	Transactions []transactionResponse `json:"transactions"`
	NextCursor   *string               `json:"next_cursor"`
}

// encodeTransactionsCursor/decodeTransactionsCursor turn a page's last row
// into an opaque cursor and back - base64 of "occurred_on|id", the same
// pair ListTransactionsPage's keyset WHERE clause compares against. Opaque
// to the client on purpose: nothing about the wire format is documented or
// meant to be parsed by it, only round-tripped.
func encodeTransactionsCursor(occurredOn string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(occurredOn + "|" + strconv.FormatInt(id, 10)))
}

// decodeTransactionsCursor rejects anything that doesn't round-trip to a
// real calendar date plus a positive id - the shape listTransactions below
// answers 400 invalid_argument for, per #225's "malformed cursor -> 400".
func decodeTransactionsCursor(raw string) (occurredOn string, id int64, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", 0, false
	}
	occurredOn, idPart, found := strings.Cut(string(decoded), "|")
	if !found {
		return "", 0, false
	}
	t, err := time.Parse(occurredOnLayout, occurredOn)
	if err != nil || t.Format(occurredOnLayout) != occurredOn {
		return "", 0, false
	}
	id, err = strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return occurredOn, id, true
}

// isAllDigits is the "what was that 50.000?" detector (#225, ADR-032): a
// trimmed q that is entirely ASCII digits also becomes an exact amount
// match alongside the text search. s is assumed already non-empty - callers
// only reach this after confirming that.
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validDuesPeriod reports whether s is a real "YYYY-MM" calendar month,
// mirroring internal/ledger's own validateDuesPeriod (dues.go) - see
// duesPeriodLayout's comment above for why this route keeps its own copy
// rather than importing that one.
func validDuesPeriod(s string) bool {
	t, err := time.Parse(duesPeriodLayout, s)
	return err == nil && t.Format(duesPeriodLayout) == s
}

// listTransactions is GET /api/transactions (#225, ADR-032 "Lists: paging
// and search"): newest-first, keyset-paged on (occurred_on DESC, id DESC) -
// never LIMIT/OFFSET, because occurred_on is backdatable (PRD section 7.2)
// and an offset would silently skip or duplicate a row that lands in the
// middle of the list between two page fetches. A direct-CRUD read
// (ADR-027) - no derived invariant beyond the sum itself - so it calls
// a.queries directly, the same split listAccounts and listPurposes already
// use.
//
// ?q= searches note, purpose name and member name (case-insensitive
// substring, ListTransactionsPage's own doc comment has the reasoning) plus
// an exact amount match when q is all digits. ?member_id= and
// ?dues_period= are exact-match filters for
// Dues/MemberPayments.tsx's payment history panel - not a Riwayat UI
// filter, undocumented in copy/UI on purpose (ADR-032 holds filters to M7).
//
// ?purpose_id= is the one filter that does reach the UI (#262): ADR-032
// names Riwayat -> Transaksi filtered to a purpose as the only route to a
// closed envelope, so History/Transactions.tsx renders it as a deep link it
// can clear - never as a chooser it offers, which would be M7's filter set
// arriving early. All three compose with q and with the cursor.
//
// ListTransactionsPage is asked for one row more than the page size so this
// handler can tell "the next page is empty" apart from "there is a next
// page" without a second round trip - the extra row, if it came back, is
// trimmed before the response and its own (occurred_on, id) becomes
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
		occurredOn, id, ok := decodeTransactionsCursor(cursor)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The cursor is not valid.")
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
			// A q too long to fit int64 (overflow) still searches note,
			// purpose and member text above - it just never also matches an
			// amount, which is the only thing this branch would have added.
		}
	}

	if raw := r.URL.Query().Get("member_id"); raw != "" {
		memberID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The member_id filter is not a valid number.")
			return
		}
		params.MemberID = memberID
	}

	if duesPeriod := r.URL.Query().Get("dues_period"); duesPeriod != "" {
		if !validDuesPeriod(duesPeriod) {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The dues_period filter is not a valid \"YYYY-MM\" period.")
			return
		}
		params.DuesPeriod = duesPeriod
	}

	// An id that is not a positive integer is a malformed request, not an
	// empty result: a filter the treasurer cannot see is one she cannot
	// correct, so this says so rather than quietly listing everything.
	if raw := r.URL.Query().Get("purpose_id"); raw != "" {
		purposeID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || purposeID <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The purpose_id filter is not a valid number.")
			return
		}
		params.PurposeID = purposeID
	}

	rows, err := a.queries.ListTransactionsPage(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	var nextCursor *string
	if len(rows) > transactionsPageSize {
		rows = rows[:transactionsPageSize]
		last := rows[len(rows)-1]
		encoded := encodeTransactionsCursor(last.OccurredOn, last.ID)
		nextCursor = &encoded
	}

	// One batched, fund-scoped receipt lookup for the whole page (#154),
	// never one query per row - see receiptIDsForTransactions' own comment.
	ids := make([]int64, len(rows))
	for i, t := range rows {
		ids[i] = t.ID
	}
	receiptIDs, err := a.receiptIDsForTransactions(r.Context(), fund.ID, ids)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]transactionResponse, 0, len(rows))
	for _, t := range rows {
		row := toTransactionsPageResponse(t)
		row.ReceiptIDs = orEmptyReceiptIDs(receiptIDs[t.ID])
		resp = append(resp, row)
	}
	writeJSON(w, http.StatusOK, transactionsPageResponse{Transactions: resp, NextCursor: nextCursor})
}
