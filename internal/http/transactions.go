package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// transactionRequest is POST /api/transactions's body: one ordinary entry or
// one correction (PRD §7.2, §7.6). A pass-through movement is not a special
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

// listTransactions is GET /api/transactions: the recent-transactions list
// PRD §7.8's reconcile flow reads. A direct-CRUD read (ADR-027) - no derived
// invariant beyond the sum itself - so it calls a.queries directly, the same
// split listAccounts and listPurposes already use. Ordered oldest-first,
// ListTransactionsByFund's own ORDER BY occurred_on, id.
func (a *api) listTransactions(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	transactions, err := a.queries.ListTransactionsByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]transactionResponse, 0, len(transactions))
	for _, t := range transactions {
		resp = append(resp, toTransactionResponse(t))
	}
	writeJSON(w, http.StatusOK, resp)
}
