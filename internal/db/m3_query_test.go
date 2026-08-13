package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// The five queries M3's domain layer (internal/ledger) reads, exercised
// directly against the real schema before any ledger code depends on them
// (issue #38). Each is scoped by fund_id even where a narrower key would
// already be unique, per ADR-024.

func TestMaxTransactionIDByFundIsNoRowsOnAnEmptyFund(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newScenarioFund(t, sqlDB, "Neighborhood Fund", validSlug)

	// This is the whole reason the query is ORDER BY ... LIMIT 1 rather than
	// an aggregate: a fund's first-ever reconciliation must see a clean
	// sql.ErrNoRows, not a NULL-scan error (ADR-024, issue #38).
	_, err := store.New(sqlDB).MaxTransactionIDByFund(context.Background(), f.fundID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("MaxTransactionIDByFund on an empty fund = %v, want sql.ErrNoRows", err)
	}
}

func TestMaxTransactionIDByFundIsTheHighestIDInTheFund(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Neighborhood Fund", validSlug)

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 100_000,
		OccurredOn: "2026-08-01", Kind: "opening",
	})
	last := f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 50_000,
		OccurredOn: "2026-08-02", Kind: "normal",
	})

	// A second fund with a higher-numbered row of its own - scoping proof, not
	// just a happy path. If the query forgot fund_id it would return this row
	// instead of the first fund's.
	g := newScenarioFund(t, sqlDB, "Community Fund", "zyxwvutsrqponmlkjihgfe")
	g.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: g.cashID, PurposeID: g.mainID, Direction: "in", Amount: 999_000,
		OccurredOn: "2026-08-03", Kind: "opening",
	})

	got, err := q.MaxTransactionIDByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("MaxTransactionIDByFund = %v, want no error", err)
	}
	if got != last {
		t.Errorf("MaxTransactionIDByFund = %d, want %d - fund B's higher-id row must not leak in", got, last)
	}
}

func TestIncidentalTotalsSeparatesCollectedFromDisbursed(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Neighborhood Fund", validSlug)
	kurbanID := createPurpose(t, sqlDB, f.fundID, "incidental", "Lent 2026")

	// Nothing posted yet: both figures are zero, not an error.
	totals, err := q.IncidentalTotals(ctx, store.IncidentalTotalsParams{
		FundID: f.fundID, PurposeID: kurbanID,
	})
	if err != nil {
		t.Fatalf("IncidentalTotals before any postings = %v, want no error", err)
	}
	if totals.CollectedAmount != 0 || totals.DisbursedAmount != 0 {
		t.Errorf("IncidentalTotals before any postings = %+v, want both zero", totals)
	}

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: kurbanID, Direction: "in", Amount: 3_200_000,
		OccurredOn: "2026-05-20", Kind: "normal",
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: kurbanID, Direction: "out", Amount: 3_000_000,
		OccurredOn: "2026-06-05", Kind: "normal",
	})

	totals, err = q.IncidentalTotals(ctx, store.IncidentalTotalsParams{
		FundID: f.fundID, PurposeID: kurbanID,
	})
	if err != nil {
		t.Fatalf("IncidentalTotals = %v, want no error", err)
	}
	if totals.CollectedAmount != 3_200_000 {
		t.Errorf("collected = %d, want 3200000", totals.CollectedAmount)
	}
	if totals.DisbursedAmount != 3_000_000 {
		t.Errorf("disbursed = %d, want 3000000", totals.DisbursedAmount)
	}
}

func TestDuesPaidByPeriodCoversFullPartialAndUnpaidMembers(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Neighborhood Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, f.fundID, "permanent resident")
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
		TierID: tierID, Amount: 25_000, EffectiveFrom: "2026-01", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateDuesRate = %v, want no error", err)
	}
	period := "2026-08"

	jane, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: f.fundID, Name: "Jane Doe", TierID: &tierID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(Jane Doe) = %v, want no error", err)
	}
	john, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: f.fundID, Name: "John Doe", TierID: &tierID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(John Doe) = %v, want no error", err)
	}
	jack, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: f.fundID, Name: "Jack Doe", TierID: &tierID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(Jack Doe) = %v, want no error", err)
	}

	// Jane Doe paid in full, John Doe paid part of it, Jack Doe has not paid at
	// all - and so has no row in the result at all, since GROUP BY member_id
	// only sees members who posted a dues row for this period.
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 25_000,
		OccurredOn: "2026-08-05", Kind: "dues", MemberID: &jane.ID, DuesPeriod: &period,
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 10_000,
		OccurredOn: "2026-08-05", Kind: "dues", MemberID: &john.ID, DuesPeriod: &period,
	})

	rows, err := q.DuesPaidByPeriod(ctx, store.DuesPaidByPeriodParams{
		FundID: f.fundID, DuesPeriod: &period,
	})
	if err != nil {
		t.Fatalf("DuesPaidByPeriod = %v, want no error", err)
	}

	paid := map[int64]int64{}
	for _, r := range rows {
		if r.MemberID == nil {
			t.Fatalf("DuesPaidByPeriod row with nil member_id, want kind='dues' to guarantee non-NULL")
		}
		paid[*r.MemberID] = r.PaidAmount
	}
	if len(paid) != 2 {
		t.Fatalf("DuesPaidByPeriod rows = %d, want 2 (Jack Doe never paid, so no row)", len(paid))
	}
	if paid[jane.ID] != 25_000 {
		t.Errorf("Jane Doe paid = %d, want 25000 (paid in full)", paid[jane.ID])
	}
	if paid[john.ID] != 10_000 {
		t.Errorf("John Doe paid = %d, want 10000 (partial)", paid[john.ID])
	}
	if _, ok := paid[jack.ID]; ok {
		t.Errorf("Jack Doe has a row = %v, want none - he never paid this period", paid[jack.ID])
	}
}

func TestLatestDuesPeriodPaidByMemberIsTheChronologicalMax(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Neighborhood Fund", validSlug)
	memberID := createMember(t, sqlDB, f.fundID, "Jane Doe")

	// Posted out of order, so a lexicographic MAX genuinely has to sort them,
	// not just echo insertion order.
	for _, period := range []string{"2026-08", "2026-06", "2026-12", "2026-07"} {
		p := period
		f.entry(t, sqlDB, store.CreateTransactionParams{
			AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 25_000,
			OccurredOn: "2026-08-12", Kind: "dues", MemberID: &memberID, DuesPeriod: &p,
		})
	}

	rows, err := q.LatestDuesPeriodPaidByMember(ctx, f.fundID)
	if err != nil {
		t.Fatalf("LatestDuesPeriodPaidByMember = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Fatalf("LatestDuesPeriodPaidByMember rows = %d, want 1", len(rows))
	}
	if rows[0].MemberID == nil || *rows[0].MemberID != memberID {
		t.Fatalf("LatestDuesPeriodPaidByMember member_id = %v, want %d", rows[0].MemberID, memberID)
	}

	// The type assertion is the test: LatestPeriod must already be a plain
	// string (the CAST(... AS TEXT) fix), not something the caller has to
	// type-assert out of interface{}.
	latest := rows[0].LatestPeriod
	if latest != "2026-12" {
		t.Errorf("latest period = %q, want 2026-12 - the chronological max, not the last one inserted", latest)
	}
}

func TestGetReimbursementSettlement(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Neighborhood Fund", validSlug)
	memberID := createMember(t, sqlDB, f.fundID, "Jane Doe")

	unsettled := createReimbursement(t, sqlDB, f.fundID, memberID, f.mainID, 150_000)
	if _, err := q.GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
		FundID: f.fundID, ReimbursementID: &unsettled,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetReimbursementSettlement on an unsettled claim = %v, want sql.ErrNoRows", err)
	}

	settled := createReimbursement(t, sqlDB, f.fundID, memberID, f.mainID, 80_000)
	settlementID := f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 80_000,
		OccurredOn: "2026-08-15", Kind: "reimbursement", ReimbursementID: &settled,
	})

	row, err := q.GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
		FundID: f.fundID, ReimbursementID: &settled,
	})
	if err != nil {
		t.Fatalf("GetReimbursementSettlement on a settled claim = %v, want no error", err)
	}
	if row.ID != settlementID {
		t.Errorf("GetReimbursementSettlement id = %d, want %d - the settling transaction itself", row.ID, settlementID)
	}
	if row.Amount != 80_000 {
		t.Errorf("GetReimbursementSettlement amount = %d, want 80000", row.Amount)
	}
}

func TestGetReimbursementSettlementDoesNotSeeAnotherFundsClaim(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	a := newScenarioFund(t, sqlDB, "Fund A", validSlug)
	b := newScenarioFund(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")
	memberB := createMember(t, sqlDB, b.fundID, "Jane Doe")

	claimB := createReimbursement(t, sqlDB, b.fundID, memberB, b.mainID, 80_000)
	b.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: b.cashID, PurposeID: b.mainID, Direction: "out", Amount: 80_000,
		OccurredOn: "2026-08-15", Kind: "reimbursement", ReimbursementID: &claimB,
	})

	// The claim id exists and is settled - but in fund B, not fund A. Without
	// fund_id scoping this would wrongly return fund B's settlement.
	if _, err := q.GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
		FundID: a.fundID, ReimbursementID: &claimB,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetReimbursementSettlement across funds = %v, want sql.ErrNoRows", err)
	}
}
