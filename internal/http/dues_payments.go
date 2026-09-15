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

// duesPaymentPeriod is one period within a POST /api/dues-payments request:
// the period paid and the amount paid toward it. The schema's kind='dues'
// CHECK requires exactly one dues_period per row (ADR-024), and paying
// several months in one sitting is the treasurer's real workflow (PRD section 7.3) -
// not one row that means three things - so periods is an array on the wire
// and never flattened into a total.
type duesPaymentPeriod struct {
	DuesPeriod string `json:"dues_period"`
	Amount     int64  `json:"amount"`
}

// duesPaymentRequest is POST /api/dues-payments's body: one member paying
// one or more periods in the same sitting, on the same account and purpose,
// dated and noted the same way across all of them - Periods is the only part
// that repeats.
type duesPaymentRequest struct {
	AccountID  int64               `json:"account_id"`
	PurposeID  int64               `json:"purpose_id"`
	MemberID   int64               `json:"member_id"`
	OccurredOn string              `json:"occurred_on"`
	Note       *string             `json:"note"`
	Periods    []duesPaymentPeriod `json:"periods"`
}

// createDuesPayment is POST /api/dues-payments: makes one call to
// Ledger.PostDuesPayments, which posts one row per entry in Periods inside a
// single database transaction - never flattened into one multi-period row,
// and the response echoes that back as one array entry per posted row, in
// the order given.
//
// Periods must not be empty - a request-shape check this handler owns, the
// same way resolveMember owns "the id in the path is a valid number": an
// empty array would otherwise reach the ledger meaning "post nothing," which
// PostDuesPayments itself also rejects as ErrInvalidArgument (the two layers
// deliberately agree). Checking it here first keeps this handler's existing
// 400 message specific to the request body, before a fund lookup even runs.
//
// A failure on any period - including one partway through a multi-period
// batch - rolls back every row PostDuesPayments would otherwise have posted
// for this call: the ledger validates every period before writing any of
// them, and writes all of them inside one transaction. Nothing here loops
// over PostDuesPayment per period any more (#96) - that shape left an
// earlier period's row standing when a later one failed, because each call
// owned and committed its own transaction independently.
func (a *api) createDuesPayment(w http.ResponseWriter, r *http.Request) {
	var req duesPaymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if len(req.Periods) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "At least one period must be given.")
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	periods := make([]ledger.PeriodAmount, 0, len(req.Periods))
	for _, period := range req.Periods {
		periods = append(periods, ledger.PeriodAmount{
			DuesPeriod: period.DuesPeriod,
			Amount:     money.Amount(period.Amount),
		})
	}

	posted, err := a.ledger.PostDuesPayments(r.Context(), ledger.PostDuesPaymentsParams{
		FundID:     fund.ID,
		AccountID:  req.AccountID,
		PurposeID:  req.PurposeID,
		MemberID:   req.MemberID,
		OccurredOn: req.OccurredOn,
		Note:       req.Note,
		Periods:    periods,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	resp := make([]transactionResponse, 0, len(posted))
	for _, row := range posted {
		resp = append(resp, toTransactionResponse(row))
	}

	writeJSON(w, http.StatusCreated, resp)
}

// reverseDuesPaymentRequest is POST /api/dues-payments/{id}/reversal's body.
// Deliberately narrow: account_id, purpose_id, amount, member_id and
// dues_period are never accepted on the wire here - Ledger.ReverseDuesPayment
// copies all five from the original row itself (ADR-029), the same
// discipline the settle-reimbursement route already follows for its own
// claim fields.
type reverseDuesPaymentRequest struct {
	OccurredOn string  `json:"occurred_on"`
	Note       *string `json:"note"`
}

// reverseDuesPayment is POST /api/dues-payments/{id}/reversal: wraps
// Ledger.ReverseDuesPayment. {id} names the kind='dues' transaction being
// reversed, not a dues-payment resource of its own - there is no separate
// dues-payment entity, only transaction rows (PRD section 4's "stay exactly as
// wide as dues": this route reverses a dues payment and nothing else, never
// a generic "reverse any transaction" primitive).
//
// The response is the posted reversal row, as the existing
// transactionResponse - the same wire shape POST /api/dues-payments and GET
// /api/transactions already use, so a client already knows how to read it.
func (a *api) reverseDuesPayment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The transaction id is not a valid number.")
		return
	}

	var req reverseDuesPaymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	reversal, err := a.ledger.ReverseDuesPayment(r.Context(), ledger.ReverseDuesPaymentParams{
		FundID:        fund.ID,
		TransactionID: id,
		OccurredOn:    req.OccurredOn,
		Note:          req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransactionResponse(reversal))
}

// duesPaymentHistoryResponse is one row of GET /api/dues-payments (#228,
// ADR-032 "Lists: paging and search"): a dues payment, or the
// kind='adjustment' row that reverses one (ADR-029) - the same list, since
// PRD section 7.3 asks a reversal to read as an entry beside the payment it
// undoes, never as a payment edited away.
//
// is_reversal replaces a bare kind string on this wire: the client only
// ever needs to tell these two shapes apart, never anything a third kind
// value would add, and a bool is one branch instead of a string compare.
//
// reverses_transaction_id is set only when this row is itself a reversal;
// reversed_by_transaction_id only when this row is a payment something else
// reverses (never both - a reversal is never itself reversed). reverses_
// occurred_on rides only on a reversal row, the original payment's own
// date, so the link reads correctly even when that original sits on a
// later page than the reversal that names it.
type duesPaymentHistoryResponse struct {
	ID                      int64   `json:"id"`
	IsReversal              bool    `json:"is_reversal"`
	MemberID                int64   `json:"member_id"`
	MemberName              string  `json:"member_name"`
	DuesPeriod              string  `json:"dues_period"`
	Amount                  int64   `json:"amount"`
	OccurredOn              string  `json:"occurred_on"`
	AccountName             string  `json:"account_name"`
	Note                    *string `json:"note"`
	ReversesTransactionID   *int64  `json:"reverses_transaction_id"`
	ReversedByTransactionID *int64  `json:"reversed_by_transaction_id"`
	ReversesOccurredOn      *string `json:"reverses_occurred_on"`
}

// toDuesPaymentHistoryResponse maps one ListDuesPaymentsPage row to the
// wire. member_id and dues_period arrive as pointers only because sqlc
// reads them off "transaction"'s own nullable columns - the query's WHERE
// clause (kind='dues' OR a dues reversal) is exactly the schema's own CHECK
// for "these two are never null", so the dereference here is never a
// zero value in practice, and this is the one place that fact gets turned
// into the wire's plain (non-pointer) member_id/dues_period.
func toDuesPaymentHistoryResponse(row store.ListDuesPaymentsPageRow) duesPaymentHistoryResponse {
	var memberID int64
	if row.MemberID != nil {
		memberID = *row.MemberID
	}
	var duesPeriod string
	if row.DuesPeriod != nil {
		duesPeriod = *row.DuesPeriod
	}
	return duesPaymentHistoryResponse{
		ID:                      row.ID,
		IsReversal:              row.Kind == "adjustment",
		MemberID:                memberID,
		MemberName:              row.MemberName,
		DuesPeriod:              duesPeriod,
		Amount:                  row.Amount,
		OccurredOn:              row.OccurredOn,
		AccountName:             row.AccountName,
		Note:                    row.Note,
		ReversesTransactionID:   row.ReversesTransactionID,
		ReversedByTransactionID: row.ReversedByTransactionID,
		ReversesOccurredOn:      row.ReversesOccurredOn,
	}
}

// duesPaymentsPageSize is GET /api/dues-payments's fixed page size (#228,
// ADR-032 "Lists: paging and search"): 25 a page, the same as every other
// paged list in this package.
const duesPaymentsPageSize = 25

// duesPaymentsPageResponse is GET /api/dues-payments's envelope - the same
// {rows, next_cursor} shape transactionsPageResponse and
// reimbursementsPageResponse already use.
type duesPaymentsPageResponse struct {
	DuesPayments []duesPaymentHistoryResponse `json:"dues_payments"`
	NextCursor   *string                      `json:"next_cursor"`
}

// encodeDuesPaymentsCursor/decodeDuesPaymentsCursor are
// encodeReimbursementsCursor/decodeReimbursementsCursor's own shape (base64
// of "occurred_on|id") kept local rather than shared, for the same reason
// that comment gives: this route's cursor is its own opaque wire format
// over its own query.
func encodeDuesPaymentsCursor(occurredOn string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(occurredOn + "|" + strconv.FormatInt(id, 10)))
}

// decodeDuesPaymentsCursor rejects anything that doesn't round-trip to a
// real calendar date plus a positive id - the shape listDuesPayments below
// answers 400 invalid_argument for.
func decodeDuesPaymentsCursor(raw string) (occurredOn string, id int64, ok bool) {
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

// listDuesPayments is GET /api/dues-payments (#228, ADR-032 "Lists: paging
// and search"): dues payment history, which PRD section 7.3 implies and
// which GET /api/dues-status never answered - that route reads one period
// at a time, this route answers "when did that payment actually come in?"
// across every period, newest-first, 25 a page.
//
// ?q= searches member name only (the response's own decided shape carries
// no purpose or note column worth searching for this list, the same
// narrower surface GET /api/reimbursements already keeps).
//
// A direct-CRUD read (ADR-027): no derived invariant beyond the query
// itself, so this calls a.queries directly, the same split every other
// list route in this package uses.
func (a *api) listDuesPayments(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	params := store.ListDuesPaymentsPageParams{
		FundID:    fund.ID,
		PageLimit: duesPaymentsPageSize + 1,
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		occurredOn, id, ok := decodeDuesPaymentsCursor(cursor)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The cursor is not valid.")
			return
		}
		params.CursorOccurredOn = occurredOn
		params.CursorID = &id
	}

	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params.Q = q
	}

	rows, err := a.queries.ListDuesPaymentsPage(r.Context(), params)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	var nextCursor *string
	if len(rows) > duesPaymentsPageSize {
		rows = rows[:duesPaymentsPageSize]
		last := rows[len(rows)-1]
		encoded := encodeDuesPaymentsCursor(last.OccurredOn, last.ID)
		nextCursor = &encoded
	}

	resp := make([]duesPaymentHistoryResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, toDuesPaymentHistoryResponse(row))
	}
	writeJSON(w, http.StatusOK, duesPaymentsPageResponse{DuesPayments: resp, NextCursor: nextCursor})
}
