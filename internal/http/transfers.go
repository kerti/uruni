package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// transferRequest is POST /api/transfers's body: one amount leaving one
// account and arriving in another, within the same purpose. purpose_id is
// singular deliberately - a between_accounts transfer never changes what the
// money is for, only where it sits (ADR-024), and the reclass_purpose shape
// that does change it belongs to CloseIncidentalAndRoll, not to a route.
//
// There is no note: the ledger's PostTransferBetweenAccountsParams carries
// none, because a transfer's two legs already say everything a treasurer
// would write - the amount, the date, and which way it went.
type transferRequest struct {
	PurposeID     int64  `json:"purpose_id"`
	FromAccountID int64  `json:"from_account_id"`
	ToAccountID   int64  `json:"to_account_id"`
	Amount        int64  `json:"amount"`
	OccurredOn    string `json:"occurred_on"`
}

// transferResponse is the wire shape of the transfer row. No fund_id, same
// reasoning as accountResponse. The two legs it groups are not repeated here:
// they are ordinary transaction rows, already readable through GET
// /api/transactions, which is why this slice deliberately has no GET
// /api/transfers of its own.
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
// cash at the bank or drawing it back out (PRD §6's location tracking). The
// fund's total is unchanged by construction - one amount, two opposite legs -
// so the only thing that moves is where the money sits.
//
// Nothing is validated here (ADR-027): a non-positive amount, a malformed
// date and identical from/to legs are all the ledger's own
// ErrInvalidArgument, reaching the client as a 400 through the shared mapper
// with the ledger's message rather than a second one written here.
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
		FundID:        fund.ID,
		PurposeID:     req.PurposeID,
		FromAccountID: req.FromAccountID,
		ToAccountID:   req.ToAccountID,
		Amount:        money.Amount(req.Amount),
		OccurredOn:    req.OccurredOn,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toTransferResponse(transfer))
}
