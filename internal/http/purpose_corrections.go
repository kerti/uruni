package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
)

// purposeCorrectionRequest is POST /api/transactions/{id}/purpose-correction's
// body: one field, purpose_id (ADR-033, #267). There is no amount, account_id
// or occurred_on: Ledger.PostPurposeCorrection copies account_id and
// occurred_on from the row {id} names and accepts no amount, as
// reverseTransactionRequest does. One field makes immutability structural:
// nothing this route carries could let it edit the original row, even by
// accident.
type purposeCorrectionRequest struct {
	PurposeID int64 `json:"purpose_id"`
}

// postPurposeCorrection is POST /api/transactions/{id}/purpose-correction:
// wraps Ledger.PostPurposeCorrection. {id} names the transaction row whose
// peruntukan is wrong, the nested-verb idiom of
// /api/incidentals/{purposeID}/close and /api/transactions/{id}/reversal
// ("the route names the row, the verb the action").
//
// Nothing is validated here (ADR-027): every refusal (the four ineligible
// kinds, a reversal, both closed-incidental directions, the no-op,
// another fund's transaction id) is PostPurposeCorrection's named error,
// reaching the client through mapLedgerError with the ledger's message.
//
// The response is the posted transfer row, the transferResponse of POST
// /api/transfers: a correction's two legs are ordinary transaction rows
// readable through GET /api/transactions.
//
// Keyed (ADR-043): 200, not 201, on a first success and on a replay alike. Under
// a key, a repeat of the correction that worked is that 200, not the 409
// purpose_correction_noop it would otherwise earn.
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

	idem := idempotencyFrom(r)
	correction, err := a.ledger.PostPurposeCorrection(r.Context(), ledger.PostPurposeCorrectionParams{
		FundID:        fund.ID,
		TransactionID: id,
		PurposeID:     req.PurposeID,
		Idempotency:   idem,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	markReplay(w, r, a.log(r), idem)
	writeJSON(w, http.StatusOK, toTransferResponse(correction))
}
