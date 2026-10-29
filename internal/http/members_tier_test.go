package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
)

// The tier is effective-dated (ADR-040): PATCH states it "from a month", the
// roster shows the current one, and a dues-status row carries the tier at its
// period. The ledger tests prove the rules; these prove the wire.

func mustTier(t *testing.T, r http.Handler, name string, amount int64) int64 {
	t.Helper()
	rec := postDuesTier(t, r, name)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var tier duesTierResponse
	if err := json.NewDecoder(rec.Body).Decode(&tier); err != nil {
		t.Fatal(err)
	}
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: amount, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return tier.ID
}

func decodeMember(t *testing.T, rec *httptest.ResponseRecorder, want int) memberResponse {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, want, rec.Body.String())
	}
	var m memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decoding member: %v (body: %s)", err, rec.Body.String())
	}
	return m
}

func TestPatchMemberTierFromAMonthLeavesEarlierMonthsAtTheOldTier(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d", rec.Code)
	}
	tierA := mustTier(t, r, "Reguler", 10_000)
	tierB := mustTier(t, r, "Senior", 50_000)
	joined := "2026-01-01"
	member := decodeMember(t, postMember(t, r, memberRequest{Name: "Jane", TierID: &tierA, JoinedOn: &joined}), http.StatusCreated)
	if member.TierID == nil || *member.TierID != tierA {
		t.Fatalf("POST tier_id = %v, want %d", member.TierID, tierA)
	}

	now := ledger.CurrentDuesPeriod(time.Now(), wib)
	got := decodeMember(t, patchMember(t, r, member.ID, fmt.Sprintf(`{"tier_id":%d,"tier_effective_from":%q}`, tierB, now)), http.StatusOK)
	if got.TierID == nil || *got.TierID != tierB {
		t.Errorf("PATCH tier_id = %v, want %d (current tier after the change)", got.TierID, tierB)
	}

	// The roster shows the current tier and its rate.
	page := decodeMembersPage(t, getMembers(t, r, ""))
	if len(page.Members) != 1 || page.Members[0].TierID == nil || *page.Members[0].TierID != tierB {
		t.Fatalf("roster = %+v, want one member in tier %d", page.Members, tierB)
	}

	// A dues-status row carries the tier at ITS period: February is still the old
	// tier at the old amount, and the current month is the new one.
	for _, tt := range []struct {
		period string
		tier   int64
		owed   int64
	}{{"2026-02", tierA, 10_000}, {now, tierB, 50_000}} {
		var rows []duesStatusResponse
		rec := getDuesStatus(t, r, tt.period)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/dues-status?period=%s = %d (body: %s)", tt.period, rec.Code, rec.Body.String())
		}
		if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].OwedAmount != tt.owed || rows[0].Member.TierID == nil || *rows[0].Member.TierID != tt.tier {
			t.Errorf("%s: rows = %+v, want one at tier %d owing %d", tt.period, rows, tt.tier, tt.owed)
		}
	}
}

func TestPatchMemberTierStartMonthIsRefusedOutsideItsBounds(t *testing.T) {
	t.Parallel()
	// The clock is pinned (mid-August 2026 in the fund's WIB), so the month after
	// the current one is a constant and not the runner's date.
	r := authedRouterAt(t, testStoreDB(t), fixedClock(time.Date(2026, 8, 15, 12, 0, 0, 0, wib)))
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d", rec.Code)
	}
	tier := mustTier(t, r, "Reguler", 10_000)
	joined := "2026-03-01"
	member := decodeMember(t, postMember(t, r, memberRequest{Name: "Jane", JoinedOn: &joined}), http.StatusCreated)

	const next = "2026-09"
	// The two bounds are #632's typed date error (ADR-040: "not a new code"); a
	// value that is not a month at all is still the generic invalid argument.
	for name, tc := range map[string]struct{ body, code string }{
		"before the join month":   {fmt.Sprintf(`{"tier_id":%d,"tier_effective_from":"2026-02"}`, tier), "date_out_of_range"},
		"after the current month": {fmt.Sprintf(`{"tier_id":%d,"tier_effective_from":%q}`, tier, next), "date_out_of_range"},
		"not a month":             {fmt.Sprintf(`{"tier_id":%d,"tier_effective_from":"soon"}`, tier), "invalid_argument"},
		"null":                    {fmt.Sprintf(`{"tier_id":%d,"tier_effective_from":null}`, tier), "invalid_argument"},
		"without a tier":          {`{"tier_effective_from":"2026-04"}`, "invalid_argument"},
	} {
		rec := patchMember(t, r, member.ID, tc.body)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != tc.code {
			t.Errorf("%s: PATCH = %d %s, want 400 %s", name, rec.Code, rec.Body.String(), tc.code)
		}
	}

	// Refused means nothing was written: the member is still without a tier.
	page := decodeMembersPage(t, getMembers(t, r, ""))
	if page.Members[0].TierID != nil {
		t.Errorf("tier_id = %v after only refused PATCHes, want nil", page.Members[0].TierID)
	}
}

func TestPatchMemberJoinDateBackdatedCarriesTheTierBackToTheNewJoinMonth(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d", rec.Code)
	}
	tier := mustTier(t, r, "Reguler", 10_000)
	joined := ledger.CurrentDuesPeriod(time.Now(), wib) + "-01"
	member := decodeMember(t, postMember(t, r, memberRequest{Name: "Jane", TierID: &tier, JoinedOn: &joined}), http.StatusCreated)

	decodeMember(t, patchMember(t, r, member.ID, `{"joined_on":"2026-01-01"}`), http.StatusOK)

	rec := getOutstandingDues(t, r, member.ID, "2026-02")
	var out []outstandingDuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Period != "2026-01" || out[0].OwedAmount != 10_000 {
		t.Errorf("outstanding = %+v, want January and February owed at 10000: live arrears reached the new join month", out)
	}
}

func TestDeleteTierNamedByAMemberTierRowIsRefusedEvenWhenSuperseded(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d", rec.Code)
	}
	slip := mustTier(t, r, "Salah", 10_000)
	right := mustTier(t, r, "Benar", 10_000)
	joined := "2026-01-01"
	member := decodeMember(t, postMember(t, r, memberRequest{Name: "Jane", TierID: &slip, JoinedOn: &joined}), http.StatusCreated)
	// Corrected: January is now "Benar"; "Salah" survives only as a superseded row.
	decodeMember(t, patchMember(t, r, member.ID, fmt.Sprintf(`{"tier_id":%d,"tier_effective_from":"2026-01"}`, right)), http.StatusOK)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/dues-tiers/%d", slip), nil))
	if rec.Code != http.StatusConflict || decodeError(t, rec).Code != "referenced_by_other_records" {
		t.Errorf("DELETE a tier only a superseded row names = %d %s, want 409 referenced_by_other_records", rec.Code, rec.Body.String())
	}
}
