package http

import (
	"encoding/base64"
	"net/http"
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
	AccountID    int64   `json:"account_id"`
	PurposeID    int64   `json:"purpose_id"`
	Direction    string  `json:"direction"`
	Amount       int64   `json:"amount"`
	OccurredOn   string  `json:"occurred_on"`
	Note         *string `json:"note"`
	IsAdjustment bool    `json:"is_adjustment"`

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
	// whichever leg this row is (see ListTransactionsPage), nil outside
	// kind='transfer'.
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
	// A transfer's two legs are ordinary rows, each with only its own receipts.
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
	resp.ClaimNote = t.ClaimNote
	resp.TransferKind = t.TransferKind
	resp.TransferCorrectsTransactionID = t.TransferCorrectsTransactionID
	resp.TransferReason = t.TransferReason
	resp.EffectivePurposeID = t.EffectivePurposeID

	resp.Reversed = t.Reversed != 0
	resp.Counted = t.Counted != 0
	resp.ReversesOccurredOn = t.ReversesOccurredOn
	resp.Reversible = ledger.ReversalRefusal(ledger.ReversalFacts{
		Kind:                t.Kind,
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
		Now:          a.now(),
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

// encodeTransactionsCursor/decodeTransactionsCursor turn a page's last row
// into an opaque cursor and back: base64 of "occurred_on|id", the pair
// ListTransactionsPage's keyset WHERE compares. Opaque on purpose; the
// client only round-trips it.
func encodeTransactionsCursor(occurredOn string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(occurredOn + "|" + strconv.FormatInt(id, 10)))
}

// decodeTransactionsCursor rejects anything that does not round-trip to a
// real calendar date plus a positive id; listTransactions answers 400
// invalid_argument for it (#225).
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

// listTransactions is GET /api/transactions (#225, ADR-032): newest-first,
// keyset-paged on (occurred_on DESC, id DESC), never LIMIT/OFFSET, because
// occurred_on is backdatable (PRD section 7.2) and an offset would skip or
// duplicate a row landing mid-list between page fetches. A direct-CRUD read
// (ADR-027), so it calls a.queries directly.
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
			// A q too long to fit int64 still searches note, purpose and member text; it
			// just never also matches an amount.
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

	// An id that is not a positive integer is a malformed request, not an empty
	// result: a filter the treasurer cannot see is one she cannot correct.
	if raw := r.URL.Query().Get("purpose_id"); raw != "" {
		purposeID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || purposeID <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The purpose_id filter is not a valid number.")
			return
		}
		params.PurposeID = purposeID
	}

	if month := r.URL.Query().Get("month"); month != "" {
		if !validDuesPeriod(month) {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The month filter is not a valid \"YYYY-MM\" month.")
			return
		}
		params.Month = month
	}

	if direction := r.URL.Query().Get("direction"); direction != "" {
		if direction != "in" && direction != "out" {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The direction filter must be \"in\" or \"out\".")
			return
		}
		params.Direction = direction
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
		encoded := encodeTransactionsCursor(last.OccurredOn, last.ID)
		nextCursor = &encoded
	}

	// One batched, fund-scoped receipt lookup for the page (#154), never a query
	// per row; see receiptIDsForTransactions.
	ids := make([]int64, len(rows))
	for i, t := range rows {
		ids[i] = t.ID
	}
	receiptIDs, err := a.receiptIDsForTransactions(r.Context(), fund.ID, ids)
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	resp := make([]transactionResponse, 0, len(rows))
	for _, t := range rows {
		row := toTransactionsPageResponse(t, today)
		row.ReceiptIDs = orEmptyReceiptIDs(receiptIDs[t.ID])
		resp = append(resp, row)
	}
	writeJSON(w, http.StatusOK, transactionsPageResponse{Transactions: resp, NextCursor: nextCursor})
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
		Now:           a.now(),
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
