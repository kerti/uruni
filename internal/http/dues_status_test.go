package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

func getDuesStatus(t *testing.T, r http.Handler, period string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-status?period="+period, nil))
	return rec
}

// getOutstandingDues issues GET /api/members/{id}/outstanding-dues. through
// is appended as a query parameter only when non-empty, so a caller testing
// the omitted-through default can pass "".
func getOutstandingDues(t *testing.T, r http.Handler, memberID int64, through string) *httptest.ResponseRecorder {
	t.Helper()
	path := fmt.Sprintf("/api/members/%d/outstanding-dues", memberID)
	if through != "" {
		path += "?through=" + through
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestGetDuesStatusRequiresAFund(t *testing.T) {
	rec := getDuesStatus(t, testRouter(t), "2026-08")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/dues-status before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetDuesStatusReturnsPerMemberStatus is the slice's dues-status
// acceptance criterion: unpaid, partial and paid all read back correctly
// for one period, across different members of the same tier.
func TestGetDuesStatusReturnsPerMemberStatus(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	tierRec := postDuesTier(t, r, "Full")
	if tierRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d (body: %s)", tierRec.Code, http.StatusCreated, tierRec.Body.String())
	}
	var tier duesTierResponse
	if err := json.NewDecoder(tierRec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}

	rateRec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2026-01"})
	if rateRec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rateRec.Code, http.StatusCreated, rateRec.Body.String())
	}

	joined := "2026-01-15"
	unpaidRec := postMember(t, r, memberRequest{Name: "Unpaid", TierID: &tier.ID, JoinedOn: &joined})
	var unpaid memberResponse
	if err := json.NewDecoder(unpaidRec.Body).Decode(&unpaid); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	partialRec := postMember(t, r, memberRequest{Name: "Partial", TierID: &tier.ID, JoinedOn: &joined})
	var partial memberResponse
	if err := json.NewDecoder(partialRec.Body).Decode(&partial); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: partial.ID, OccurredOn: "2026-08-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 10_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments (partial) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	paidRec := postMember(t, r, memberRequest{Name: "Paid", TierID: &tier.ID, JoinedOn: &joined})
	var paid memberResponse
	if err := json.NewDecoder(paidRec.Body).Decode(&paid); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: paid.ID, OccurredOn: "2026-08-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments (paid) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := getDuesStatus(t, r, "2026-08")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []duesStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if len(got) != 3 {
		t.Fatalf("GET /api/dues-status returned %d rows, want 3", len(got))
	}

	byID := map[int64]duesStatusResponse{}
	for _, s := range got {
		byID[s.Member.ID] = s
	}

	if s, ok := byID[unpaid.ID]; !ok {
		t.Error("unpaid member missing from the response")
	} else {
		if s.Status != "unpaid" {
			t.Errorf("unpaid member status = %q, want %q", s.Status, "unpaid")
		}
		if s.OwedAmount != 25_000 {
			t.Errorf("unpaid member owed_amount = %d, want %d", s.OwedAmount, 25_000)
		}
		if s.PaidAmount != 0 {
			t.Errorf("unpaid member paid_amount = %d, want %d", s.PaidAmount, 0)
		}
	}

	if s, ok := byID[partial.ID]; !ok {
		t.Error("partial member missing from the response")
	} else {
		if s.Status != "partial" {
			t.Errorf("partial member status = %q, want %q", s.Status, "partial")
		}
		if s.PaidAmount != 10_000 {
			t.Errorf("partial member paid_amount = %d, want %d", s.PaidAmount, 10_000)
		}
	}

	if s, ok := byID[paid.ID]; !ok {
		t.Error("paid member missing from the response")
	} else {
		if s.Status != "paid" {
			t.Errorf("paid member status = %q, want %q", s.Status, "paid")
		}
		if s.PaidAmount != 25_000 {
			t.Errorf("paid member paid_amount = %d, want %d", s.PaidAmount, 25_000)
		}
	}
}

// TestGetDuesStatusRejectsAMalformedPeriod is this route's half of the
// slice's malformed-input acceptance criterion: DuesStatusForPeriod's own
// validateDuesPeriod check answers, the handler passes the raw query
// parameter through unvalidated.
func TestGetDuesStatusRejectsAMalformedPeriod(t *testing.T) {
	for _, period := range []string{"", "2026-13", "not-a-period"} {
		r := testRouter(t)
		setUpFund(t, r)

		rec := getDuesStatus(t, r, period)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/dues-status?period=%q = %d, want %d (body: %s)", period, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
		}
	}
}

// TestGetDuesStatusExcludesAMemberWithNoTier proves a member carrying no
// dues obligation never appears here at all - not as unpaid, not as
// anything - since DuesStatusForPeriod's own doc comment names this as the
// roster boundary a member with tier_id == nil sits outside of.
func TestGetDuesStatusExcludesAMemberWithNoTier(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	if rec := postMember(t, r, memberRequest{Name: "No Tier"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := getDuesStatus(t, r, "2026-08")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("GET /api/dues-status with only a no-tier member = %q, want %q", got, "[]\n")
	}
}

// --- GET /api/members/{id}/outstanding-dues (#186) ---------------------------

// TestGetOutstandingDuesReturnsUnpaidAndPartialPeriodsOldestFirst is this
// route's own acceptance criterion, exercised through the real HTTP surface
// rather than the ledger method directly: a member with one unpaid, one
// partial and one paid period gets back exactly the two outstanding ones,
// oldest first.
func TestGetOutstandingDuesReturnsUnpaidAndPartialPeriodsOldestFirst(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	tierRec := postDuesTier(t, r, "Full")
	var tier duesTierResponse
	if err := json.NewDecoder(tierRec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	joined := "2026-01-01"
	memberRec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID, JoinedOn: &joined})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-03-15",
		Periods: []duesPaymentPeriod{
			{DuesPeriod: "2026-02", Amount: 10_000},
			{DuesPeriod: "2026-03", Amount: 25_000},
		},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := getOutstandingDues(t, r, member.ID, "2026-03")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/members/{id}/outstanding-dues = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []outstandingDuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("GET /api/members/{id}/outstanding-dues returned %d rows, want 2 (body: %s)", len(got), rec.Body.String())
	}
	if got[0].Period != "2026-01" || got[0].Status != "unpaid" {
		t.Errorf("row 0 = %+v, want period 2026-01, status unpaid", got[0])
	}
	if got[1].Period != "2026-02" || got[1].Status != "partial" || got[1].PaidAmount != 10_000 {
		t.Errorf("row 1 = %+v, want period 2026-02, status partial, paid_amount 10000", got[1])
	}
}

// TestGetOutstandingDuesOnAnotherFundsMemberIs404 mirrors
// TestGetReconciliationDetailOnAnotherFundsSnapshotIs404: a real member id
// belonging to a second fund must read as not-found through this app's
// router, which only ever resolves to the first (and only, per v1) fund
// setup created - it must never be found and only then rejected for
// ownership.
func TestGetOutstandingDuesOnAnotherFundsMemberIs404(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setUpFund(t, r)

	q := store.New(sqlDB)
	ctx := context.Background()
	otherFund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	otherMember, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: otherFund.ID, Name: "John", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(other fund) = %v, want no error", err)
	}

	rec := getOutstandingDues(t, r, otherMember.ID, "2026-06")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/members/{other fund's id}/outstanding-dues = %d, want %d (body: %s)",
			rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetOutstandingDuesRejectsAMalformedThrough is this route's half of the
// slice's malformed-input acceptance criterion: OutstandingDuesForMember's
// own validateDuesPeriod check answers, the handler passes the raw ?through=
// query parameter through unvalidated, same as getDuesStatus does for
// ?period=.
func TestGetOutstandingDuesRejectsAMalformedThrough(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	for _, through := range []string{"2026-13", "2026-1", "not-a-period"} {
		rec := getOutstandingDues(t, r, member.ID, through)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET .../outstanding-dues?through=%q = %d, want %d (body: %s)", through, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_argument" {
			t.Errorf("through=%q: error code = %q, want %q", through, got.Code, "invalid_argument")
		}
	}
}

// The member id in the path is parsed by this handler rather than by
// resolveMember, so its own bad-input path needs covering: a non-numeric id
// is the caller's mistake, not a missing member, and reads as 400 rather
// than 404.
func TestGetOutstandingDuesRejectsANonNumericMemberID(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/members/not-a-number/outstanding-dues", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/members/not-a-number/outstanding-dues = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// Before setup there is no fund to scope the lookup to, so the route answers
// the same "run setup first" 404 every other fund-scoped route does - not a
// 200 with an empty list, which would read as "this member owes nothing".
func TestGetOutstandingDuesRequiresAFund(t *testing.T) {
	rec := getOutstandingDues(t, testRouter(t), 1, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET .../outstanding-dues before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}
