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

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func postMember(t *testing.T, r http.Handler, req memberRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling member request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/members", bytes.NewReader(body)))
	return rec
}

func TestPostMembersRequiresAFund(t *testing.T) {
	// No POST /api/setup yet - there is no fund to post a member against.
	rec := postMember(t, testRouter(t), memberRequest{Name: "Jane"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/members before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostMembersCreatesAndListReturnsIt(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	joined := "2026-01-15"
	rec := postMember(t, r, memberRequest{Name: "Jane", JoinedOn: &joined})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var created memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.ID == 0 {
		t.Error("member.id is zero")
	}
	if created.Name != "Jane" {
		t.Errorf("member.name = %q, want %q", created.Name, "Jane")
	}
	if created.JoinedOn == nil || *created.JoinedOn != joined {
		t.Errorf("member.joined_on = %v, want %q", created.JoinedOn, joined)
	}
	if created.TierID != nil {
		t.Errorf("member.tier_id = %v, want nil", created.TierID)
	}
	if created.InactiveOn != nil {
		t.Errorf("member.inactive_on = %v, want nil (creation cannot set it)", created.InactiveOn)
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/members", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/members = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}

	var members []memberResponse
	if err := json.NewDecoder(list.Body).Decode(&members); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, list.Body.String())
	}
	// *string fields compare by address, not value, so this can't use != on
	// the struct - compare the JSON each side encodes to instead.
	gotJSON, _ := json.Marshal(members)
	wantJSON, _ := json.Marshal([]memberResponse{created})
	if len(members) != 1 || string(gotJSON) != string(wantJSON) {
		t.Errorf("GET /api/members = %s, want %s", gotJSON, wantJSON)
	}
}

func TestGetMembersReturnsAnEmptyListBeforeAnyMemberExists(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/members", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/members = %d, want %d", rec.Code, http.StatusOK)
	}
	// [] on the wire, never null, so a client need not nil-check before ranging.
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("GET /api/members body = %q, want %q", got, "[]\n")
	}
}

func TestPostMembersRejectsAnEmptyName(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	// member.name CHECK (length(trim(name)) > 0) - proven through the shared
	// mapper, not a hand-rolled check in the handler.
	rec := postMember(t, r, memberRequest{Name: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/members with empty name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func TestPostMembersRejectsATierIDBelongingToNoFund(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	noSuchTier := int64(999)
	rec := postMember(t, r, memberRequest{Name: "Jane", TierID: &noSuchTier})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/members with a bad tier_id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostMembersRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/members", bytes.NewReader([]byte("{oops"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/members with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetMembersRequiresAFund(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/members", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/members before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// testRouterAndLedger is testRouter's twin for #81's one integration-style
// test: it needs a real *ledger.Ledger over the same *sql.DB the router
// answers requests against, so a PATCH through the HTTP route and a read
// through DuesStatusForPeriod see the same data.
func testRouterAndLedger(t *testing.T) (http.Handler, *ledger.Ledger) {
	t.Helper()
	sqlDB := testStoreDB(t)
	l := ledger.New(sqlDB)
	return New(testAssets(), testBuild, l, store.New(sqlDB), testLogger()), l
}

func patchMember(t *testing.T, r http.Handler, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/members/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body)))
	return rec
}

func deleteMember(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/members/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}

// setUpMember is the fixture every plain PATCH/DELETE member test needs: a
// fund, then a member with no tier - renaming, deleting and clearing a
// nullable field none of them need dues data behind them.
func setUpMember(t *testing.T, r http.Handler, name string) memberResponse {
	t.Helper()
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}
	joined := "2026-01-15"
	rec := postMember(t, r, memberRequest{Name: name, JoinedOn: &joined})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	return created
}

func TestPatchMemberRenamesIt(t *testing.T) {
	r := testRouter(t)
	member := setUpMember(t, r, "Jame")

	rec := patchMember(t, r, member.ID, `{"name":"Jane"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/members/%d = %d, want %d (body: %s)", member.ID, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.Name != "Jane" {
		t.Errorf("member.name = %q, want %q", got.Name, "Jane")
	}
	if got.JoinedOn == nil || *got.JoinedOn != *member.JoinedOn {
		t.Errorf("member.joined_on = %v, want unchanged %v", got.JoinedOn, member.JoinedOn)
	}
}

// TestPatchMemberTierIDAbsentPresentAndNull covers all three states #81
// asks for on a nullable field: the key missing from the body entirely
// leaves tier_id untouched, the key present with a value sets it, and the
// key present with JSON null clears it - which is what removes the dues
// obligation (issue #81, "clearing tier_id is what removes a dues
// obligation").
func TestPatchMemberTierIDAbsentPresentAndNull(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}
	tierRec := postDuesTier(t, r, "Full")
	if tierRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d", tierRec.Code, http.StatusCreated)
	}
	var tier duesTierResponse
	if err := json.NewDecoder(tierRec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	t.Run("absent leaves it unchanged", func(t *testing.T) {
		rec := patchMember(t, r, member.ID, `{"name":"Jane"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.TierID != nil {
			t.Errorf("member.tier_id = %v, want nil (tier_id absent from the request)", got.TierID)
		}
	})

	t.Run("present with a value sets it", func(t *testing.T) {
		rec := patchMember(t, r, member.ID, fmt.Sprintf(`{"tier_id":%d}`, tier.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.TierID == nil || *got.TierID != tier.ID {
			t.Errorf("member.tier_id = %v, want %d", got.TierID, tier.ID)
		}
	})

	t.Run("present with null clears it", func(t *testing.T) {
		rec := patchMember(t, r, member.ID, `{"tier_id":null}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.TierID != nil {
			t.Errorf("member.tier_id = %v, want nil (tier_id explicitly cleared)", got.TierID)
		}
	})
}

// TestPatchMemberInactiveOnAbsentPresentAndNull is inactive_on's version of
// the tier_id test above - the same three states, since #81 asks for both.
func TestPatchMemberInactiveOnAbsentPresentAndNull(t *testing.T) {
	r := testRouter(t)
	member := setUpMember(t, r, "Jane")

	t.Run("absent leaves it unchanged", func(t *testing.T) {
		rec := patchMember(t, r, member.ID, `{"name":"Jane"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.InactiveOn != nil {
			t.Errorf("member.inactive_on = %v, want nil (inactive_on absent from the request)", got.InactiveOn)
		}
	})

	t.Run("present with a value marks them inactive", func(t *testing.T) {
		rec := patchMember(t, r, member.ID, `{"inactive_on":"2026-03-15"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.InactiveOn == nil || *got.InactiveOn != "2026-03-15" {
			t.Errorf("member.inactive_on = %v, want %q", got.InactiveOn, "2026-03-15")
		}
	})

	t.Run("present with null reinstates them", func(t *testing.T) {
		rec := patchMember(t, r, member.ID, `{"inactive_on":null}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.InactiveOn != nil {
			t.Errorf("member.inactive_on = %v, want nil (inactive_on explicitly cleared)", got.InactiveOn)
		}
	})
}

// TestPatchMemberInactiveOnIsHonoredByDuesStatusForPeriod is the
// integration-style assertion #81 asks for: it goes through the route, then
// checks the *derived* status via internal/ledger's own
// DuesStatusForPeriod, not just that the column changed. DuesStatusForPeriod
// itself is untouched by this slice (TestDuesStatusForPeriodMemberOwesThe...
// and its "excluded after" sibling already cover its semantics directly);
// this test only proves the PATCH route actually reaches them.
func TestPatchMemberInactiveOnIsHonoredByDuesStatusForPeriod(t *testing.T) {
	r, l := testRouterAndLedger(t)

	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}
	tierRec := postDuesTier(t, r, "Full")
	if tierRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d", tierRec.Code, http.StatusCreated)
	}
	var tier duesTierResponse
	if err := json.NewDecoder(tierRec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	joined := "2026-01-15"
	memberRec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID, JoinedOn: &joined})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	// Leaves partway through March - owed for March in full, gone from April.
	rec := patchMember(t, r, member.ID, `{"inactive_on":"2026-03-15"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/members/%d = %d, want %d (body: %s)", member.ID, rec.Code, http.StatusOK, rec.Body.String())
	}

	ctx := context.Background()

	fundRec := httptest.NewRecorder()
	r.ServeHTTP(fundRec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	var fund fundResponse
	if err := json.NewDecoder(fundRec.Body).Decode(&fund); err != nil {
		t.Fatalf("decoding fund response: %v", err)
	}

	statusesMarch, err := l.DuesStatusForPeriod(ctx, fund.ID, "2026-03")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(2026-03) = %v, want no error", err)
	}
	found := false
	for _, s := range statusesMarch {
		if s.Member.ID == member.ID {
			found = true
			if s.OwedAmount.Int64() != 50_000 {
				t.Errorf("March owed = %d, want %d (owes the month they went inactive in full)", s.OwedAmount.Int64(), 50_000)
			}
		}
	}
	if !found {
		t.Errorf("member %d is not in March's roster, want present (they went inactive mid-March)", member.ID)
	}

	statusesApril, err := l.DuesStatusForPeriod(ctx, fund.ID, "2026-04")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(2026-04) = %v, want no error", err)
	}
	for _, s := range statusesApril {
		if s.Member.ID == member.ID {
			t.Errorf("member %d is in April's roster, want excluded (they went inactive in March)", member.ID)
		}
	}
}

func TestPatchMemberReturns404ForAnUnknownID(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := patchMember(t, r, 999, `{"name":"Jane"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH /api/members/999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPatchMemberReturns400ForANonNumericID(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/members/abc", strings.NewReader(`{"name":"Jane"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/members/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPatchMemberRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	member := setUpMember(t, r, "Jane")

	rec := patchMember(t, r, member.ID, "{oops")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/members/%d with malformed JSON = %d, want %d", member.ID, rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestPatchMemberRejectsAnEmptyName(t *testing.T) {
	r := testRouter(t)
	member := setUpMember(t, r, "Jane")

	// member.name CHECK (length(trim(name)) > 0) - proven through the shared
	// mapper, same as the creation-side test.
	rec := patchMember(t, r, member.ID, `{"name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/members/%d with empty name = %d, want %d (body: %s)", member.ID, rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func TestDeleteMemberWithNoTransactionsSucceeds(t *testing.T) {
	r := testRouter(t)
	member := setUpMember(t, r, "Jane")

	rec := deleteMember(t, r, member.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/members/%d = %d, want %d (body: %s)", member.ID, rec.Code, http.StatusNoContent, rec.Body.String())
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/members", nil))
	if got := list.Body.String(); got != "[]\n" {
		t.Errorf("GET /api/members after delete = %q, want %q", got, "[]\n")
	}
}

// TestDeleteMemberWithTransactionsReturns409 is #81's other must-have: a
// member with real money posted against them refuses the delete with a
// clean 409, not a 500 and not a cascade that would orphan the transaction.
// This rides the composite foreign key's own refusal through mapSQLiteError
// - there is no hand-rolled reference check to bypass.
func TestDeleteMemberWithTransactionsReturns409(t *testing.T) {
	r, l := testRouterAndLedger(t)

	setupRec := postSetup(t, r, "Test Fund")
	if setupRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", setupRec.Code, http.StatusCreated)
	}
	var setup setupResponse
	if err := json.NewDecoder(setupRec.Body).Decode(&setup); err != nil {
		t.Fatalf("decoding setup response: %v", err)
	}

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	ctx := context.Background()
	if _, err := l.PostDuesPayment(ctx, ledger.PostDuesPaymentParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		MemberID: member.ID, DuesPeriod: "2026-01", Amount: 50_000, OccurredOn: "2026-01-15",
	}); err != nil {
		t.Fatalf("PostDuesPayment() = %v, want no error", err)
	}

	rec := deleteMember(t, r, member.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE /api/members/%d with a real transaction = %d, want %d (body: %s)", member.ID, rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "referenced_by_other_records" {
		t.Errorf("error code = %q, want %q", got.Code, "referenced_by_other_records")
	}

	// The member and its transaction both survive - nothing was cascaded.
	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/members", nil))
	var members []memberResponse
	if err := json.NewDecoder(list.Body).Decode(&members); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("GET /api/members after a refused delete = %d members, want 1", len(members))
	}
}

func TestDeleteMemberReturns404ForAnUnknownID(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := deleteMember(t, r, 999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE /api/members/999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestDeleteMemberReturns400ForANonNumericID(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/members/abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE /api/members/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}
