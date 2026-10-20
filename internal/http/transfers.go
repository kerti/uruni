package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// transferRequest is POST /api/transfers's body: one amount leaving one
// account and arriving in another, within one purpose. purpose_id is singular:
// a between_accounts transfer changes where money sits, not what it is for
// (ADR-024); the reclass_purpose shape that changes it belongs to
// CloseIncidentalAndRoll, not a route.
//
// The optional note is written to both legs, or neither. This reverses an
// earlier reading of ADR-024 (that amount, date and direction say everything),
// which held only for cash-to-bank. "Why did this move?" is what a transfer
// leaves open, and a note on one leg alone makes the other read as an
// unexplained arrival.
type transferRequest struct {
	PurposeID     int64   `json:"purpose_id"`
	FromAccountID int64   `json:"from_account_id"`
	ToAccountID   int64   `json:"to_account_id"`
	Amount        int64   `json:"amount"`
	OccurredOn    string  `json:"occurred_on"`
	Note          *string `json:"note"`
}

// transferResponse is the wire shape of the transfer row. No fund_id, as
// accountResponse. Its two legs are ordinary transaction rows readable through
// GET /api/transactions, so there is no GET /api/transfers.
type transferResponse struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	CreatedAt int64  `json:"created_at"`
}

func toTransferResponse(t store.Transfer) transferResponse {
	return transferResponse{ID: t.ID, Kind: t.Kind, CreatedAt: t.CreatedAt}
}

// createTransfer is POST /api/transfers: wraps
// Ledger.PostTransferBetweenAccounts, the treasurer depositing the wallet's
// cash at the bank or drawing it back out (PRD section 6). The fund's total is
// unchanged by construction (one amount, two opposite legs); only where the
// money sits moves.
//
// Nothing is validated here (ADR-027): a non-positive amount, malformed date
// and identical from/to legs are the ledger's ErrInvalidArgument, a 400
// through the shared mapper with the ledger's message.
func (a *api) createTransfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	transfer, err := a.ledger.PostTransferBetweenAccounts(r.Context(), ledger.PostTransferBetweenAccountsParams{
		Now:           a.now(),
		FundID:        fund.ID,
		PurposeID:     req.PurposeID,
		FromAccountID: req.FromAccountID,
		ToAccountID:   req.ToAccountID,
		Amount:        money.Amount(req.Amount),
		OccurredOn:    req.OccurredOn,
		Note:          req.Note,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransferResponse(transfer))
}
