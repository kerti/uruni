package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// fundResponse is the wire shape of a fund row. sqlc's generated store.Fund
// carries no json tags (sqlc.yaml sets emit_json_tags: false, deliberately -
// nullability needs a type change more than the wire needs a shortcut), so
// this package owns the wire shape explicitly, the same way errorEnvelope and
// health already do.
type fundResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	ReportSlug string `json:"report_slug"`
	CreatedAt  int64  `json:"created_at"`
}

func toFundResponse(f store.Fund) fundResponse {
	return fundResponse{
		ID:         f.ID,
		Name:       f.Name,
		Currency:   f.Currency,
		ReportSlug: f.ReportSlug,
		CreatedAt:  f.CreatedAt,
	}
}

// setupRequest is POST /api/setup's body: the one thing first-run setup asks
// the treasurer for directly (PRD §7.1, "name the fund"). Everything else
// SetUpFund creates - the main purpose, the cash and bank accounts - is
// fixed, not user input; see setup.go's own comment in internal/ledger.
type setupRequest struct {
	Name string `json:"name"`
}

// setupResponse is pinned by #64, not a builder's choice: fund is the whole
// row because it carries report_slug, which M7's public report needs, and
// the three ids are what slices #65-#67 post against the moment setup
// returns.
type setupResponse struct {
	Fund          fundResponse `json:"fund"`
	MainPurposeID int64        `json:"main_purpose_id"`
	CashAccountID int64        `json:"cash_account_id"`
	BankAccountID int64        `json:"bank_account_id"`
}

// setupFund is POST /api/setup. No fund_id anywhere: v1 is exactly one fund,
// and SetUpFund itself is what refuses a second one - this handler decodes
// and passes the name through, it does not pre-check anything the ledger
// already checks (ADR-027).
func (a *api) setupFund(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := a.ledger.SetUpFund(r.Context(), ledger.SetUpFundParams{FundName: req.Name})
	if err != nil {
		mapLedgerError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, setupResponse{
		Fund:          toFundResponse(result.Fund),
		MainPurposeID: result.MainPurposeID,
		CashAccountID: result.CashAccountID,
		BankAccountID: result.BankAccountID,
	})
}

// getFund is GET /api/fund: a direct-CRUD read (no derived invariant), so it
// calls a.queries itself rather than going through the ledger, the same
// split api's own doc comment describes. No fund_id in the route - v1 is
// exactly one fund, so the handler resolves it server-side: the first (and
// only) row ListFunds returns, or 404 meaning "run setup."
func (a *api) getFund(w http.ResponseWriter, r *http.Request) {
	funds, err := a.queries.ListFunds(r.Context())
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}
	if len(funds) == 0 {
		writeAPIError(w, http.StatusNotFound, "not_found", "No fund has been set up yet.")
		return
	}

	writeJSON(w, http.StatusOK, toFundResponse(funds[0]))
}
