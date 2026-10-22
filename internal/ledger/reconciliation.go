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
// line: the plug that squares a gap (resolution "adjusted") or the real,
// forgotten entry that explains it ("entry_added"). TakeReconciliation posts
// it as a new "transaction" row from these fields, inside its withTx, and
// never accepts a transaction id from the caller (ADR-024, ADR-027): a
// caller-supplied id could name an entry the cutoff had already counted,
// the false-record bug this rule exists to prevent.
//
// There is no AccountID field: the fix posts against the account of its
// enclosing AccountCount, which is derived rather than repeated.
type Fix struct {
	PurposeID  int64
	Direction  string       // "in" or "out"
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string
}

// AccountCount is one counted location within a reconciliation snapshot: an
// account, what the treasurer found there, and how any gap was resolved.
//
// Fix is nested here rather than in a second slice matched by account_id
// (the shape ADR-027's draft sketched). A fix squares exactly one line, and
// naming that line by position or a repeated account id is an alignment a
// caller could get wrong silently: fixes[2] meant for counts[2] would square
// counts[1] if a count were skipped or reordered. ADR-027's transfer leg
// removed the same failure by dropping the leg's Direction field. Nesting
// makes "this fix belongs to this line" a property of the Go value.
type AccountCount struct {
	AccountID    int64
	ActualAmount money.Amount // what the treasurer actually counted; must not be negative
	Resolution   string       // "matched", "left_open", "adjusted", "entry_added"

	// Fix carries the raw data behind the entry that resolves this line.
	// Required (non-nil) when Resolution is "adjusted" or "entry_added"; must be
	// nil for "matched" and "left_open".
	Fix *Fix
}

// TakeReconciliationParams is every argument TakeReconciliation needs to take
// one snapshot.
//
// There is no PerformedAt field: a reconciliation is done right now, in one
// sitting, with no reason to backdate it (unlike a transaction's OccurredOn).
// TakeReconciliation stamps PerformedAt and CreatedAt with the same
// time.Now().Unix(), as every write in this package stamps CreatedAt.
type TakeReconciliationParams struct {
	FundID int64
	Note   *string
	Counts []AccountCount

	// Now is the clock each fix's occurred_on ceiling is read from (ADR-041). It
	// dates nothing: PerformedAt is still stamped from the wall clock.
	// Zero means time.Now().
	Now time.Time

	// Idempotency, when non-nil, makes the snapshot safe to repeat (ADR-043).
	Idempotency *Idempotency
}

// TakeReconciliation takes one reconciliation snapshot: for each counted
// account, it freezes the recorded figure as of a single ledger cutoff,
// compares it to what the treasurer counted, and, where the resolution calls
// for one, posts the entry that resolves the gap. One call, one withTx, in
// the order ADR-024 and ADR-027 require:
//
//  1. Take the cutoff (MaxTransactionIDByFund). sql.ErrNoRows means an empty
//     ledger: through_transaction_id is stored NULL and every recorded_amount
//     is 0, without calling AccountBalanceThrough.
//  2. Read recorded_amount per counted account, through that cutoff.
//  3. Insert each fix as its own "transaction" row - kind='adjustment' for
//     "adjusted", kind='normal' for "entry_added" - from the caller's raw Fix
//     data, never a transaction id the caller supplies.
//  4. Insert the reconciliation row, then its reconciliation_line rows,
//     naming the fix ids where the resolution is "adjusted" or "entry_added"
//     (#498).
//
// The ordering is the point (ADR-024, ADR-027). A fix posted through an
// ordinary call before the cutoff was taken would be inside the cutoff,
// recorded_amount would equal actual_amount, and an "entry_added" line would
// store difference_amount = 0: schema-legal, since the CHECK verifies
// arithmetic and not history, and a permanent false record that no gap was
// found. Taking the cutoff first and posting fixes from raw data in the same
// call gives a fix an id above the cutoff, outside this snapshot's math and
// inside the next one's, by construction.
//
// difference_amount is computed in Go with the checked money.Amount.Sub,
// never in SQL; the schema's CHECK (difference_amount = actual_amount -
// recorded_amount) re-confirms the arithmetic rather than being its only
// home.
//
// The four resolutions:
//
//   - "matched": difference_amount must be 0. No fix. "Matched" against a
//     nonzero gap is ErrInvalidArgument: the schema has no CHECK tying
//     resolution to the difference, so a lying "matched" would be
//     schema-legal.
//   - "left_open": the figures differ and nothing is posted. The line is
//     saved as-is; revisiting it is a new snapshot (ADR-024), never an edit
//     (the immutability triggers forbid one).
//   - "adjusted": a plug (#498). Fix's signed amount (+ for "in", - for
//     "out") must equal the line's difference exactly, or the whole snapshot
//     is refused with ErrReconciliationPlugMismatch before anything is
//     posted: a plug that undershoots, overshoots or points the wrong way
//     would leave a known gap under a resolution claiming none. Fix's
//     transaction is posted (kind='adjustment') and the line names it in
//     adjustment_transaction_id, which the schema's CHECK also requires.
//   - "entry_added": Fix's transaction is posted (kind='normal') with its
//     real OccurredOn. It may close only part of the gap (the treasurer found
//     one missing entry, not necessarily all), so no amount is refused;
//     whatever is left (difference - signed(fix), the remainder_amount
//     ListReconciliationLines reads) keeps the line open. The line names the
//     entry in adjustment_transaction_id too (#498), so a read derives the
//     remainder from the ledger instead of storing a second figure. The id is
//     the row this call just posted, never from a caller.
//
// The count must be exactly the fund's active locations (PRD section 7.8:
// "each active location ... a retired location is not asked about"), checked
// first, inside the transaction, before the cutoff or any posting:
//
//   - A count naming a retired location (inactive_on set) is
//     ErrAccountInactive.
//   - A snapshot leaving out an active location is
//     ErrReconciliationMissingLocation. Only the full set can freeze a figure
//     meaning "the fund matches"; a subset would read cocok with the
//     discrepancy in the uncounted location.
//
// Both refuse the whole snapshot: no reconciliation row, no line, no fix.
//
// Argument-shape failures - an unrecognised resolution, "matched" with a
// nonzero difference, "adjusted"/"entry_added" missing a Fix, "left_open"
// naming one, a Fix with a non-positive amount, an unrecognised Fix
// direction, a malformed OccurredOn, the same account counted twice, a
// negative actual_amount - are ErrInvalidArgument before anything is written
// (ADR-027). A Fix dated outside the plausible window is ErrDateOutOfRange.
// Anything else, such as an account or purpose id of another fund, is a domain
// bug and surfaces wrapped generically.
//
// With p.Idempotency set (ADR-043), a repeat returns the first snapshot and
// takes none; the checks inside the transaction do not run on a replay. The
// lines are read with GetReconciliationDetail, as after a first success.
func (l *Ledger) TakeReconciliation(ctx context.Context, p TakeReconciliationParams) (store.Reconciliation, error) {
	window, err := l.windowFor(ctx, l.q, p.FundID, p.Now)
	if err != nil {
		return store.Reconciliation{}, err
	}
	if err := validateTakeReconciliationParams(p, window); err != nil {
		return store.Reconciliation{}, err
	}

	// lineInput is one line's numbers and the fix id (if any) it should name,
	// resolved while the cutoff is fixed, so the reconciliation row and lines
	// insert afterwards without recomputing.
	type lineInput struct {
		accountID                          int64
		recordedAmount, actualAmount, diff money.Amount
		resolution                         string
		adjustmentTransactionID            *int64
	}

	rec, err := withIdempotency(ctx, l, p.Idempotency, func(q store.Querier) (store.Reconciliation, error) {
		var rec store.Reconciliation
		if err := requireEveryActiveLocationCounted(ctx, q, p); err != nil {
			return store.Reconciliation{}, err
		}

		now := time.Now().Unix()

		cutoff, err := q.MaxTransactionIDByFund(ctx, p.FundID)
		emptyLedger := false
		switch {
		case err == nil:
			// cutoff holds the fund's highest transaction id; used below.
		case errors.Is(err, sql.ErrNoRows):
			emptyLedger = true
		default:
			return store.Reconciliation{}, fmt.Errorf("finding the reconciliation cutoff: %w", err)
		}

		lines := make([]lineInput, 0, len(p.Counts))
		for _, c := range p.Counts {
			var recordedAmount money.Amount
			if !emptyLedger {
				v, err := q.AccountBalanceThrough(ctx, store.AccountBalanceThroughParams{
					FundID: p.FundID, AccountID: c.AccountID, ID: cutoff,
				})
				if err != nil {
					return store.Reconciliation{}, fmt.Errorf("reading recorded amount for account %d: %w", c.AccountID, err)
				}
				recordedAmount = money.FromDB(v)
			}

			diff, err := c.ActualAmount.Sub(recordedAmount)
			if err != nil {
				return store.Reconciliation{}, fmt.Errorf("computing difference for account %d: %w", c.AccountID, err)
			}
			if c.Resolution == "matched" && diff != 0 {
				return store.Reconciliation{}, fmt.Errorf("%w: resolution \"matched\" for account %d has a difference of %d",
					ErrInvalidArgument, c.AccountID, diff.Int64())
			}

			var adjustmentTransactionID *int64
			if c.Fix != nil {
				// A plug must square the gap exactly (#498). Checked against the frozen
				// difference before the fix is posted, so a refusal leaves nothing behind.
				if c.Resolution == "adjusted" {
					signed, err := SignedFixAmount(c.Fix)
					if err != nil {
						return store.Reconciliation{}, fmt.Errorf("signing the fix for account %d: %w", c.AccountID, err)
					}
					if signed != diff {
						return store.Reconciliation{}, fmt.Errorf("%w: account %d has a difference of %d, the plug is %d",
							ErrReconciliationPlugMismatch, c.AccountID, diff.Int64(), signed.Int64())
					}
				}
				// A fix is a posting like any other (ADR-031, #473): it may not land on a
				// closed envelope.
				if err := refuseClosedIncidental(ctx, q, c.Fix.PurposeID, ErrIncidentalClosed); err != nil {
					return store.Reconciliation{}, err
				}
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
					Note:       normalizeNote(c.Fix.Note),
					CreatedAt:  now,
				})
				if err != nil {
					return store.Reconciliation{}, fmt.Errorf("posting the fix for account %d: %w", c.AccountID, err)
				}
				// Both resolutions name the entry they posted (#498); the remainder of a
				// partial entry_added is derived through it.
				adjustmentTransactionID = &fix.ID
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
			return store.Reconciliation{}, fmt.Errorf("creating reconciliation: %w", err)
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
				return store.Reconciliation{}, fmt.Errorf("creating reconciliation line for account %d: %w", ln.accountID, err)
			}
		}
		return rec, nil
	})
	if err != nil {
		return store.Reconciliation{}, err
	}
	return rec, nil
}

// SignedFixAmount is a fix's effect on its account's balance: positive for
// "in", negative for "out". Fix.Amount is validated positive before this is
// reached, so negation cannot overflow.
func SignedFixAmount(f *Fix) (money.Amount, error) {
	switch f.Direction {
	case "in":
		return f.Amount, nil
	case "out":
		return money.Amount(0).Sub(f.Amount)
	default:
		return 0, fmt.Errorf("%w: fix direction must be \"in\" or \"out\", got %q", ErrInvalidArgument, f.Direction)
	}
}

// ReconciliationDetail is one snapshot with the counted-account lines it
// froze, what GET /api/reconciliations/{id} shows (PRD section 7.8).
type ReconciliationDetail struct {
	Reconciliation store.Reconciliation
	Lines          []store.ListReconciliationLinesRow
}

// GetReconciliationDetail fetches one snapshot and its lines for GET
// /api/reconciliations/{id}.
//
// The snapshot fetch is fund-scoped (#105): an id names a row, it does not
// prove the caller may see it. The line fetch is not separately scoped:
// reconciliation_id suffices once the snapshot is known to be this fund's,
// since reconciliation_line's composite FK ties fund_id to reconciliation_id.
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

// requireEveryActiveLocationCounted is TakeReconciliation's location rule
// (#444), read inside its transaction so the fund's locations cannot change
// between the check and the write.
//
// A retired location is inactive_on non-NULL, whatever date it holds, as in
// PostPurposeMove and the SPA. An account id not this fund's (unknown, or
// another fund's) is skipped by both checks, and the completeness check
// stands down while one is present: the composite foreign key on
// reconciliation_line always refuses it, and that refusal (ADR-027) is the
// right answer, not "missing location".
func requireEveryActiveLocationCounted(ctx context.Context, q store.Querier, p TakeReconciliationParams) error {
	accounts, err := q.ListAccountsByFund(ctx, p.FundID)
	if err != nil {
		return fmt.Errorf("listing the fund's locations: %w", err)
	}
	byID := make(map[int64]store.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}

	counted := make(map[int64]bool, len(p.Counts))
	foreign := false
	for _, c := range p.Counts {
		a, ok := byID[c.AccountID]
		if !ok {
			foreign = true
			continue
		}
		if a.InactiveOn != nil {
			return ErrAccountInactive
		}
		counted[a.ID] = true
	}
	if foreign {
		return nil
	}

	for _, a := range accounts {
		if a.InactiveOn == nil && !counted[a.ID] {
			return ErrReconciliationMissingLocation
		}
	}
	return nil
}

// validateTakeReconciliationParams checks TakeReconciliation's inputs before
// anything is written: an unrecognised resolution, a Fix present or missing
// against the resolution, a non-positive or malformed Fix, a negative
// actual_amount, an empty Counts, or the same account named twice. None of
// this re-derives what the schema enforces (ADR-027): reconciliation_line's
// CHECKs verify arithmetic and the "adjusted names something" rule, but
// nothing stops a lying "matched" line, and a duplicate account would only
// fail UNIQUE (reconciliation_id, account_id) with a raw string.
//
// "matched" needing difference_amount == 0 is checked inside withTx, since it
// needs the frozen recorded_amount this function cannot read.
func validateTakeReconciliationParams(p TakeReconciliationParams, window dateWindow) error {
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
		if err := CheckAmountCeiling("actual_amount", c.ActualAmount); err != nil {
			return err
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
			if err := CheckAmountCeiling("fix amount", c.Fix.Amount); err != nil {
				return err
			}
			if c.Fix.Direction != "in" && c.Fix.Direction != "out" {
				return fmt.Errorf("%w: fix direction for account %d must be \"in\" or \"out\", got %q", ErrInvalidArgument, c.AccountID, c.Fix.Direction)
			}
			if err := validateOccurredOn("fix occurred_on", c.Fix.OccurredOn, window.entry()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unrecognised resolution %q for account %d", ErrInvalidArgument, c.Resolution, c.AccountID)
		}
	}
	return nil
}
