package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

func getDuesStatus(t *testing.T, r http.Handler, period string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-status?period="+period, nil))
	return rec
}

// getOutstandingDues issues GET /api/members/{id}/outstanding-dues; through is
// sent only when non-empty, so "" tests the omitted-through default.
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
	t.Parallel()
	rec := getDuesStatus(t, testRouter(t), "2026-08")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/dues-status before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetDuesStatusReturnsPerMemberStatus is the dues-status criterion:
// unpaid, partial and paid read back correctly for one period across members
// of one tier.
func TestGetDuesStatusReturnsPerMemberStatus(t *testing.T) {
	t.Parallel()
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
		AccountID: setup.CashAccountID(t),
		MemberID:  partial.ID, OccurredOn: "2026-08-05",
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
		AccountID: setup.CashAccountID(t),
		MemberID:  paid.ID, OccurredOn: "2026-08-05",
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

// TestGetDuesStatusCarriesPaidThroughForAMemberPaidAhead checks #357's wire
// shape: paid_through names the last month of the run for a member paid ahead,
// and is null for one who only paid this month.
func TestGetDuesStatusCarriesPaidThroughForAMemberPaidAhead(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	var tier duesTierResponse
	if err := json.NewDecoder(postDuesTier(t, r, "Full").Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2026-01"})

	pay := func(name string, periods ...string) int64 {
		var m memberResponse
		if err := json.NewDecoder(postMember(t, r, memberRequest{Name: name, TierID: &tier.ID}).Body).Decode(&m); err != nil {
			t.Fatalf("decoding member response: %v", err)
		}
		req := duesPaymentRequest{AccountID: setup.CashAccountID(t), MemberID: m.ID, OccurredOn: "2026-10-01"}
		for _, period := range periods {
			req.Periods = append(req.Periods, duesPaymentPeriod{DuesPeriod: period, Amount: 25_000})
		}
		if rec := postDuesPayment(t, r, req); rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/dues-payments (%s) = %d, want %d (body: %s)", name, rec.Code, http.StatusCreated, rec.Body.String())
		}
		return m.ID
	}
	ahead := pay("Ahead", "2026-10", "2026-11", "2026-12")
	current := pay("Current", "2026-10")

	var got []duesStatusResponse
	if err := json.NewDecoder(getDuesStatus(t, r, "2026-10").Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	for _, s := range got {
		switch s.Member.ID {
		case ahead:
			if s.Status != "paid_in_advance" || s.PaidThrough == nil || *s.PaidThrough != "2026-12" {
				t.Errorf("ahead: status, paid_through = %q, %v; want paid_in_advance, 2026-12", s.Status, s.PaidThrough)
			}
		case current:
			if s.Status != "paid" || s.PaidThrough != nil {
				t.Errorf("current: status, paid_through = %q, %v; want paid, null", s.Status, s.PaidThrough)
			}
		}
	}
}

// TestGetDuesStatusRejectsAMalformedPeriod is this route's half of the
// malformed-input criterion: DuesStatusForPeriod's validateDuesPeriod answers,
// the handler passing the raw query parameter through.
func TestGetDuesStatusRejectsAMalformedPeriod(t *testing.T) {
	t.Parallel()
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

// TestGetDuesStatusExcludesAMemberWithNoTier: a member with no dues obligation
// never appears, not even as unpaid; DuesStatusForPeriod names tier_id == nil
// as outside the roster.
func TestGetDuesStatusExcludesAMemberWithNoTier(t *testing.T) {
	t.Parallel()
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

// --- GET /api/members/{id}/outstanding-dues (#186) ---

// TestGetOutstandingDuesReturnsUnpaidAndPartialPeriodsOldestFirst is this
// route's criterion through the real HTTP surface: a member with one unpaid,
// one partial and one paid period gets exactly the two outstanding, oldest
// first.
func TestGetOutstandingDuesReturnsUnpaidAndPartialPeriodsOldestFirst(t *testing.T) {
	t.Parallel()
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
		AccountID: setup.CashAccountID(t),
		MemberID:  member.ID, OccurredOn: "2026-03-15",
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
// TestGetReconciliationDetailOnAnotherFundsSnapshotIs404: a real member id of
// a second fund reads as not-found through this app's router, which resolves
// only to the one fund setup created, never found then rejected for ownership.
func TestGetOutstandingDuesOnAnotherFundsMemberIs404(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setUpFund(t, r)

	q := store.New(sqlDB)
	ctx := context.Background()
	otherFund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	otherMember, err := q.CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01",
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
// malformed-input criterion: OutstandingDuesForMember's validateDuesPeriod
// answers, the handler passing the raw ?through= through, as getDuesStatus
// does ?period=.
func TestGetOutstandingDuesRejectsAMalformedThrough(t *testing.T) {
	t.Parallel()
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

// The member id in the path is parsed here, not by resolveMember, so its
// bad-input path needs covering: a non-numeric id is the caller's mistake and
// reads 400, not 404.
func TestGetOutstandingDuesRejectsANonNumericMemberID(t *testing.T) {
	t.Parallel()
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

// Before setup there is no fund to scope the lookup, so the route answers the
// "run setup first" 404 of every fund-scoped route, not a 200 with an empty
// list, which would read as "this member owes nothing".
func TestGetOutstandingDuesRequiresAFund(t *testing.T) {
	t.Parallel()
	rec := getOutstandingDues(t, testRouter(t), 1, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET .../outstanding-dues before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetDuesStatusCarriesArrearsMonths is #740: a status row reads the same
// Tunggakan the Anggota roster does, relative to the real current month and
// not to the period asked for.
func TestGetDuesStatusCarriesArrearsMonths(t *testing.T) {
	t.Parallel()
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
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2020-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	twoBack, oneBack, current := arrearsMonthPeriods()
	joinedOn := twoBack + "-01"
	behindRec := postMember(t, r, memberRequest{Name: "Behind", TierID: &tier.ID, JoinedOn: &joinedOn})
	var behind memberResponse
	if err := json.NewDecoder(behindRec.Body).Decode(&behind); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	squareRec := postMember(t, r, memberRequest{Name: "Square", TierID: &tier.ID, JoinedOn: &joinedOn})
	var square memberResponse
	if err := json.NewDecoder(squareRec.Body).Decode(&square); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	today := time.Now().In(wib).Format("2006-01-02")
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), MemberID: square.ID, OccurredOn: today,
		Periods: []duesPaymentPeriod{{DuesPeriod: twoBack, Amount: 25_000}, {DuesPeriod: oneBack, Amount: 25_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Asked for the current month and for an earlier one: the arrears do not move.
	for _, period := range []string{current, oneBack} {
		rec := getDuesStatus(t, r, period)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/dues-status?period=%s = %d (body: %s)", period, rec.Code, rec.Body.String())
		}
		var got []duesStatusResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		byID := map[int64]int{}
		for _, row := range got {
			byID[row.Member.ID] = row.Member.ArrearsMonths
		}
		if byID[behind.ID] != 2 {
			t.Errorf("period %s: behind member arrears_months = %d, want 2", period, byID[behind.ID])
		}
		if byID[square.ID] != 0 {
			t.Errorf("period %s: settled member arrears_months = %d, want 0", period, byID[square.ID])
		}
	}
}
