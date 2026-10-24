package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// fundResponse is the wire shape of a fund row. sqlc's store.Fund has no json
// tags (sqlc.yaml emit_json_tags: false: nullability needs a type change more
// than the wire needs a shortcut), so this package owns the wire shape, as
// errorEnvelope and health do.
type fundResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
	// TimeZone is the fund's IANA zone name (ADR-041).
	TimeZone   string `json:"time_zone"`
	ReportSlug string `json:"report_slug"`
	// ReportURL is the shareable link: URUNI_BASE_URL + /report/ + slug. Only the
	// server knows the base URL, so the SPA gets the finished link, not one built
	// from window.location (the dev server's or a proxy's origin). With no base
	// URL configured (local dev) it is the bare path, resolved against the SPA's
	// origin.
	ReportURL string `json:"report_url"`
	CreatedAt int64  `json:"created_at"`
}

func toFundResponse(f store.Fund, baseURL string) fundResponse {
	return fundResponse{
		ReportURL:  baseURL + "/report/" + f.ReportSlug,
		ID:         f.ID,
		Name:       f.Name,
		Currency:   f.Currency,
		TimeZone:   f.TimeZone,
		ReportSlug: f.ReportSlug,
		CreatedAt:  f.CreatedAt,
	}
}

// setupAccountRequest is one entry of setupRequest.Accounts: the kind and name
// for one location at setup (#78). Shape only: kind validity and a non-blank
// name are the schema's CHECKs to refuse (ADR-027).
//
// OpeningBalance is optional, as in accountRequest (#230: a location and its
// opening balance are born together, or not at all). Absent equals 0; neither
// posts a row.
type setupAccountRequest struct {
	Kind           string                 `json:"kind"`
	Name           string                 `json:"name"`
	OpeningBalance *openingBalanceRequest `json:"opening_balance"`
}

// setupRequest is POST /api/setup's body: the fund's name and time zone (PRD
// section 7.1) and every account it starts with (#78), at least one (SetUpFund
// refuses an empty list). TimeZone is optional: absent means Asia/Jakarta
// (ADR-041). The main purpose is not on the wire: "Kas Utama" is a fixed domain
// name (CONTEXT.md, ADR-014), never user input.
type setupRequest struct {
	Name     string                `json:"name"`
	TimeZone string                `json:"time_zone"`
	Accounts []setupAccountRequest `json:"accounts"`
}

// setupResponse is pinned by #64 for fund and main_purpose_id; accounts became
// a list under #78, as SetUpFund no longer creates a fixed pair.
type setupResponse struct {
	Fund          fundResponse      `json:"fund"`
	MainPurposeID int64             `json:"main_purpose_id"`
	Accounts      []accountResponse `json:"accounts"`
}

// setupFund is POST /api/setup. No fund_id: v1 is one fund, and SetUpFund
// refuses a second. The handler passes the name and accounts through without
// pre-checks the ledger makes (ADR-027).
func (a *api) setupFund(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	accounts := make([]ledger.AccountInput, 0, len(req.Accounts))
	for _, acc := range req.Accounts {
		accounts = append(accounts, ledger.AccountInput{
			Kind:           acc.Kind,
			Name:           acc.Name,
			OpeningBalance: acc.OpeningBalance.toOpeningBalance(),
		})
	}

	result, err := a.ledger.SetUpFund(r.Context(), ledger.SetUpFundParams{
		FundName: req.Name,
		TimeZone: req.TimeZone,
		Accounts: accounts,
	})
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}

	accountsResp := make([]accountResponse, 0, len(result.Accounts))
	for _, acc := range result.Accounts {
		accountsResp = append(accountsResp, toAccountResponse(acc))
	}

	writeJSON(w, http.StatusCreated, setupResponse{
		Fund:          toFundResponse(result.Fund, a.baseURL),
		MainPurposeID: result.MainPurposeID,
		Accounts:      accountsResp,
	})
}

// getFund is GET /api/fund: a direct-CRUD read (no derived invariant) calling
// a.queries, per api's split. No fund_id in the route: v1 is one fund, which
// resolveFund resolves server-side: the first (and only) ListFunds row, or 404
// meaning "run setup."
func (a *api) getFund(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toFundResponse(fund, a.baseURL))
}

// updateFundRequest is PATCH /api/fund's body: the display name and the time
// zone, each optional, at least one required. No currency (an invariant
// through 0.x) and no report_slug, the public report's unguessable address,
// whose change is its own decision (POST /api/fund/report-slug), not a side
// effect of fixing a typo.
//
// Both are pointers so a body with neither ({}, a misspelt key) is a 400, not a
// silent rename to the empty string, as updateDuesRateRequest.Amount. The
// schema's CHECKs still refuse a blank name and a zone outside the three
// (ADR-027, ADR-041).
type updateFundRequest struct {
	Name     *string `json:"name"`
	TimeZone *string `json:"time_zone"`
}

// updateFund is PATCH /api/fund: renames the kas and/or changes its time zone.
// The name is a display label (it heads every screen and the public report) and
// nothing posted references it, so no history is rewritten, like renaming a
// location (updateAccount). The setup wizard's copy promises it: "bisa diganti
// nanti kalau perlu." A zone change rewrites nothing either (ADR-041): every
// day and month is derived from it, so setting the old zone back restores every
// earlier reading.
func (a *api) updateFund(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}

	var req updateFundRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.TimeZone == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "A name or a time zone is required.")
		return
	}

	updated, err := a.queries.UpdateFund(r.Context(), store.UpdateFundParams{ID: fund.ID, Name: req.Name, TimeZone: req.TimeZone})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusOK, toFundResponse(updated, a.baseURL))
}

// replaceReportSlug is POST /api/fund/report-slug: the leak escape hatch
// (ADR-035). It answers with the whole fund, so the SPA swaps its link for the
// new report_url at once. The old slug stops resolving immediately.
func (a *api) replaceReportSlug(w http.ResponseWriter, r *http.Request) {
	fund, ok := a.resolveFund(w, r)
	if !ok {
		return
	}
	updated, err := a.ledger.ReplaceReportSlug(r.Context(), fund.ID)
	if err != nil {
		mapLedgerError(w, a.log(r), err)
		return
	}
	a.securityEvent(r, slog.LevelInfo, "report_link_rotated")
	writeJSON(w, http.StatusOK, toFundResponse(updated, a.baseURL))
}

// fundLocation is the fund's time zone as a *time.Location, the only way a
// handler comes by one (ADR-041): never from a request, never a fallback. A zone
// that fails to load means a broken database, so the request answers 500.
func (a *api) fundLocation(w http.ResponseWriter, r *http.Request, fund store.Fund) (*time.Location, bool) {
	loc, err := ledger.FundLocation(fund)
	if err != nil {
		a.log(r).Error("fund has an unusable time zone", "fund_id", fund.ID, "time_zone", fund.TimeZone, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return nil, false
	}
	return loc, true
}

// resolveFund returns the single fund every fund-scoped route operates
// against, or answers the request (404, "run setup") and reports false. v1 is
// one fund and no route takes a fund_id, so "the fund" is the first (and only)
// ListFunds row. getFund did this alone until #65 added siblings needing the
// same check; it moved here to stay the one place it is made.
func (a *api) resolveFund(w http.ResponseWriter, r *http.Request) (store.Fund, bool) {
	funds, err := a.queries.ListFunds(r.Context())
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return store.Fund{}, false
	}
	if len(funds) == 0 {
		writeAPIError(w, http.StatusNotFound, "not_found", "No fund has been set up yet.")
		return store.Fund{}, false
	}
	return funds[0], true
}
