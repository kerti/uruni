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
)

func postIncidental(t *testing.T, r http.Handler, req openIncidentalRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling incidental request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/incidentals", bytes.NewReader(body)))
	return rec
}

func getIncidentals(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/incidentals"+query, nil))
	return rec
}

func getIncidentalDetail(t *testing.T, r http.Handler, purposeID int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/incidentals/%d", purposeID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func postCloseIncidental(t *testing.T, r http.Handler, purposeID int64, req closeIncidentalRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling close request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/incidentals/%d/close", purposeID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

func decodeIncidental(t *testing.T, rec *httptest.ResponseRecorder) incidentalResponse {
	t.Helper()
	var got incidentalResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding incidental response: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

func decodeIncidentals(t *testing.T, rec *httptest.ResponseRecorder) []incidentalResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/incidentals = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []incidentalResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding incidentals: %v", err)
	}
	return got
}

// openIncidentalFor opens one envelope for "Jane's wedding" and returns it.
func openIncidentalFor(t *testing.T, r http.Handler, occasion, openedOn string) incidentalResponse {
	t.Helper()
	rec := postIncidental(t, r, openIncidentalRequest{Occasion: occasion, OpenedOn: openedOn})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/incidentals = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return decodeIncidental(t, rec)
}

func TestPostIncidentalsRequiresAFund(t *testing.T) {
	rec := postIncidental(t, testRouter(t), openIncidentalRequest{Occasion: "Jane's wedding", OpenedOn: "2026-08-12"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/incidentals before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestOpenIncidentalCreatesAnOpenEnvelope pins the create response shape:
// a fresh envelope, unclosed, with the target amount it was opened with.
func TestOpenIncidentalCreatesAnOpenEnvelope(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	target := int64(500_000)
	rec := postIncidental(t, r, openIncidentalRequest{
		Occasion: "Jane's wedding", TargetAmount: &target, OpenedOn: "2026-08-12",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/incidentals = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	created := decodeIncidental(t, rec)
	if created.PurposeID == 0 {
		t.Error("purpose_id = 0, want a real id")
	}
	if created.Occasion != "Jane's wedding" {
		t.Errorf("occasion = %q, want %q", created.Occasion, "Jane's wedding")
	}
	if created.TargetAmount == nil || *created.TargetAmount != 500_000 {
		t.Errorf("target_amount = %v, want 500000", created.TargetAmount)
	}
	if created.OpenedOn != "2026-08-12" {
		t.Errorf("opened_on = %q, want %q", created.OpenedOn, "2026-08-12")
	}
	if created.ClosedOn != nil {
		t.Errorf("closed_on = %v, want nil - a freshly opened envelope is not closed", created.ClosedOn)
	}
}

// TestPostIncidentalsRejectsWhatTheLedgerRefuses proves this handler
// validates nothing itself: an empty occasion, a non-positive target and a
// calendar-invalid opened_on all come back as ErrInvalidArgument through
// mapLedgerError.
func TestPostIncidentalsRejectsWhatTheLedgerRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  openIncidentalRequest
	}{
		{"empty occasion", openIncidentalRequest{Occasion: "   ", OpenedOn: "2026-08-12"}},
		{"zero target", func() openIncidentalRequest {
			zero := int64(0)
			return openIncidentalRequest{Occasion: "Jane's wedding", TargetAmount: &zero, OpenedOn: "2026-08-12"}
		}()},
		{"negative target", func() openIncidentalRequest {
			neg := int64(-500_000)
			return openIncidentalRequest{Occasion: "Jane's wedding", TargetAmount: &neg, OpenedOn: "2026-08-12"}
		}()},
		{"malformed opened_on", openIncidentalRequest{Occasion: "Jane's wedding", OpenedOn: "2026-02-30"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)
			setUpFund(t, r)

			rec := postIncidental(t, r, tc.req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/incidentals = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "invalid_argument" {
				t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
			}
		})
	}
}

func TestPostIncidentalsRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/incidentals", strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/incidentals with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetIncidentalsRequiresAFund(t *testing.T) {
	rec := getIncidentals(t, testRouter(t), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/incidentals before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetIncidentalDetailRequiresAFund(t *testing.T) {
	rec := getIncidentalDetail(t, testRouter(t), 1)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/incidentals/{id} before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostCloseRequiresAFund(t *testing.T) {
	rec := postCloseIncidental(t, testRouter(t), 1, closeIncidentalRequest{AccountID: 1, ClosedOn: "2026-08-20"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/incidentals/{id}/close before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetIncidentalDetailOnAnUnknownPurposeIs404 covers the id naming
// nothing: the path segment is the client's mistake, not a server fault.
func TestGetIncidentalDetailOnAnUnknownPurposeIs404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getIncidentalDetail(t, r, 9_999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/incidentals/9999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetIncidentalDetailRejectsANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/incidentals/abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/incidentals/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestGetIncidentalsOpenFiltersToOpenEnvelopes is the filter's acceptance
// criterion: the unfiltered list keeps every envelope as history, ?open=true
// keeps only the ones still collecting.
func TestGetIncidentalsOpenFiltersToOpenEnvelopes(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	still := openIncidentalFor(t, r, "Flood relief", "2026-08-01")
	toClose := openIncidentalFor(t, r, "Jane's wedding", "2026-08-02")

	closeRec := postCloseIncidental(t, r, toClose.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	})
	if closeRec.Code != http.StatusOK {
		t.Fatalf("close = %d, want %d (body: %s)", closeRec.Code, http.StatusOK, closeRec.Body.String())
	}

	all := decodeIncidentals(t, getIncidentals(t, r, ""))
	if len(all) != 2 {
		t.Fatalf("GET /api/incidentals returned %d envelopes, want 2 - a closed one is still history", len(all))
	}

	open := decodeIncidentals(t, getIncidentals(t, r, "?open=true"))
	if len(open) != 1 {
		t.Fatalf("GET /api/incidentals?open=true returned %d envelopes, want 1", len(open))
	}
	if open[0].PurposeID != still.PurposeID {
		t.Errorf("open envelope = %d, want the still-open %d", open[0].PurposeID, still.PurposeID)
	}

	if got := decodeIncidentals(t, getIncidentals(t, r, "?open=false")); len(got) != 2 {
		t.Errorf("GET /api/incidentals?open=false returned %d envelopes, want 2 - explicitly asking not to filter", len(got))
	}
}

func TestGetIncidentalsRejectsAnUnparseableOpenFilter(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := getIncidentals(t, r, "?open=yes")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/incidentals?open=yes = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestGetIncidentalDetailReturnsTotals is the detail route's acceptance
// criterion: collected and disbursed, summed from the transactions actually
// posted against the envelope's purpose - not stored anywhere on the row.
func TestGetIncidentalDetailReturnsTotals(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 30_000, OccurredOn: "2026-08-03",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("disbursement = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := getIncidentalDetail(t, r, envelope.PurposeID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/incidentals/{id} = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail incidentalDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding incidental detail: %v", err)
	}
	if detail.PurposeID != envelope.PurposeID {
		t.Errorf("purpose_id = %d, want %d", detail.PurposeID, envelope.PurposeID)
	}
	if detail.CollectedAmount != 100_000 {
		t.Errorf("collected_amount = %d, want 100000", detail.CollectedAmount)
	}
	if detail.DisbursedAmount != 30_000 {
		t.Errorf("disbursed_amount = %d, want 30000", detail.DisbursedAmount)
	}
	if detail.ClosedOn != nil {
		t.Errorf("closed_on = %v, want nil - still open", detail.ClosedOn)
	}
}

// TestCloseIncidentalRollsLeftoverVerifiedThroughBalances is the slice's
// central acceptance criterion: open, contribute through the ordinary
// transactions endpoint, close, and prove the leftover actually moved by
// reading the fund and purpose balances - never by trusting the ledger's own
// return value alone.
func TestCloseIncidentalRollsLeftoverVerifiedThroughBalances(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 30_000, OccurredOn: "2026-08-03",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("disbursement = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	ctx := context.Background()
	fundBefore, err := l.FundBalance(ctx, setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}
	mainBefore, err := l.PurposeBalance(ctx, setup.Fund.ID, setup.MainPurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) before = %v, want no error", err)
	}

	closeRec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	})
	if closeRec.Code != http.StatusOK {
		t.Fatalf("POST /api/incidentals/{id}/close = %d, want %d (body: %s)", closeRec.Code, http.StatusOK, closeRec.Body.String())
	}
	var closed closeIncidentalResponse
	if err := json.NewDecoder(closeRec.Body).Decode(&closed); err != nil {
		t.Fatalf("decoding close response: %v", err)
	}
	if closed.Incidental.ClosedOn == nil || *closed.Incidental.ClosedOn != "2026-08-20" {
		t.Errorf("closed_on = %v, want %q", closed.Incidental.ClosedOn, "2026-08-20")
	}

	// The balance check is the acceptance criterion itself: the fund's total
	// is unchanged (a roll moves money, it does not create or destroy it),
	// the envelope's own purpose balance goes to exactly 0, and main rises
	// by exactly the leftover - all read fresh off the ledger, never taken
	// from closed.RolledAmount alone.
	fundAfter, err := l.FundBalance(ctx, setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	if fundAfter != fundBefore {
		t.Errorf("FundBalance() before=%d after=%d, want identical - a roll moves money, it does not create or destroy it", fundBefore, fundAfter)
	}

	envelopeBalance, err := l.PurposeBalance(ctx, setup.Fund.ID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(envelope) = %v, want no error", err)
	}
	if envelopeBalance != 0 {
		t.Errorf("PurposeBalance(envelope) = %d, want 0 - the leftover rolled out", envelopeBalance.Int64())
	}

	mainAfter, err := l.PurposeBalance(ctx, setup.Fund.ID, setup.MainPurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) after = %v, want no error", err)
	}
	if mainAfter != mainBefore+70_000 {
		t.Errorf("PurposeBalance(main) after = %d, want %d (before + the 70000 leftover)", mainAfter.Int64(), (mainBefore + 70_000).Int64())
	}
}

// TestCloseIncidentalZeroLeftoverPostsNothing and its negative-leftover
// sibling below are the two "nothing to roll" boundaries: both close the
// envelope but post no transaction, verified through the fund's own
// balance rather than assumed from a 201 status alone.
func TestCloseIncidentalZeroLeftoverPostsNothing(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-03",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("disbursement = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	ctx := context.Background()
	mainBefore, err := l.PurposeBalance(ctx, setup.Fund.ID, setup.MainPurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) before = %v, want no error", err)
	}

	closeRec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	})
	if closeRec.Code != http.StatusOK {
		t.Fatalf("close = %d, want %d (body: %s)", closeRec.Code, http.StatusOK, closeRec.Body.String())
	}
	var closed closeIncidentalResponse
	if err := json.NewDecoder(closeRec.Body).Decode(&closed); err != nil {
		t.Fatalf("decoding close response: %v", err)
	}
	if closed.RolledAmount != 0 {
		t.Errorf("rolled_amount = %d, want 0", closed.RolledAmount)
	}
	if closed.Incidental.ClosedOn == nil || *closed.Incidental.ClosedOn != "2026-08-20" {
		t.Errorf("closed_on = %v, want %q", closed.Incidental.ClosedOn, "2026-08-20")
	}

	mainAfter, err := l.PurposeBalance(ctx, setup.Fund.ID, setup.MainPurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) after = %v, want no error", err)
	}
	if mainAfter != mainBefore {
		t.Errorf("PurposeBalance(main) before=%d after=%d, want identical - a zero leftover posts nothing", mainBefore.Int64(), mainAfter.Int64())
	}
}

func TestCloseIncidentalNegativeLeftoverPostsNothing(t *testing.T) {
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-03",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("disbursement = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	ctx := context.Background()
	mainBefore, err := l.PurposeBalance(ctx, setup.Fund.ID, setup.MainPurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) before = %v, want no error", err)
	}

	closeRec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	})
	if closeRec.Code != http.StatusOK {
		t.Fatalf("close = %d, want %d (body: %s)", closeRec.Code, http.StatusOK, closeRec.Body.String())
	}
	var closed closeIncidentalResponse
	if err := json.NewDecoder(closeRec.Body).Decode(&closed); err != nil {
		t.Fatalf("decoding close response: %v", err)
	}
	if closed.RolledAmount != 0 {
		t.Errorf("rolled_amount = %d, want 0", closed.RolledAmount)
	}

	mainAfter, err := l.PurposeBalance(ctx, setup.Fund.ID, setup.MainPurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) after = %v, want no error", err)
	}
	if mainAfter != mainBefore {
		t.Errorf("PurposeBalance(main) before=%d after=%d, want identical - an over-disbursed envelope posts nothing on close", mainBefore.Int64(), mainAfter.Int64())
	}
}

// TestCloseIncidentalTwiceReturnsItsNamed409 covers the closed-once rule at
// the route: the second call is a conflict with its own code, not a second
// roll and not a generic 500.
func TestCloseIncidentalTwiceReturnsItsNamed409(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	first := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first close = %d, want %d (body: %s)", first.Code, http.StatusOK, first.Body.String())
	}

	second := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-21",
	})
	if second.Code != http.StatusConflict {
		t.Fatalf("second close = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "incidental_already_closed" {
		t.Errorf("error code = %q, want %q", got.Code, "incidental_already_closed")
	}
}

func TestPostCloseOnAnUnknownPurposeIs404(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postCloseIncidental(t, r, 9_999, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("close on an unknown purpose = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostCloseRejectsAMalformedClosedOn(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	rec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "not-a-date",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("close with a malformed date = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostCloseRejectsANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/incidentals/abc/close",
		strings.NewReader(`{"account_id":1,"closed_on":"2026-08-20"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("close with a non-numeric id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostCloseRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/incidentals/%d/close", envelope.PurposeID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{oops")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("close with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestNoContributeRouteExists is the acceptance criterion stated as a test:
// there is no dedicated contribute route - a contribution to an envelope is
// an ordinary transaction posted through POST /api/transactions.
func TestNoContributeRouteExists(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/incidentals/%d/contribute", envelope.PurposeID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"amount":1}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST %s = %d, want %d - no contribute route is in scope", path, rec.Code, http.StatusNotFound)
	}
}
