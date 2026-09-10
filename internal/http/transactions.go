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
	}
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

	resp := make([]transactionResponse, 0, len(rows))
	for _, t := range rows {
		resp = append(resp, toTransactionResponse(t))
	}
	writeJSON(w, http.StatusOK, transactionsPageResponse{Transactions: resp, NextCursor: nextCursor})
}
