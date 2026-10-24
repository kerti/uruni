package ledger

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The effective pos (ADR-033) is computed twice, on purpose: in SQL, once,
// as ledger_entry.effective_purpose_id (#628), which the correction dialog and
// the next correction's "from" leg read; and in Go, by foldCorrections, which
// nets a row's corrections into the one move the public report draws. They are
// written differently (a count of entries minus exits per purpose, against a
// map of the same) and nothing but this test makes them answer alike.

// effectiveCoverage counts what the generated ledgers exercised, so a generator
// that stopped producing corrections cannot leave the property passing on
// nothing.
type effectiveCoverage struct {
	corrections   atomic.Int64 // purpose corrections posted
	repeated      atomic.Int64 // rows corrected at least twice
	repeatedOut   atomic.Int64 // 'out' rows corrected at least twice
	correctedBack atomic.Int64 // rows whose last correction returned them to the stored tag
	refusedLegs   atomic.Int64 // transfer legs a correction was refused for
	allocations   atomic.Int64 // purpose moves posted beside the corrections
}

// TestEffectivePurposeAgreesWithTheDisplayFold runs fixed-seed random sequences
// of postings, purpose moves and purpose corrections (some repeated on one row,
// some correcting a row back to where it began, some aimed at a transfer leg,
// which is refused) and holds, after every sequence:
//
//   - for every row, the SQL effective purpose - through both queries that read
//     it, the Riwayat list and EffectivePurposeForTransaction - equals the
//     destination foldCorrections gives the row, or its stored tag where the
//     fold has no move for it (never corrected, or corrected back);
//   - Riwayat still lists the stored tag, never the effective one (ADR-033);
//   - summing every non-correction row's signed amount by its effective purpose
//     gives PurposeBalance for each purpose, which sums stored tags over the
//     rows and both legs of every correction. The two agree because a correction
//     is the pair that carries the move, so counting its legs and also moving
//     the original would count it twice.
func TestEffectivePurposeAgreesWithTheDisplayFold(t *testing.T) {
	t.Parallel()
	var cov effectiveCoverage

	t.Run("seeds", func(t *testing.T) {
		for seed := int64(1); seed <= 40; seed++ {
			t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
				t.Parallel()
				checkEffectivePurposeSequence(t, seed, &cov)
			})
		}
	})

	// The group above has finished (t.Run returns once its parallel subtests do).
	for name, got := range map[string]int64{
		"corrections posted":                  cov.corrections.Load(),
		"rows corrected twice or more":        cov.repeated.Load(),
		"'out' rows corrected twice or more":  cov.repeatedOut.Load(),
		"rows corrected back to the stored":   cov.correctedBack.Load(),
		"corrections of a transfer leg tried": cov.refusedLegs.Load(),
		"purpose moves posted":                cov.allocations.Load(),
	} {
		if got == 0 {
			t.Errorf("the generated ledgers had no %s: the property was not exercised", name)
		}
	}
}

func checkEffectivePurposeSequence(t *testing.T, seed int64, cov *effectiveCoverage) {
	t.Helper()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // a fixed seed is the point: the same sequence every run, nothing secret
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	otherID := createPurpose(t, q, f.fundID, "pass_through", "Other pass-through")
	envelope := openTestIncidental(t, l, f.fundID, "Envelope", "2026-08-01").PurposeID

	purposes := []int64{f.mainID, f.passID, otherID, envelope}
	dates := []string{"2026-08-03", "2026-08-17", "2026-09-02", "2026-09-10"}

	// Money to move: the envelope and main hold enough for any purpose move.
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 900_000, "2026-08-01", nil)
	postEntry(t, l, f.fundID, f.cashID, envelope, "in", 400_000, "2026-08-02", nil)

	var posted []int64 // every row a correction may be aimed at
	corrected := map[int64][]int64{}
	storedAt := map[int64]int64{}
	directionOf := map[int64]string{}

	// Corrections are legitimately refused for some targets; anything else fails.
	expected := []error{ErrPurposeCorrectionNoop}

	for step := 0; step < 45; step++ {
		switch roll := rng.Intn(10); {
		case roll < 3 || len(posted) == 0: // a posting
			direction := DirectionIn
			if rng.Intn(2) == 0 {
				direction = DirectionOut
			}
			purposeID := purposes[rng.Intn(len(purposes))]
			amount := money.Amount((1 + rng.Intn(200)) * 500)
			row := postEntry(t, l, f.fundID, f.cashID, purposeID, direction, amount, dates[rng.Intn(len(dates))], nil)
			posted = append(posted, row.ID)
			storedAt[row.ID] = purposeID
			directionOf[row.ID] = string(direction)

		case roll < 4: // a purpose move: a reclass_purpose pair that corrects nothing
			from, to := purposes[rng.Intn(2)*3], purposes[rng.Intn(2)*3] // main or envelope
			if from == to {
				continue
			}
			_, err := l.at(onTheDay("2026-09-30")).PostPurposeMove(ctx, PostPurposeMoveParams{
				FundID: f.fundID, FromPurposeID: from, ToPurposeID: to, AccountID: f.cashID,
				Amount: money.Amount(1 + rng.Intn(50)*100), OccurredOn: dates[rng.Intn(len(dates))],
			})
			if err != nil {
				t.Fatalf("PostPurposeMove() = %v, want no error", err)
			}
			cov.allocations.Add(1)

		default: // a correction, mostly of a row already corrected
			target := posted[rng.Intn(len(posted))]
			if rng.Intn(3) > 0 && len(corrected) > 0 {
				// Map order is random, so pick from the sorted keys: the seed alone
				// decides the sequence.
				keys := slices.Sorted(maps.Keys(corrected))
				target = keys[rng.Intn(len(keys))]
			}
			to := purposes[rng.Intn(len(purposes))]
			_, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: target, PurposeID: to})
			switch {
			case err == nil:
				cov.corrections.Add(1)
				corrected[target] = append(corrected[target], to)
			case isOneOf(err, expected):
			default:
				t.Fatalf("PostPurposeCorrection(row %d -> %d) = %v, want no error or a no-op", target, to, err)
			}
		}
	}

	// A leg of a pair is half a movement, and refusing it is what keeps the
	// effective purpose of a leg equal to its stored one.
	legRows, err := q.ListTransactionsPage(ctx, store.ListTransactionsPageParams{FundID: f.fundID, PageLimit: 1_000_000})
	if err != nil {
		t.Fatalf("ListTransactionsPage() = %v", err)
	}
	for _, r := range legRows {
		if r.Kind != "transfer" {
			continue
		}
		_, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: r.ID, PurposeID: f.passID})
		if !errors.Is(err, ErrPurposeCorrectionTransfer) {
			t.Fatalf("correcting transfer leg %d = %v, want ErrPurposeCorrectionTransfer", r.ID, err)
		}
		cov.refusedLegs.Add(1)
	}

	for id, path := range corrected {
		if len(path) >= 2 {
			cov.repeated.Add(1)
			if directionOf[id] == "out" {
				cov.repeatedOut.Add(1)
			}
		}
		if path[len(path)-1] == storedAt[id] {
			cov.correctedBack.Add(1)
		}
	}

	assertEffectivePurposeMatchesTheFold(t, q, l, f.fundID, purposes, storedAt)
}

func assertEffectivePurposeMatchesTheFold(t *testing.T, q *store.Queries, l *Ledger, fundID int64, purposes []int64, storedAt map[int64]int64) {
	t.Helper()
	ctx := context.Background()

	rows, err := q.ListTransactionsPage(ctx, store.ListTransactionsPageParams{FundID: fundID, PageLimit: 1_000_000})
	if err != nil {
		t.Fatalf("ListTransactionsPage() = %v", err)
	}
	report, err := q.ListReportTransactions(ctx, store.ListReportTransactionsParams{FundID: fundID, FromDate: "1900-01-01"})
	if err != nil {
		t.Fatalf("ListReportTransactions() = %v", err)
	}
	folded, err := foldCorrections(report)
	if err != nil {
		t.Fatalf("foldCorrections() = %v, want the corrections to form paths", err)
	}

	// The fold states a move as what happened to the row (#654), so its To is the
	// row's destination whichever way the row went.
	destination := func(r store.ListTransactionsPageRow) int64 {
		if m, ok := folded[r.ID]; ok {
			return m.ToPurposeID
		}
		return r.PurposeID
	}

	byEffective := map[int64]int64{}
	for _, r := range rows {
		if want, ok := storedAt[r.ID]; ok && r.PurposeID != want {
			t.Errorf("row %d: Riwayat lists purpose %d, want the stored tag %d", r.ID, r.PurposeID, want)
		}

		want := destination(r)
		if r.EffectivePurposeID != want {
			t.Errorf("row %d (%s, stored %d): ListTransactionsPage effective purpose = %d, the display fold says %d",
				r.ID, r.Direction, r.PurposeID, r.EffectivePurposeID, want)
		}
		single, err := q.EffectivePurposeForTransaction(ctx, store.EffectivePurposeForTransactionParams{FundID: fundID, ID: r.ID})
		if err != nil {
			t.Fatalf("EffectivePurposeForTransaction(%d) = %v", r.ID, err)
		}
		if single != want {
			t.Errorf("row %d (%s, stored %d): EffectivePurposeForTransaction = %d, the display fold says %d",
				r.ID, r.Direction, r.PurposeID, single, want)
		}

		// A correction's two legs carry the move; the original is what they move.
		if r.TransferCorrectsTransactionID != nil {
			continue
		}
		signed := r.Amount
		if r.Direction == "out" {
			signed = -signed
		}
		byEffective[r.EffectivePurposeID] += signed
	}

	for _, p := range purposes {
		balance, err := l.PurposeBalance(ctx, fundID, p)
		if err != nil {
			t.Fatalf("PurposeBalance(%d) = %v", p, err)
		}
		if got := byEffective[p]; got != balance.Int64() {
			t.Errorf("purpose %d: rows summed by effective purpose = %d, PurposeBalance = %d", p, got, balance.Int64())
		}
	}
}

func isOneOf(err error, set []error) bool {
	for _, e := range set {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
