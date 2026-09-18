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

	var page membersPageResponse
	if err := json.NewDecoder(list.Body).Decode(&page); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, list.Body.String())
	}
	// *string fields compare by address, not value, so this can't use != on
	// the struct - compare the JSON each side encodes to instead. created
	// itself has zero-value tier_name/current_rate/arrears_months (no
	// tier), and so does this member's own list row (no tier means no dues
	// obligation, hence no arrears either) - the two are expected to match
	// field for field.
	gotJSON, _ := json.Marshal(page.Members)
	wantJSON, _ := json.Marshal([]memberResponse{created})
	if len(page.Members) != 1 || string(gotJSON) != string(wantJSON) {
		t.Errorf("GET /api/members members = %s, want %s", gotJSON, wantJSON)
	}
	if page.NextCursor != nil {
		t.Errorf("GET /api/members next_cursor = %v, want nil (one member fits on one page)", page.NextCursor)
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
	// members: [] on the wire, never null, so a client need not nil-check
	// before ranging - and next_cursor: null, the envelope #233 always
	// answers with now, never the bare array #65 originally shipped.
	want := `{"members":[],"next_cursor":null}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("GET /api/members body = %q, want %q", got, want)
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
	return authedRouterFor(t, sqlDB), l
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
	want := `{"members":[],"next_cursor":null}` + "\n"
	if got := list.Body.String(); got != want {
		t.Errorf("GET /api/members after delete = %q, want %q", got, want)
	}
}

// TestDeleteMemberWithTransactionsReturns409 is #81's other must-have: a
// member with real money posted against them refuses the delete with a
// clean 409, not a 500 and not a cascade that would orphan the transaction.
// This rides the composite foreign key's own refusal through mapSQLiteError
// - there is no hand-rolled reference check to bypass.
func TestDeleteMemberWithTransactionsReturns409(t *testing.T) {
	r, l := testRouterAndLedger(t)

	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	ctx := context.Background()
	if _, err := l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
		FundID: setup.Fund.ID, AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		MemberID: member.ID, OccurredOn: "2026-01-15",
		Periods: []ledger.PeriodAmount{{DuesPeriod: "2026-01", Amount: 50_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
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
	var page membersPageResponse
	if err := json.NewDecoder(list.Body).Decode(&page); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(page.Members) != 1 {
		t.Errorf("GET /api/members after a refused delete = %d members, want 1", len(page.Members))
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

// --- GET /api/members: paging, search, tier fields, arrears (#233, ADR-032) ---

func getMembers(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/members"
	if query != "" {
		path += "?" + query
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decodeMembersPage(t *testing.T, rec *httptest.ResponseRecorder) membersPageResponse {
	t.Helper()
	var page membersPageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decoding members page response: %v (body: %s)", err, rec.Body.String())
	}
	return page
}

// A roster bigger than one page (membersPageSize=25) is walked to the end
// via next_cursor with no member skipped and none duplicated - #233's own
// "one request per page regardless of member count" and ADR-032's keyset
// contract together. Names are zero-padded so lexicographic order (what the
// server actually sorts by) matches creation order, keeping the assertions
// readable without depending on that order for correctness - the test
// collects every id it saw into a set regardless of what order pages
// arrive in.
func TestGetMembersPagesLargerRosterWithoutSkippingOrDuplicating(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	const total = 30 // > membersPageSize (25), so at least two pages are forced
	created := make(map[int64]bool, total)
	for i := 0; i < total; i++ {
		rec := postMember(t, r, memberRequest{Name: fmt.Sprintf("Member %02d", i)})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/members #%d = %d, want %d (body: %s)", i, rec.Code, http.StatusCreated, rec.Body.String())
		}
		var m memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
			t.Fatalf("decoding member response: %v", err)
		}
		created[m.ID] = true
	}

	seen := make(map[int64]bool, total)
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > total { // guard against an infinite loop if paging is broken
			t.Fatalf("paged %d times without reaching the end - next_cursor never went nil", pages)
		}
		page := decodeMembersPage(t, getMembers(t, r, "cursor="+cursor))
		if len(page.Members) > membersPageSize {
			t.Fatalf("page %d returned %d members, want at most %d", pages, len(page.Members), membersPageSize)
		}
		for _, m := range page.Members {
			if seen[m.ID] {
				t.Errorf("member %d (%s) appeared on more than one page - duplicate", m.ID, m.Name)
			}
			seen[m.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}

	if len(seen) != total {
		t.Errorf("paged through %d members, want %d", len(seen), total)
	}
	for id := range created {
		if !seen[id] {
			t.Errorf("member %d was created but never seen while paging", id)
		}
	}
	// The second page must have existed at all - a single oversized page
	// would make the skip/duplicate assertions above vacuous.
	if pages < 2 {
		t.Fatalf("paged only %d time(s) for %d members and a page size of %d, want at least 2 pages", pages, total, membersPageSize)
	}
}

// ?q= is a case-insensitive substring match on name, computed on the
// server (ADR-032: "Client-side filtering of a paged list is prohibited
// outright") - a lowercase query must find an uppercase name and vice
// versa, and must not find a name that does not contain it at all.
func TestGetMembersQMatchesCaseInsensitiveSubstringServerSide(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	for _, name := range []string{"Budi Santoso", "BUDIman Wijaya", "Siti Aminah"} {
		if rec := postMember(t, r, memberRequest{Name: name}); rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/members(%q) = %d, want %d (body: %s)", name, rec.Code, http.StatusCreated, rec.Body.String())
		}
	}

	page := decodeMembersPage(t, getMembers(t, r, "q=budi"))
	if len(page.Members) != 2 {
		t.Fatalf("GET /api/members?q=budi = %d members, want 2 (got %+v)", len(page.Members), page.Members)
	}
	for _, m := range page.Members {
		if m.Name != "Budi Santoso" && m.Name != "BUDIman Wijaya" {
			t.Errorf("GET /api/members?q=budi matched %q, want only the two names containing \"budi\"", m.Name)
		}
	}

	none := decodeMembersPage(t, getMembers(t, r, "q=zzz-no-such-substring"))
	if len(none.Members) != 0 {
		t.Errorf("GET /api/members?q=zzz-no-such-substring = %d members, want 0", len(none.Members))
	}
}

// A malformed cursor is 400 invalid_argument, the same answer
// decodeTransactionsCursor gives GET /api/transactions (#225).
func TestGetMembersMalformedCursorReturns400InvalidArgument(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	for _, cursor := range []string{"not-valid-base64!!!", "aGVsbG8"} { // second: valid base64, no "|" separator
		t.Run(cursor, func(t *testing.T) {
			rec := getMembers(t, r, "cursor="+cursor)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET /api/members?cursor=%s = %d, want %d (body: %s)", cursor, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "invalid_argument" {
				t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
			}
		})
	}
}

// next_cursor is null on the last (here, only) page - a client must be able
// to tell "no more pages" apart from "here is another cursor" without
// special-casing an empty members array.
func TestGetMembersNextCursorIsNilOnLastPage(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}
	if rec := postMember(t, r, memberRequest{Name: "Jane"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeMembersPage(t, getMembers(t, r, ""))
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v, want nil (one member fits on one page)", page.NextCursor)
	}
}

// A member's row carries its tier's name and the rate effective for the
// current month - and both read nil for a tier with no rate yet decided
// (the "madya TBD" case, PRD section 6), never an invented amount.
func TestGetMembersRowShowsTierNameAndCurrentRateNilWhenUndecided(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Madya")
	// No rate posted for this tier at all - "TBD".

	rec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeMembersPage(t, getMembers(t, r, ""))
	if len(page.Members) != 1 {
		t.Fatalf("GET /api/members = %d members, want 1", len(page.Members))
	}
	got := page.Members[0]
	if got.TierName == nil || *got.TierName != "Madya" {
		t.Errorf("tier_name = %v, want %q", got.TierName, "Madya")
	}
	if got.CurrentRate != nil {
		t.Errorf("current_rate = %v, want nil (tier has no rate effective yet)", got.CurrentRate)
	}
}

func TestGetMembersRowShowsCurrentRateWhenOneIsEffective(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2020-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeMembersPage(t, getMembers(t, r, ""))
	if len(page.Members) != 1 {
		t.Fatalf("GET /api/members = %d members, want 1", len(page.Members))
	}
	got := page.Members[0]
	if got.CurrentRate == nil || *got.CurrentRate != 50_000 {
		t.Errorf("current_rate = %v, want 50000", got.CurrentRate)
	}
}

// arrearsMonthPeriods gives this file's own HTTP-level arrears tests the
// same relative-to-now periods internal/ledger's own
// TestArrearsMonthsForMember* tests use, for the same reason: a fixture
// pinned to a hard-coded calendar month eventually becomes a fixture about
// the past.
func arrearsMonthPeriods() (twoBack, oneBack, current string) {
	now := time.Now()
	return now.AddDate(0, -2, 0).Format("2006-01"), now.AddDate(0, -1, 0).Format("2006-01"), now.Format("2006-01")
}

// The full round trip for #233's own headline acceptance criterion: a
// member with a part-paid earlier period reads arrears_months=1 on the
// roster row, through the real route rather than the ledger method
// directly (dues_status_test.go in internal/ledger covers the derivation
// itself in isolation).
func TestGetMembersRowArrearsMonthsReflectsPartPaidEarlierPeriod(t *testing.T) {
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

	_, oneBack, _ := arrearsMonthPeriods()
	joinedOn := oneBack + "-01"
	memberRec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID, JoinedOn: &joinedOn})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID, MemberID: member.ID,
		OccurredOn: today,
		Periods:    []duesPaymentPeriod{{DuesPeriod: oneBack, Amount: 10_000}}, // less than the 25000 owed
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	page := decodeMembersPage(t, getMembers(t, r, ""))
	if len(page.Members) != 1 {
		t.Fatalf("GET /api/members = %d members, want 1", len(page.Members))
	}
	if got := page.Members[0].ArrearsMonths; got != 1 {
		t.Errorf("arrears_months = %d, want 1 (one part-paid period before the current one)", got)
	}
}

// PRD section 7.1: backdating a join date makes arrears appear on the
// roster row that were not there before - the full HTTP round trip for the
// property internal/ledger's own
// TestArrearsMonthsForMemberBackdatingJoinedOnMakesArrearsAppear proves at
// the derivation level.
func TestGetMembersRowArrearsMonthsAppearsAfterBackdatingJoinedOn(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2020-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	twoBack, _, current := arrearsMonthPeriods()
	joinedOn := current + "-01" // joined the current period: nothing owed before it
	memberRec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID, JoinedOn: &joinedOn})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	before := decodeMembersPage(t, getMembers(t, r, ""))
	if len(before.Members) != 1 || before.Members[0].ArrearsMonths != 0 {
		t.Fatalf("arrears_months before backdating = %+v, want exactly one member with 0", before.Members)
	}

	backdated := twoBack + "-01"
	patchRec := patchMember(t, r, member.ID, fmt.Sprintf(`{"joined_on":%q}`, backdated))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/members/%d = %d, want %d (body: %s)", member.ID, patchRec.Code, http.StatusOK, patchRec.Body.String())
	}

	after := decodeMembersPage(t, getMembers(t, r, ""))
	if len(after.Members) != 1 {
		t.Fatalf("GET /api/members after backdating = %d members, want 1", len(after.Members))
	}
	if got := after.Members[0].ArrearsMonths; got != 2 {
		t.Errorf("arrears_months after backdating joined_on to %q = %d, want 2 (two unpaid periods now fall in the window)", backdated, got)
	}
}

// The page-boundary case the 30-member test cannot reach: a roster of
// exactly membersPageSize. The handler peeks at one extra row to decide
// whether there is a next page, so exactly 25 must come back as one full
// page with next_cursor nil - never a cursor pointing at an empty 26th-row
// page, which would give the treasurer a "muat lebih banyak" button that
// loads nothing.
func TestGetMembersExactlyOnePageFullReturnsNoCursor(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	for i := 0; i < membersPageSize; i++ {
		rec := postMember(t, r, memberRequest{Name: fmt.Sprintf("Member %02d", i)})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/members #%d = %d, want %d (body: %s)", i, rec.Code, http.StatusCreated, rec.Body.String())
		}
	}

	page := decodeMembersPage(t, getMembers(t, r, ""))
	if len(page.Members) != membersPageSize {
		t.Errorf("GET /api/members returned %d members, want %d", len(page.Members), membersPageSize)
	}
	if page.NextCursor != nil {
		t.Errorf("GET /api/members next_cursor = %q, want nil - a roster of exactly %d is one full page, not two",
			*page.NextCursor, membersPageSize)
	}
}
