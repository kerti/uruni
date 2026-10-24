package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// purposeMoveRequest is POST /api/purpose-moves's body (ADR-036, #383): an
// amount leaving one purpose and arriving in another, both legs on one account.
// The account is asked for, not chosen silently, because the row names it and
// the choice is the treasurer's (the close-envelope form's precedent); its
// balance does not move.
//
// The optional note is written to both legs, or neither, as in POST
// /api/transfers.
type purposeMoveRequest struct {
	FromPurposeID int64   `json:"from_purpose_id"`
	ToPurposeID   int64   `json:"to_purpose_id"`
	AccountID     int64   `json:"account_id"`
	Amount        int64   `json:"amount"`
	OccurredOn    string  `json:"occurred_on"`
	Note          *string `json:"note"`
}

// createPurposeMove is POST /api/purpose-moves: wraps Ledger.PostPurposeMove,
// the treasurer giving from Kas Utama to an envelope, taking it back, or moving
// between two envelopes. Nothing leaves the fund, so no balance or
// reconciliation figure moves; only what the money is for.
//
// Nothing is validated here (ADR-027): every refusal is PostPurposeMove's
// named error, through mapLedgerError. It answers with POST /api/transfers'
// transferResponse; the two legs are ordinary rows readable through GET
// /api/transactions.
//
// Keyed (ADR-043).
func (a *api) createPurposeMove(w http.ResponseWriter, r *http.Request) {
	var req purposeMoveRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	idem := idempotencyFrom(r)
	transfer, err := a.ledger.PostPurposeMove(r.Context(), ledger.PostPurposeMoveParams{
		FundID:        fund.ID,
		FromPurposeID: req.FromPurposeID,
		ToPurposeID:   req.ToPurposeID,
		AccountID:     req.AccountID,
		Amount:        money.Amount(req.Amount),
		OccurredOn:    req.OccurredOn,
		Note:          req.Note,
		Idempotency:   idem,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	markReplay(w, r, a.log(r), idem)
	writeJSON(w, http.StatusCreated, toTransferResponse(transfer))
}
