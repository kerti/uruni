package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/store"
)

// accountResponse is the wire shape of an account row. No fund_id, same
// reasoning as memberResponse. kind is exposed because it is the whole point
// of the row - cash and bank reconcile differently (PRD §7.7).
type accountResponse struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func toAccountResponse(a store.Account) accountResponse {
	return accountResponse{ID: a.ID, Kind: a.Kind, Name: a.Name, CreatedAt: a.CreatedAt}
}

// listAccounts is GET /api/accounts: the two locations setup created. There
// is deliberately no POST - PRD §6 names exactly cash and bank, SetUpFund
// creates both, and a route no UI would call is what the prime directive
// says to leave out.
func (a *api) listAccounts(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	accounts, err := a.queries.ListAccountsByFund(r.Context(), fund.ID)
	if err != nil {
		mapSQLiteError(w, a.logger, err)
		return
	}

	resp := make([]accountResponse, 0, len(accounts))
	for _, acc := range accounts {
		resp = append(resp, toAccountResponse(acc))
	}
	writeJSON(w, http.StatusOK, resp)
}
