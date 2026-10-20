package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// dues_rate is ADR-040's other append-only table: a correction number within the
// month, a deleting correction, a standing view, an UPDATE-only trigger. These
// tests hold the schema half; internal/ledger's hold the pricing reads.

func rateRow(tierID int64, from string, amount, correction, deleted int64) store.CreateDuesRateParams {
	return store.CreateDuesRateParams{TierID: tierID, Amount: amount, EffectiveFrom: from, Correction: correction, Deleted: deleted, CreatedAt: 1}
}

func TestDuesRateMonthHoldsOneRowPerCorrection(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	tierID := createDuesTier(t, sqlDB, createFund(t, sqlDB, "Test Fund", validSlug), "warga")

	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 50_000, 0, 0)); err != nil {
		t.Fatalf("first row = %v, want no error", err)
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 60_000, 0, 0)); err == nil {
		t.Error("the same tier, month and correction twice = nil error, want UNIQUE to reject it")
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 60_000, 1, 0)); err != nil {
		t.Errorf("the next correction of the month = %v, want no error", err)
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-02", 50_000, -1, 0)); err == nil {
		t.Error("a negative correction = nil error, want the CHECK to reject it")
	}
}

func TestDuesRateOnlyACorrectionCanDeleteAndTheFlagIsABoolean(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	tierID := createDuesTier(t, sqlDB, createFund(t, sqlDB, "Test Fund", validSlug), "warga")

	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 50_000, 0, 1)); err == nil {
		t.Error("a month's first row marked deleted = nil error, want CHECK (deleted = 0 OR correction > 0) to reject it")
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 50_000, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 50_000, 1, 1)); err != nil {
		t.Errorf("a deleting correction = %v, want no error", err)
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-02", 50_000, 1, 2)); err == nil {
		t.Error("deleted = 2 = nil error, want CHECK (deleted IN (0, 1)) to reject it")
	}
	// The amount stays NOT NULL and inside its CHECKs on a deleting correction too.
	if _, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-03", 1_000_000_000_000, 1, 1)); err == nil {
		t.Error("a deleting correction above the ceiling = nil error, want the amount CHECK to reject it")
	}
}

func TestDuesRateStandingViewKeepsTheHighestCorrectionAndDropsADeletedMonth(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")
	other := createDuesTier(t, sqlDB, createFund(t, sqlDB, "Other Fund", "bcdefghijklmnopqrstuvw"), "warga")

	// Written out of order on purpose: the standing row is the highest correction,
	// never the latest id or created_at.
	for _, r := range []store.CreateDuesRateParams{
		rateRow(tierID, "2026-01", 70_000, 2, 0),
		rateRow(tierID, "2026-01", 50_000, 0, 0),
		rateRow(tierID, "2026-01", 60_000, 1, 0),
		rateRow(tierID, "2026-06", 90_000, 0, 0), // filed...
		rateRow(tierID, "2026-06", 90_000, 1, 1), // ...and withdrawn
		rateRow(tierID, "2026-09", 80_000, 0, 0), // deleted, then re-added
		rateRow(tierID, "2026-09", 80_000, 1, 1),
		rateRow(tierID, "2026-09", 85_000, 2, 0),
		rateRow(other, "2026-01", 10_000, 0, 0),
	} {
		if _, err := q.CreateDuesRate(ctx, r); err != nil {
			t.Fatalf("CreateDuesRate(%s #%d) = %v, want no error", r.EffectiveFrom, r.Correction, err)
		}
	}

	rows, err := q.ListDuesRatesByTier(ctx, tierID)
	if err != nil {
		t.Fatalf("ListDuesRatesByTier = %v, want no error", err)
	}
	type standing struct {
		from       string
		amount     int64
		correction int64
	}
	var got []standing
	for _, r := range rows {
		got = append(got, standing{r.EffectiveFrom, r.Amount, r.Correction})
	}
	want := []standing{{"2026-01", 70_000, 2}, {"2026-09", 85_000, 2}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("standing rates = %+v, want %+v (one per month, the highest correction, a withdrawn month absent, oldest first)", got, want)
	}

	// The fallback: at June the withdrawn month has no rate, so January's stands;
	// dropping deleted rows before taking the highest would answer 90000.
	for period, wantAmount := range map[string]int64{"2026-05": 70_000, "2026-06": 70_000, "2026-08": 70_000, "2026-09": 85_000, "2027-01": 85_000} {
		r, err := q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{TierID: tierID, EffectiveFrom: period})
		if err != nil || r.Amount != wantAmount {
			t.Errorf("GetEffectiveDuesRate(%s) = %+v, %v, want %d", period, r, err, wantAmount)
		}
	}
	if _, err := q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{TierID: tierID, EffectiveFrom: "2025-12"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetEffectiveDuesRate before the first rate = %v, want sql.ErrNoRows", err)
	}

	// A tier whose only month was withdrawn has no rate at all.
	lone := createDuesTier(t, sqlDB, fundID, "madya")
	if _, err := q.CreateDuesRate(ctx, rateRow(lone, "2026-03", 5_000, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateDuesRate(ctx, rateRow(lone, "2026-03", 5_000, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{TierID: lone, EffectiveFrom: "2026-03"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetEffectiveDuesRate(a tier whose only rate was withdrawn) = %v, want sql.ErrNoRows", err)
	}

	export, err := q.ListDuesRatesByFund(ctx, fundID)
	if err != nil {
		t.Fatalf("ListDuesRatesByFund = %v, want no error", err)
	}
	if len(export) != 10 {
		t.Errorf("ListDuesRatesByFund returned %d rows, want all 10 of the fund's rows (8 of warga, 2 of madya; superseded and deleting ones are the trail)", len(export))
	}

	latest, err := q.LatestDuesRateOfMonth(ctx, store.LatestDuesRateOfMonthParams{TierID: tierID, EffectiveFrom: "2026-06"})
	if err != nil || latest.Correction != 1 || latest.Deleted != 1 {
		t.Errorf("LatestDuesRateOfMonth(June) = %+v, %v, want the deleting correction 1", latest, err)
	}
	if _, err := q.LatestDuesRateOfMonth(ctx, store.LatestDuesRateOfMonthParams{TierID: tierID, EffectiveFrom: "2026-11"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("LatestDuesRateOfMonth(a month with no row) = %v, want sql.ErrNoRows", err)
	}
}

func TestDuesRateRowsRefuseAnUpdate(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	tierID := createDuesTier(t, sqlDB, createFund(t, sqlDB, "Test Fund", validSlug), "warga")
	row, err := q.CreateDuesRate(ctx, rateRow(tierID, "2026-01", 50_000, 0, 0))
	if err != nil {
		t.Fatalf("CreateDuesRate = %v, want no error", err)
	}

	const want = "dues_rate rows are immutable"
	for _, stmt := range []string{
		`UPDATE dues_rate SET amount = 60000 WHERE id = ?`,
		`UPDATE dues_rate SET effective_from = '2026-02' WHERE id = ?`,
		`UPDATE dues_rate SET correction = 1 WHERE id = ?`,
		`UPDATE dues_rate SET correction = 1, deleted = 1 WHERE id = ?`,
	} {
		_, err := sqlDB.ExecContext(ctx, stmt, row.ID)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want an error containing %q", stmt, err, want)
		}
	}
}
