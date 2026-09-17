package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
)

// purposeCorrectionRequest is POST /api/transactions/{id}/purpose-correction's
// body: one field, purpose_id (ADR-033, #267). There is deliberately no
// amount, account_id or occurred_on - Ledger.PostPurposeCorrection copies
// account_id and occurred_on from the row {id} names and never accepts an
// amount at all, the same discipline reverseDuesPaymentRequest already holds
// for the payment it reverses. One field is what makes immutability
// structural here rather than promised: there is no way for this route to
// edit the original row even by accident, because nothing it could carry
// would let it try.
type purposeCorrectionRequest struct {
	PurposeID int64 `json:"purpose_id"`
}

// postPurposeCorrection is POST /api/transactions/{id}/purpose-correction:
// wraps Ledger.PostPurposeCorrection. {id} names the transaction row whose
// peruntukan is wrong, matching the nested-verb idiom
// /api/incidentals/{purposeID}/close and /api/dues-payments/{id}/reversal
// already use for "the route names the row, the verb names the action."
//
// Nothing is validated here (ADR-027): every refusal - the four ineligible
// kinds, a dues reversal, both closed-incidental directions, the no-op, a
// transaction id belonging to another fund - is PostPurposeCorrection's own
// named error, reaching the client through mapLedgerError with the ledger's
// own message.
//
// The response is the posted transfer row, the same transferResponse shape
// POST /api/transfers already answers with - a correction's two legs are
// ordinary transaction rows, already readable through GET
// /api/transactions, exactly as transferResponse's own comment says for
// PostTransferBetweenAccounts.
func (a *api) postPurposeCorrection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The transaction id is not a valid number.")
		return
	}

	var req purposeCorrectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	correction, err := a.ledger.PostPurposeCorrection(r.Context(), ledger.PostPurposeCorrectionParams{
		FundID:        fund.ID,
		TransactionID: id,
		PurposeID:     req.PurposeID,
	})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, toTransferResponse(correction))
}
