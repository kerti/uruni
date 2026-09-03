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

// otherFundFixture is a second fund and one row of every kind a resolve*
// helper looks up, written straight through the store rather than the API -
// the API refuses a second fund by design (ErrFundAlreadyExists), which is
// exactly why the unscoped lookups #188 found were never exploitable and
// never noticed either.
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
	member, err := q.CreateMember(ctx, store.CreateMemberParams{
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

// TestRoutesOnAnotherFundsRowAre404 is #188's regression: every route that
// reaches a row through a resolve* helper must read a real id belonging to a
// second fund as not-found, because the lookup is fund-scoped in the query
// (GetMemberForFund and its siblings) rather than found first and rejected
// afterwards. This is the shape #70 established and ADR-030 leans on when it
// defers multi-fund - "scoping stays defensive."
func TestRoutesOnAnotherFundsRowAre404(t *testing.T) {
	cases := []struct {
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
		{"POST /api/accounts/{id}/opening-balance", func(t *testing.T, r http.Handler, o otherFundFixture) *httptest.ResponseRecorder {
			return postOpeningBalance(t, r, o.accountID, postOpeningBalanceRequest{
				Amount: 100_000, OccurredOn: "2026-01-05",
			})
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
