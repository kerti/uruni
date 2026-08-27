package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// Fix is the raw data behind the entry that resolves one reconciliation
// line - either the plug that squares a gap (resolution "adjusted") or the
// real, forgotten entry that explains it (resolution "entry_added").
// TakeReconciliation posts it as a new "transaction" row from these fields,
// inside its own withTx, and never accepts a transaction id from the caller
// (ADR-024, ADR-027): a caller-supplied id could name an entry the cutoff had
// already counted, which is exactly the false-record bug this slice exists to
// prevent.
//
// There is deliberately no AccountID field. The fix always posts against the
// account its enclosing AccountCount is about - see AccountCount's comment
// for why that is derived rather than repeated.
type Fix struct {
	PurposeID  int64
	Direction  string       // "in" or "out"
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string
}

// AccountCount is one counted location within a reconciliation snapshot: an
// account, what the treasurer actually found there, and how any gap was
// resolved.
//
// Fix is nested here rather than living in a second slice matched by
// account_id, which is the shape ADR-027's own draft paragraph sketched
// before this slice existed. A fix exists to square exactly one line, so
// naming that line through a separate collection - by position, or by
// repeating the account id - is exactly the kind of alignment a caller (and
// this function) could get wrong silently: a fixes[2] meant to resolve
// counts[2] would silently square counts[1] instead if a count were ever
// skipped or reordered. That is the same failure shape ADR-027's transfer
// leg already removed by construction rather than by validation, by
// dropping leg's own Direction field so postTransferPair derives it instead
// of trusting a caller to set it correctly. Nesting Fix inside AccountCount
// does the same thing here: "this fix belongs to this line" becomes a
// property of the Go value, not an alignment invariant.
type AccountCount struct {
	AccountID    int64
	ActualAmount money.Amount // what the treasurer actually counted; must not be negative
	Resolution   string       // "matched", "left_open", "adjusted", "entry_added"

	// Fix carries the raw data behind the entry that resolves this line.
	// Required (non-nil) when Resolution is "adjusted" or "entry_added";
	// must be nil for "matched" and "left_open".
	Fix *Fix
}

// TakeReconciliationParams is every argument TakeReconciliation needs to take
// one snapshot.
//
// There is no PerformedAt field: a reconciliation is something the treasurer
// does right now, in one sitting, with no PRD-described reason to backdate
// it - unlike OccurredOn on a transaction, which names a calendar day that
// can differ from when it was typed in. TakeReconciliation stamps both
// PerformedAt and CreatedAt with the same time.Now().Unix(), exactly how
// every other write method in this package stamps CreatedAt itself rather
// than accepting it as an argument.
type TakeReconciliationParams struct {
	FundID int64
	Note   *string
	Counts []AccountCount
}

// TakeReconciliation takes one reconciliation snapshot: for each counted
// account, it freezes the recorded figure as of a single ledger cutoff,
// compares it to what the treasurer actually counted, and - for a resolution
// that calls for one - posts the entry that resolves the gap. One call, one
// withTx, in the order ADR-024 and ADR-027 both require:
//
//  1. Take the cutoff (MaxTransactionIDByFund). sql.ErrNoRows means an empty
//     ledger: through_transaction_id is stored NULL and every recorded_amount
//     is 0, without calling AccountBalanceThrough at all.
//  2. Read recorded_amount per counted account, through that cutoff.
//  3. Insert each fix as its own "transaction" row - kind='adjustment' for
//     "adjusted", kind='normal' for "entry_added" - using the caller's raw
//     Fix data, never a transaction id the caller supplies.
//  4. Insert the reconciliation row, then its reconciliation_line rows,
//     naming the fix ids where the resolution is "adjusted".
//
// The ordering is the entire point (ADR-024, ADR-027). If a fix were posted
// through an ordinary call before the cutoff was taken, the cutoff would
// already include it, recorded_amount would equal actual_amount, and a line
// labelled "entry_added" would store difference_amount = 0 - schema-legal,
// since the CHECK verifies arithmetic and not history, and a permanent false
// record that no gap was ever found. Taking the cutoff first and posting
// fixes from raw data inside this same call is what makes that impossible:
// a fix's row always gets an id above the cutoff, so it lands outside this
// snapshot's own math and inside the next one's - by construction, not by a
// rule a caller could forget.
//
// difference_amount is computed in Go with the checked money.Amount.Sub,
// never in SQL; the schema's CHECK (difference_amount = actual_amount -
// recorded_amount) then re-confirms the same arithmetic the domain already
// did, rather than being the only place it happens.
//
// The four resolutions:
//
//   - "matched": difference_amount must be 0. No fix. A caller claiming
//     "matched" against a nonzero gap is ErrInvalidArgument - the schema has
//     no CHECK tying resolution to the sign or size of the difference, so
//     without this check a lying "matched" line would be schema-legal.
//   - "left_open": the figures differ and nothing is posted today. The line
//     is saved as-is; revisiting it later is a new snapshot; see
//     ADR-024, never an edit to this one - the immutability triggers make an
//     edit impossible even if this package wanted to write one.
//   - "adjusted": Fix's transaction is posted (kind='adjustment') and the
//     line names it in adjustment_transaction_id, which the schema's own
//     CHECK also requires.
//   - "entry_added": Fix's transaction is posted (kind='normal') with its
//     own real OccurredOn; adjustment_transaction_id stays NULL, because
//     that entry is self-explanatory in the ledger in a way a plug is not
//     (ADR-024). Nothing in this package ever sets
//     AdjustmentTransactionID for this resolution - it is not a caller
//     input, so there is nothing to validate against it.
//
// Argument-shape failures - an unrecognised resolution, "matched" with a
// nonzero difference, "adjusted"/"entry_added" missing a Fix, "left_open"
// naming one, a Fix with a non-positive amount, an unrecognised Fix
// direction, a malformed OccurredOn, the same account counted twice, or an
// empty actual_amount that is negative - are rejected as ErrInvalidArgument
// before anything is written (ADR-027). Everything else - an account or
// purpose id belonging to another fund - is a domain bug, not a caller
// mistake, and surfaces wrapped generically.
func (l *Ledger) TakeReconciliation(ctx context.Context, p TakeReconciliationParams) (store.Reconciliation, error) {
	if err := validateTakeReconciliationParams(p); err != nil {
		return store.Reconciliation{}, err
	}

	// lineInput is one line's numbers and the fix id (if any) it should name,
	// resolved while the cutoff is still fixed, so the reconciliation row and
	// its lines can be inserted afterwards without recomputing anything.
	type lineInput struct {
		accountID                          int64
		recordedAmount, actualAmount, diff money.Amount
		resolution                         string
		adjustmentTransactionID            *int64
	}

	var rec store.Reconciliation
	err := l.withTx(ctx, func(q store.Querier) error {
		now := time.Now().Unix()

		cutoff, err := q.MaxTransactionIDByFund(ctx, p.FundID)
		emptyLedger := false
		switch {
		case err == nil:
			// cutoff holds the fund's highest transaction id; used below.
		case errors.Is(err, sql.ErrNoRows):
			emptyLedger = true
		default:
			return fmt.Errorf("finding the reconciliation cutoff: %w", err)
		}

		lines := make([]lineInput, 0, len(p.Counts))
		for _, c := range p.Counts {
			var recordedAmount money.Amount
			if !emptyLedger {
				v, err := q.AccountBalanceThrough(ctx, store.AccountBalanceThroughParams{
					FundID: p.FundID, AccountID: c.AccountID, ID: cutoff,
				})
				if err != nil {
					return fmt.Errorf("reading recorded amount for account %d: %w", c.AccountID, err)
				}
				recordedAmount = money.FromDB(v)
			}

			diff, err := c.ActualAmount.Sub(recordedAmount)
			if err != nil {
				return fmt.Errorf("computing difference for account %d: %w", c.AccountID, err)
			}
			if c.Resolution == "matched" && diff != 0 {
				return fmt.Errorf("%w: resolution \"matched\" for account %d has a difference of %d",
					ErrInvalidArgument, c.AccountID, diff.Int64())
			}

			var adjustmentTransactionID *int64
			if c.Fix != nil {
				kind := "normal"
				if c.Resolution == "adjusted" {
					kind = "adjustment"
				}
				fix, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
					FundID:     p.FundID,
					AccountID:  c.AccountID,
					PurposeID:  c.Fix.PurposeID,
					Direction:  c.Fix.Direction,
					Amount:     c.Fix.Amount.Int64(),
					OccurredOn: c.Fix.OccurredOn,
					Kind:       kind,
					Note:       c.Fix.Note,
					CreatedAt:  now,
				})
				if err != nil {
					return fmt.Errorf("posting the fix for account %d: %w", c.AccountID, err)
				}
				// entry_added names nothing: that entry is self-explanatory
				// in the ledger in a way a plug is not (ADR-024).
				if c.Resolution == "adjusted" {
					adjustmentTransactionID = &fix.ID
				}
			}

			lines = append(lines, lineInput{
				accountID:               c.AccountID,
				recordedAmount:          recordedAmount,
				actualAmount:            c.ActualAmount,
				diff:                    diff,
				resolution:              c.Resolution,
				adjustmentTransactionID: adjustmentTransactionID,
			})
		}

		var cutoffPtr *int64
		if !emptyLedger {
			cutoffPtr = &cutoff
		}
		rec, err = q.CreateReconciliation(ctx, store.CreateReconciliationParams{
			FundID:               p.FundID,
			PerformedAt:          now,
			ThroughTransactionID: cutoffPtr,
			Note:                 p.Note,
			CreatedAt:            now,
		})
		if err != nil {
			return fmt.Errorf("creating reconciliation: %w", err)
		}

		for _, ln := range lines {
			if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
				FundID:                  p.FundID,
				ReconciliationID:        rec.ID,
				AccountID:               ln.accountID,
				RecordedAmount:          ln.recordedAmount.Int64(),
				ActualAmount:            ln.actualAmount.Int64(),
				DifferenceAmount:        ln.diff.Int64(),
				Resolution:              ln.resolution,
				AdjustmentTransactionID: ln.adjustmentTransactionID,
			}); err != nil {
				return fmt.Errorf("creating reconciliation line for account %d: %w", ln.accountID, err)
			}
		}
		return nil
	})
	if err != nil {
		return store.Reconciliation{}, err
	}
	return rec, nil
}

// ReconciliationDetail is one snapshot together with the counted-account lines
// it froze - what GET /api/reconciliations/{id} shows (PRD section 7.8),
// composed the same way GetIncidentalDetail composes an envelope with its
// totals.
type ReconciliationDetail struct {
	Reconciliation store.Reconciliation
	Lines          []store.ReconciliationLine
}

// GetReconciliationDetail fetches one snapshot and its lines for GET
// /api/reconciliations/{id}.
//
// The snapshot fetch is fund-scoped (GetReconciliation now takes fund_id
// alongside id, #105): an id names a row, it does not prove the caller may
// see it, the same reasoning GetIncidentalDetail's own comment gives. The line
// fetch that follows is not separately fund-scoped - reconciliation_id alone
// is enough once the snapshot itself is already known to belong to this fund,
// since a line can only ever be created against the reconciliation that owns
// it (reconciliation_line's own composite FK ties fund_id to
// reconciliation_id).
func (l *Ledger) GetReconciliationDetail(ctx context.Context, fundID, id int64) (ReconciliationDetail, error) {
	rec, err := l.q.GetReconciliation(ctx, store.GetReconciliationParams{ID: id, FundID: fundID})
	if err != nil {
		return ReconciliationDetail{}, fmt.Errorf("fetching reconciliation: %w", err)
	}

	lines, err := l.q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		return ReconciliationDetail{}, fmt.Errorf("listing reconciliation lines: %w", err)
	}

	return ReconciliationDetail{Reconciliation: rec, Lines: lines}, nil
}

// validateTakeReconciliationParams checks TakeReconciliation's own inputs
// before anything is written: an unrecognised resolution, a Fix present or
// missing where the resolution disagrees, a non-positive or malformed Fix,
// a negative actual_amount, an empty Counts, or the same account named
// twice. None of this re-derives an invariant the schema already enforces
// (ADR-027) - reconciliation_line's own CHECKs verify arithmetic and the
// "adjusted names something" rule, but nothing in the schema stops a lying
// "matched" line or a duplicate account (that is reconciliation_line's
// UNIQUE (reconciliation_id, account_id), which would reject a duplicate
// too, but only with a raw "UNIQUE constraint failed" string).
//
// "matched" needing difference_amount == 0 is checked separately, inside
// withTx, because computing it needs the frozen recorded_amount this
// function has no database access to read.
func validateTakeReconciliationParams(p TakeReconciliationParams) error {
	if len(p.Counts) == 0 {
		return fmt.Errorf("%w: counts must not be empty", ErrInvalidArgument)
	}

	seen := make(map[int64]bool, len(p.Counts))
	for _, c := range p.Counts {
		if seen[c.AccountID] {
			return fmt.Errorf("%w: account %d is counted more than once", ErrInvalidArgument, c.AccountID)
		}
		seen[c.AccountID] = true

		if c.ActualAmount < 0 {
			return fmt.Errorf("%w: actual_amount must not be negative, got %d", ErrInvalidArgument, c.ActualAmount.Int64())
		}

		switch c.Resolution {
		case "matched", "left_open":
			if c.Fix != nil {
				return fmt.Errorf("%w: resolution %q for account %d must not name a fix", ErrInvalidArgument, c.Resolution, c.AccountID)
			}
		case "adjusted", "entry_added":
			if c.Fix == nil {
				return fmt.Errorf("%w: resolution %q for account %d requires a fix", ErrInvalidArgument, c.Resolution, c.AccountID)
			}
			if c.Fix.Amount <= 0 {
				return fmt.Errorf("%w: fix amount for account %d must be positive, got %d", ErrInvalidArgument, c.AccountID, c.Fix.Amount.Int64())
			}
			if c.Fix.Direction != "in" && c.Fix.Direction != "out" {
				return fmt.Errorf("%w: fix direction for account %d must be \"in\" or \"out\", got %q", ErrInvalidArgument, c.AccountID, c.Fix.Direction)
			}
			if err := validateOccurredOn(c.Fix.OccurredOn); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unrecognised resolution %q for account %d", ErrInvalidArgument, c.Resolution, c.AccountID)
		}
	}
	return nil
}
