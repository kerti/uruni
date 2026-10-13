package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// otherFundFixture is a second fund and one row of every kind a resolve* helper
// looks up, written through the store, not the API, which refuses a second fund
// by design (ErrFundAlreadyExists): why the unscoped lookups #188 found were
// never exploitable and never noticed.
type otherFundFixture struct {
	accountID int64
	memberID  int64
	tierID    int64
	rateID    int64
}

func setUpOtherFund(t *testing.T, sqlDB *sql.DB) otherFundFixture {
	t.Helper()
	q := store.New(sqlDB)
	ctx := context.Background()

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund(other) = %v, want no error", err)
	}
	account, err := q.CreateAccount(ctx, store.CreateAccountParams{
		FundID: fund.ID, Kind: "cash", Name: "Other Cash", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateAccount(other) = %v, want no error", err)
	}
	member, err := q.CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01",
		FundID: fund.ID, Name: "John", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(other) = %v, want no error", err)
	}
	tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{
		FundID: fund.ID, Name: "Other Tier", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateDuesTier(other) = %v, want no error", err)
	}
	rate, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
		TierID: tier.ID, Amount: 25_000, EffectiveFrom: "2026-01", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateDuesRate(other) = %v, want no error", err)
	}

	return otherFundFixture{
		accountID: account.ID,
		memberID:  member.ID,
		tierID:    tier.ID,
		rateID:    rate.ID,
	}
}

// TestRoutesOnAnotherFundsRowAre404 is #188's regression: every route reaching
// a row through a resolve* helper must read a real id of a second fund as
// not-found, as the lookup is fund-scoped in the query (GetMemberForFund and
// siblings), not found then rejected. This is the shape of #70 and ADR-030's
// "scoping stays defensive."
func TestRoutesOnAnotherFundsRowAre404(t *testing.T) {
	t.Parallel()
	for _, tc := range fundScopedRouteCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sqlDB := testStoreDB(t)
			r := authedRouterFor(t, sqlDB)
			setUpFund(t, r)
			other := setUpOtherFund(t, sqlDB)

			rec := tc.call(t, r, other)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s on another fund's row = %d, want %d (body: %s)",
					tc.name, rec.Code, http.StatusNotFound, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "not_found" {
				t.Errorf("error code = %q, want %q", got.Code, "not_found")
			}
		})
	}
}

// TestRoutesBeforeSetupAre404 is the other half of the scoping: the resolve*
// helpers resolve the fund themselves, so before setup there is no fund to
// scope to and each route answers resolveFund's "run setup" 404 rather than
// reaching a row.
func TestRoutesBeforeSetupAre404(t *testing.T) {
	t.Parallel()
	// Setup never runs, so no row exists for these ids: the fund check answers
	// before the lookup.
	noFund := otherFundFixture{accountID: 1, memberID: 1, tierID: 1, rateID: 1}

	for _, tc := range fundScopedRouteCases() {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)

			rec := tc.call(t, r, noFund)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s before setup = %d, want %d (body: %s)",
					tc.name, rec.Code, http.StatusNotFound, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "not_found" {
				t.Errorf("error code = %q, want %q", got.Code, "not_found")
			}
		})
	}
}

// fundScopedRouteCases is every route reaching a row through a resolve* helper,
// shared by the two tests above so the list cannot drift.
func fundScopedRouteCases() []struct {
	name string
	call func(t *testing.T, r http.Handler, other otherFundFixture) *httptest.ResponseRecorder
} {
	return []struct {
		name string
		call func(t *testing.T, r http.Handler, other otherFundFixture) *httptest.ResponseRecorder
	}{
		{"PATCH /api/members/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return patchMember(t, r, o.memberID, `{"name":"Renamed"}`)
		}},
		{"DELETE /api/members/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return deleteMember(t, r, o.memberID)
		}},
		{"PATCH /api/accounts/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return patchAccount(t, r, o.accountID, `{"name":"Renamed"}`)
		}},
		{"DELETE /api/accounts/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return deleteAccount(t, r, o.accountID)
		}},
		{"PATCH /api/dues-tiers/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return patchDuesTier(t, r, o.tierID, "Renamed")
		}},
		{"GET /api/dues-tiers/{id}/rates", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return getDuesRates(t, r, strconv.FormatInt(o.tierID, 10))
		}},
		{"POST /api/dues-tiers/{id}/rates", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return postDuesRate(t, r, o.tierID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-03"})
		}},
		{"PATCH /api/dues-rates/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return patchDuesRate(t, r, o.rateID, 30_000)
		}},
		{"DELETE /api/dues-rates/{id}", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return deleteDuesRate(t, r, o.rateID)
		}},
	}
}
