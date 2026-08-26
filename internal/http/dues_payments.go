package http

import (
	"net/http"

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

// createDuesPayment is POST /api/dues-payments: wraps Ledger.PostDuesPayment
// once per entry in Periods, in the order given, posting one row per period
// exactly as PostDuesPayment's own doc comment describes - never flattened
// into a single multi-period row, and the response echoes that back as one
// array entry per posted row.
//
// Periods must not be empty - not a ledger rule (PostDuesPayment has no
// concept of a batch; it posts exactly one row per call), but a request-shape
// check this handler owns, the same way resolveMember owns "the id in the
// path is a valid number": an empty array would silently post nothing and
// answer 201, which is worse than naming the problem.
//
// Each period is independently validated and posted by PostDuesPayment; a
// failure partway through Periods leaves every already-posted period's row
// standing - transactions are immutable (CLAUDE.md rule 3), so nothing here
// rolls one back - and reports the failing period's error. A caller that
// needs to know which periods made it through before the failure can read
// GET /api/transactions.
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

	resp := make([]transactionResponse, 0, len(req.Periods))
	for _, period := range req.Periods {
		posted, err := a.ledger.PostDuesPayment(r.Context(), ledger.PostDuesPaymentParams{
			FundID:     fund.ID,
			AccountID:  req.AccountID,
			PurposeID:  req.PurposeID,
			MemberID:   req.MemberID,
			DuesPeriod: period.DuesPeriod,
			Amount:     money.Amount(period.Amount),
			OccurredOn: req.OccurredOn,
			Note:       req.Note,
		})
		if err != nil {
			mapLedgerError(w, a.logger, err)
			return
		}
		resp = append(resp, toTransactionResponse(posted))
	}

	writeJSON(w, http.StatusCreated, resp)
}
