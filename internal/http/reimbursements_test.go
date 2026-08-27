package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func postReimbursement(t *testing.T, r http.Handler, req reimbursementRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling reimbursement request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements", bytes.NewReader(body)))
	return rec
}

func getReimbursements(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reimbursements"+query, nil))
	return rec
}

func postSettlement(t *testing.T, r http.Handler, id int64, req settleReimbursementRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling settle request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/reimbursements/%d/settle", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

// memberFor creates one member and returns its id, for the tests below that
// need a claimant and nothing else about them.
func memberFor(t *testing.T, r http.Handler, name string) int64 {
	t.Helper()
	rec := postMember(t, r, memberRequest{Name: name})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	return member.ID
}

func TestPostReimbursementsRequiresAFund(t *testing.T) {
	rec := postReimbursement(t, testRouter(t), reimbursementRequest{
		MemberID: 1, PurposeID: 1, Amount: 80_000, IncurredOn: "2026-08-12",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/reimbursements before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestReimbursementRoundTripsFromClaimToSettlement is the slice's central
// acceptance criterion: the claim itself moves no money - the fund balance
// is still zero while it is outstanding - and only settling posts the
// single 'out' row that pays it.
func TestReimbursementRoundTripsFromClaimToSettlement(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFundForTransactions(t, r)
	memberID := memberFor(t, r, "Jane")

	note := "bought lightbulbs for the pos ronda"
	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10", Note: &note,
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(createRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	if claim.ID == 0 {
		t.Error("reimbursement id = 0, want a real id")
	}
	if claim.Amount != 80_000 || claim.IncurredOn != "2026-08-10" || claim.MemberID != memberID {
		t.Errorf("claim = %+v, want amount 80000 incurred 2026-08-10 for member %d", claim, memberID)
	}

	balanceBefore, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if balanceBefore.Int64() != 0 {
		t.Fatalf("FundBalance() after the claim = %d, want 0 - recording a claim moves no money", balanceBefore.Int64())
	}

	settleRec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "2026-08-20",
	})
	if settleRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements/{id}/settle = %d, want %d (body: %s)", settleRec.Code, http.StatusCreated, settleRec.Body.String())
	}
	var posted transactionResponse
	if err := json.NewDecoder(settleRec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding settlement response: %v", err)
	}
	if posted.Kind != "reimbursement" {
		t.Errorf("settlement kind = %q, want %q", posted.Kind, "reimbursement")
	}
	if posted.Direction != "out" {
		t.Errorf("settlement direction = %q, want %q", posted.Direction, "out")
	}
	if posted.Amount != 80_000 {
		t.Errorf("settlement amount = %d, want the claim's 80000", posted.Amount)
	}
	if posted.PurposeID != setup.MainPurposeID {
		t.Errorf("settlement purpose = %d, want the claim's %d", posted.PurposeID, setup.MainPurposeID)
	}
	// The settle date, not incurred_on - the claim keeps the truth about when
	// the member actually spent their own money.
	if posted.OccurredOn != "2026-08-20" {
		t.Errorf("settlement occurred_on = %q, want the settle date %q, not incurred_on", posted.OccurredOn, "2026-08-20")
	}
	if posted.ReimbursementID == nil || *posted.ReimbursementID != claim.ID {
		t.Errorf("settlement reimbursement_id = %v, want claim %d", posted.ReimbursementID, claim.ID)
	}

	balanceAfter, err := l.FundBalance(context.Background(), setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if balanceAfter.Int64() != -80_000 {
		t.Errorf("FundBalance() after settling = %d, want -80000 - the payout is the only posted row", balanceAfter.Int64())
	}
}

// TestPostSettlementTwiceReturnsItsNamed409 covers the settled-once rule at
// the route: the second call is a conflict with its own code, not a second
// payout and not a generic 500.
func TestPostSettlementTwiceReturnsItsNamed409(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)
	memberID := memberFor(t, r, "Jane")

	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10",
	})
	var claim reimbursementResponse
	if err := json.NewDecoder(createRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}

	first := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "2026-08-20",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first settle = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "2026-08-21",
	})
	if second.Code != http.StatusConflict {
		t.Fatalf("second settle = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "reimbursement_already_settled" {
		t.Errorf("error code = %q, want %q", got.Code, "reimbursement_already_settled")
	}
}

// TestPostSettlementOnAWaivedClaimReturnsItsNamed409 reaches
// ErrReimbursementWaived, which no route can produce on its own: nothing on
// the wire sets waived_on (#69 adds no waive route), so the claim is waived
// through store.Queries directly - the same way an import or a future waive
// route would - and the route is asked to settle it.
func TestPostSettlementOnAWaivedClaimReturnsItsNamed409(t *testing.T) {
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	r := New(testAssets(), testBuild, ledger.New(sqlDB), q, testLogger())

	setup := setUpFundForTransactions(t, r)
	memberID := memberFor(t, r, "Jane")

	waivedOn := "2026-08-15"
	claim, err := q.CreateReimbursement(context.Background(), store.CreateReimbursementParams{
		FundID: setup.Fund.ID, MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10", WaivedOn: &waivedOn,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}

	rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("settling a waived claim = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "reimbursement_waived" {
		t.Errorf("error code = %q, want %q", got.Code, "reimbursement_waived")
	}
}

// TestGetReimbursementsOutstandingFiltersToUnsettledClaims is the filter's
// acceptance criterion: the unfiltered list keeps every claim as history,
// ?outstanding=true keeps only what is still owed.
func TestGetReimbursementsOutstandingFiltersToUnsettledClaims(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)
	memberID := memberFor(t, r, "Jane")

	var claims []reimbursementResponse
	for _, amount := range []int64{80_000, 40_000} {
		rec := postReimbursement(t, r, reimbursementRequest{
			MemberID: memberID, PurposeID: setup.MainPurposeID,
			Amount: amount, IncurredOn: "2026-08-10",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
		var claim reimbursementResponse
		if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
			t.Fatalf("decoding reimbursement response: %v", err)
		}
		claims = append(claims, claim)
	}

	settleRec := postSettlement(t, r, claims[0].ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "2026-08-20",
	})
	if settleRec.Code != http.StatusCreated {
		t.Fatalf("settle = %d, want %d (body: %s)", settleRec.Code, http.StatusCreated, settleRec.Body.String())
	}

	all := decodeReimbursements(t, getReimbursements(t, r, ""))
	if len(all) != 2 {
		t.Errorf("GET /api/reimbursements returned %d claims, want 2 - a settled claim is still history", len(all))
	}

	outstanding := decodeReimbursements(t, getReimbursements(t, r, "?outstanding=true"))
	if len(outstanding) != 1 {
		t.Fatalf("GET /api/reimbursements?outstanding=true returned %d claims, want 1", len(outstanding))
	}
	if outstanding[0].ID != claims[1].ID {
		t.Errorf("outstanding claim = %d, want the unsettled %d", outstanding[0].ID, claims[1].ID)
	}

	if got := decodeReimbursements(t, getReimbursements(t, r, "?outstanding=false")); len(got) != 2 {
		t.Errorf("GET /api/reimbursements?outstanding=false returned %d claims, want 2 - explicitly asking not to filter", len(got))
	}
}

func TestGetReimbursementsRejectsAnUnparseableOutstandingFilter(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := getReimbursements(t, r, "?outstanding=yes")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/reimbursements?outstanding=yes = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestPostReimbursementsRejectsWhatTheSchemaRefuses proves this handler
// validates nothing itself: a non-positive amount, a calendar-invalid date
// and a member_id naming no row all come back through mapSQLiteError.
func TestPostReimbursementsRejectsWhatTheSchemaRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func(setup setupResponse, memberID int64) reimbursementRequest
		code string
	}{
		{"zero amount", func(s setupResponse, m int64) reimbursementRequest {
			return reimbursementRequest{MemberID: m, PurposeID: s.MainPurposeID, Amount: 0, IncurredOn: "2026-08-10"}
		}, "check_violation"},
		{"negative amount", func(s setupResponse, m int64) reimbursementRequest {
			return reimbursementRequest{MemberID: m, PurposeID: s.MainPurposeID, Amount: -80_000, IncurredOn: "2026-08-10"}
		}, "check_violation"},
		{"malformed incurred_on", func(s setupResponse, m int64) reimbursementRequest {
			return reimbursementRequest{MemberID: m, PurposeID: s.MainPurposeID, Amount: 80_000, IncurredOn: "2026-02-30"}
		}, "check_violation"},
		{"unknown member", func(s setupResponse, _ int64) reimbursementRequest {
			return reimbursementRequest{MemberID: 9_999, PurposeID: s.MainPurposeID, Amount: 80_000, IncurredOn: "2026-08-10"}
		}, "invalid_argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)
			setup := setUpFundForTransactions(t, r)
			memberID := memberFor(t, r, "Jane")

			rec := postReimbursement(t, r, tc.req(setup, memberID))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != tc.code {
				t.Errorf("error code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

// TestPostSettlementOnAnUnknownClaimIs404 is the case that reaches
// mapLedgerError's sql.ErrNoRows arm: SettleReimbursement fetches the claim
// before writing, and an id naming nothing is the client's mistake about a
// path segment, not a server failure.
func TestPostSettlementOnAnUnknownClaimIs404(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	rec := postSettlement(t, r, 9_999, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("settling an unknown claim = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostSettlementRejectsAMalformedOccurredOn(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)
	memberID := memberFor(t, r, "Jane")

	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 80_000, IncurredOn: "2026-08-10",
	})
	var claim reimbursementResponse
	if err := json.NewDecoder(createRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}

	rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID, OccurredOn: "not-a-date",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("settle with a malformed date = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostReimbursementsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/reimbursements with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetReimbursementsRequiresAFund(t *testing.T) {
	rec := getReimbursements(t, testRouter(t), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/reimbursements before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostSettlementRequiresAFund(t *testing.T) {
	rec := postSettlement(t, testRouter(t), 1, settleReimbursementRequest{
		AccountID: 1, OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("settle before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestPostSettlementRejectsANonNumericID covers the one check this handler
// owns itself: {id} is a path segment, so a non-numeric one never reaches
// the ledger to be judged there.
func TestPostSettlementRejectsANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements/abc/settle",
		strings.NewReader(`{"account_id":1,"occurred_on":"2026-08-20"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("settle with a non-numeric id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostSettlementRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements/1/settle", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("settle with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestNoWaiveRouteExists is the acceptance criterion stated as a test: PRD
// §7.4 never asks to waive a claim, so the route is absent and stays absent.
func TestNoWaiveRouteExists(t *testing.T) {
	r := testRouter(t)
	setUpFundForTransactions(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reimbursements/1/waive", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/reimbursements/1/waive = %d, want %d - no waive route is in scope", rec.Code, http.StatusNotFound)
	}
}

func decodeReimbursements(t *testing.T, rec *httptest.ResponseRecorder) []reimbursementResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var claims []reimbursementResponse
	if err := json.NewDecoder(rec.Body).Decode(&claims); err != nil {
		t.Fatalf("decoding reimbursements: %v", err)
	}
	return claims
}
