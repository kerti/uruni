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

// duesPaymentPeriod is one period within a POST /api/dues-payments request:
// the period paid and the amount toward it. The kind='dues' CHECK requires
// exactly one dues_period per row (ADR-024), and paying several months at
// once is the treasurer's real workflow (PRD section 7.3), so periods is an
// array on the wire, never flattened into a total.
type duesPaymentPeriod struct {
	DuesPeriod string `json:"dues_period"`
	Amount     int64  `json:"amount"`
}

// duesPaymentRequest is POST /api/dues-payments's body: one member paying one
// or more periods in one sitting, on one account, dated and noted alike;
// Periods is the only part that repeats. No purpose_id (#473): dues always
// land on Kas Utama, which the ledger looks up.
type duesPaymentRequest struct {
	AccountID  int64               `json:"account_id"`
	MemberID   int64               `json:"member_id"`
	OccurredOn string              `json:"occurred_on"`
	Note       *string             `json:"note"`
	Periods    []duesPaymentPeriod `json:"periods"`
}

// createDuesPayment is POST /api/dues-payments: one call to
// Ledger.PostDuesPayments, which posts one row per entry in Periods in a
// single database transaction; the response echoes one array entry per posted
// row, in order given.
//
// Periods must not be empty: a request-shape check this handler owns, like
// resolveMember owning "the id in the path is a valid number". PostDuesPayments
// also rejects an empty array as ErrInvalidArgument; checking first keeps the
// 400 message specific to the body, before a fund lookup.
//
// A failure on any period, even partway through a batch, rolls back every
// row: the ledger validates every period before writing and writes all in
// one transaction. (Looping a singular PostDuesPayment per period left an
// earlier row standing when a later one failed, #96.)
//
// Keyed (ADR-043): a replay answers the whole array again, one result.
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

	idem := idempotencyFrom(r)
	posted, err := a.ledger.PostDuesPayments(r.Context(), ledger.PostDuesPaymentsParams{
		Now:        a.now(),
		FundID:     fund.ID,
		AccountID:  req.AccountID,
		MemberID:   req.MemberID,
		OccurredOn: req.OccurredOn,
		Note:       req.Note,
		Periods:    periods,

		Idempotency: idem,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	resp := make([]transactionResponse, 0, len(posted))
	for _, row := range posted {
		resp = append(resp, toTransactionResponse(row))
	}

	markReplay(w, r, a.log(r), idem)
	writeJSON(w, http.StatusCreated, resp)
}

// duesPaymentHistoryResponse is one row of GET /api/dues-payments (#228,
// ADR-032): a dues payment, or the kind='adjustment' row reversing one
// (ADR-029), in one list, since PRD section 7.3 asks a reversal to read as an
// entry beside the payment it undoes, never as a payment edited away.
//
// is_reversal replaces a bare kind string: the client only tells these two
// shapes apart, and a bool is one branch.
//
// reverses_transaction_id is set only on a reversal;
// reversed_by_transaction_id only on a payment something reverses (never both:
// a reversal is never itself reversed). reverses_occurred_on rides only on a
// reversal row, the original payment's date, so the link reads right even
// when the original sits on a later page.
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

// toDuesPaymentHistoryResponse maps one ListDuesPaymentsPage row to the wire.
// member_id and dues_period are pointers only because sqlc reads them off
// "transaction"'s nullable columns; the query's WHERE (kind='dues' OR a dues
// reversal) is the schema's CHECK for "these are never null", so the
// dereference here is never a zero value in practice.
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
// ADR-032): 25 a page, like every paged list here.
const duesPaymentsPageSize = 25

// duesPaymentsPageResponse is GET /api/dues-payments's envelope, the
// {rows, next_cursor} shape of the other paged lists.
type duesPaymentsPageResponse struct {
	DuesPayments []duesPaymentHistoryResponse `json:"dues_payments"`
	NextCursor   *string                      `json:"next_cursor"`
}

// encodeDuesPaymentsCursor/decodeDuesPaymentsCursor have the shape of the
// reimbursements cursor (base64 of "occurred_on|id"), kept local for the same
// reason: an opaque wire format over this route's own query.
func encodeDuesPaymentsCursor(occurredOn string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(occurredOn + "|" + strconv.FormatInt(id, 10)))
}

// decodeDuesPaymentsCursor rejects anything that does not round-trip to a real
// calendar date plus a positive id; listDuesPayments answers 400
// invalid_argument.
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

// listDuesPayments is GET /api/dues-payments (#228, ADR-032): dues payment
// history (PRD section 7.3), which GET /api/dues-status never answered: that
// reads one period at a time, this answers "when did that payment come in?"
// across every period, newest-first, 25 a page.
//
// ?q= searches member name only: the response carries no purpose or note
// column worth searching, the narrower surface GET /api/reimbursements keeps.
//
// A direct-CRUD read (ADR-027): it calls a.queries directly, like every list
// route here.
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
		mapSQLiteError(w, a.log(r), err)
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
