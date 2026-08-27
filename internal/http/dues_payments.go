package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// duesPaymentPeriod is one period within a POST /api/dues-payments request:
// the period paid and the amount paid toward it. The schema's kind='dues'
// CHECK requires exactly one dues_period per row (ADR-024), and paying
// several months in one sitting is the treasurer's real workflow (PRD §7.3) -
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
// dues-payment entity, only transaction rows (PRD §4's "stay exactly as
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
