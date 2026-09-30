package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// OpenIncidentalParams is every argument OpenIncidental needs to open one
// envelope.
//
// There is deliberately no separate purpose-name field: Occasion becomes
// both the purpose row's name and the incidental row's occasion, because
// PRD 7.5's own language treats them as the same label ("create an
// incidental for an occasion").
type OpenIncidentalParams struct {
	FundID       int64
	Occasion     string        // non-empty; becomes purpose.name and incidental.occasion
	TargetAmount *money.Amount // nil = no target (the schema allows NULL); must be > 0 if set
	OpenedOn     string        // "YYYY-MM-DD", a real calendar date

	// MinimumPerMember is the one figure every expected member is asked to
	// give (ADR-034) - nil = no minimum; must be > 0 if set, the same shape
	// TargetAmount holds itself to.
	MinimumPerMember *money.Amount

	// RecipientMemberIDs are the members this envelope is for (ADR-034) -
	// never expected to contribute themselves. Zero or more; nil and an
	// empty slice both mean none.
	RecipientMemberIDs []int64
}

// OpenIncidental writes one purpose row (kind='incidental') and the
// incidental row that is 1:1 with it, inside one withTx, and returns the
// created incidental.
//
// The two inserts happen together on purpose. Opening an envelope is a
// purpose row and an incidental row in two different tables; nothing in the
// schema ties their creation together, so two separate store.Queries calls
// - the shape M4 would otherwise have reached for - leave a window where a
// crash between them strands an orphan purpose: a tag that exists, appears
// in purpose lists, accepts transactions, and has no occasion, target or
// opened_on behind it (see #42's comment). That is the same atomicity
// argument ADR-027 already makes for CloseIncidentalAndRoll, applied to the
// other end of the same lifecycle.
func (l *Ledger) OpenIncidental(ctx context.Context, p OpenIncidentalParams) (store.Incidental, error) {
	if strings.TrimSpace(p.Occasion) == "" {
		return store.Incidental{}, fmt.Errorf("%w: occasion must not be empty", ErrInvalidArgument)
	}
	if p.TargetAmount != nil && *p.TargetAmount <= 0 {
		return store.Incidental{}, fmt.Errorf("%w: target_amount must be positive when set, got %d", ErrInvalidArgument, p.TargetAmount.Int64())
	}
	if p.MinimumPerMember != nil && *p.MinimumPerMember <= 0 {
		return store.Incidental{}, fmt.Errorf("%w: minimum_per_member must be positive when set, got %d", ErrInvalidArgument, p.MinimumPerMember.Int64())
	}
	if err := validateOccurredOn(p.OpenedOn); err != nil {
		return store.Incidental{}, err
	}

	var created store.Incidental
	err := l.withTx(ctx, func(q store.Querier) error {
		now := time.Now().Unix()

		purpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
			FundID: p.FundID, Kind: "incidental", Name: p.Occasion, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating purpose: %w", err)
		}

		var targetAmount *int64
		if p.TargetAmount != nil {
			v := p.TargetAmount.Int64()
			targetAmount = &v
		}
		var minimumPerMember *int64
		if p.MinimumPerMember != nil {
			v := p.MinimumPerMember.Int64()
			minimumPerMember = &v
		}

		created, err = q.CreateIncidental(ctx, store.CreateIncidentalParams{
			PurposeID: purpose.ID, Occasion: p.Occasion, TargetAmount: targetAmount,
			OpenedOn: p.OpenedOn, MinimumPerMember: minimumPerMember, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating incidental: %w", err)
		}

		if err := putIncidentalRecipientsTx(ctx, q, p.FundID, purpose.ID, p.RecipientMemberIDs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return store.Incidental{}, fmt.Errorf("opening incidental: %w", err)
	}
	return created, nil
}

// putIncidentalRecipientsTx replaces purposeID's recipient set with
// memberIDs, inside the caller's already-open store.Querier (ADR-027's ...Tx
// composition rule). It never checks the envelope's own state - recipients
// are mutable like occasion, never a posted fact (ADR-034), so both
// OpenIncidental (nothing to delete yet) and SetIncidentalParticipation
// (replacing a prior set) share this one primitive rather than a second copy
// of the insert loop.
//
// No dedup and no pre-check against member: a repeated id or one belonging
// to another fund is a caller mistake, not a domain bug, and surfaces as an
// ordinary constraint violation the same way UpdateReimbursement's
// caller-supplied ids already do (ADR-027's Amendments) - the primary key on
// (fund_id, purpose_id, member_id) and the composite FK are the actual
// guarantees, not a re-derivation of either here.
func putIncidentalRecipientsTx(ctx context.Context, q store.Querier, fundID, purposeID int64, memberIDs []int64) error {
	if err := q.DeleteIncidentalRecipientsByPurpose(ctx, purposeID); err != nil {
		return fmt.Errorf("clearing incidental recipients: %w", err)
	}
	for _, memberID := range memberIDs {
		if err := q.CreateIncidentalRecipient(ctx, store.CreateIncidentalRecipientParams{
			FundID: fundID, PurposeID: purposeID, MemberID: memberID,
		}); err != nil {
			return fmt.Errorf("adding incidental recipient: %w", err)
		}
	}
	return nil
}

// RenameIncidentalParams is every argument RenameIncidental needs to correct
// one envelope's occasion.
type RenameIncidentalParams struct {
	FundID    int64
	PurposeID int64
	Occasion  string // non-empty; becomes purpose.name and incidental.occasion, same as OpenIncidentalParams.Occasion
}

// RenameIncidental corrects a mistyped occasion (#264). The occasion is
// stored twice - as purpose.name and as incidental.occasion - for the same
// reason OpenIncidentalParams' own comment gives: PRD 7.5 treats the two as
// one label, so a rename that moved only one of them would leave the pair
// half-corrected, the exact half-renamed-row shape OpenIncidental's own
// atomicity argument exists to prevent at creation time. Both updates run
// inside one withTx so a crash between them cannot strand that
// disagreement.
//
// This posts no ledger entry and moves no money: a posted transaction
// references a purpose by id, and nothing in the ledger reads the text, so
// correcting it rewrites no history - the same correction UpdatePurposeName's
// own comment already makes for a location or a titipan, extended here to
// the one other place the label happens to live twice.
//
// Fund-scoped through the same GetIncidental fetch every other single-
// envelope write in this file opens with (ReopenIncidental, CloseIncidentalAndRoll):
// an id names a row, it does not prove the caller may see it, so the fetch
// runs first and the two unscoped updates that follow only ever touch a
// purpose_id already proven to belong to FundID.
func (l *Ledger) RenameIncidental(ctx context.Context, p RenameIncidentalParams) (store.Incidental, error) {
	if strings.TrimSpace(p.Occasion) == "" {
		return store.Incidental{}, fmt.Errorf("%w: occasion must not be empty", ErrInvalidArgument)
	}

	var renamed store.Incidental
	err := l.withTx(ctx, func(q store.Querier) error {
		if _, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: p.PurposeID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching incidental: %w", err)
		}

		if _, err := q.UpdatePurposeName(ctx, store.UpdatePurposeNameParams{ID: p.PurposeID, Name: p.Occasion}); err != nil {
			return fmt.Errorf("renaming purpose: %w", err)
		}

		var err error
		renamed, err = q.UpdateIncidentalOccasion(ctx, store.UpdateIncidentalOccasionParams{
			PurposeID: p.PurposeID, Occasion: p.Occasion,
		})
		if err != nil {
			return fmt.Errorf("renaming incidental occasion: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.Incidental{}, fmt.Errorf("renaming incidental: %w", err)
	}
	return renamed, nil
}

// SetIncidentalParticipationParams is every argument SetIncidentalParticipation
// needs to replace one envelope's minimum and its recipients (ADR-034).
type SetIncidentalParticipationParams struct {
	FundID    int64
	PurposeID int64

	// MinimumPerMember nil clears the minimum; a set value must be > 0,
	// the same shape OpenIncidentalParams.MinimumPerMember holds itself to.
	MinimumPerMember *money.Amount

	// RecipientMemberIDs fully replaces the envelope's recipient set - not
	// an add/remove delta - the same "editable like occasion" shape
	// RenameIncidental gives the occasion itself. nil and an empty slice
	// both mean "no recipients".
	RecipientMemberIDs []int64
}

// SetIncidentalParticipation replaces one envelope's minimum_per_member and
// its incidental_recipient rows in one withTx (ADR-034). Both are mutable
// like occasion - nothing here is a posted fact, so neither half checks the
// envelope's own closed_on: a closed envelope's expectation can still be
// corrected the same way its occasion can (RenameIncidental carries no such
// check either), and only a *posting* is what ADR-031 refuses.
//
// Fund-scoped through the same GetIncidental fetch every other single-
// envelope write in this file opens with: an id names a row, it does not
// prove the caller may change it.
func (l *Ledger) SetIncidentalParticipation(ctx context.Context, p SetIncidentalParticipationParams) (store.Incidental, error) {
	if p.MinimumPerMember != nil && *p.MinimumPerMember <= 0 {
		return store.Incidental{}, fmt.Errorf("%w: minimum_per_member must be positive when set, got %d", ErrInvalidArgument, p.MinimumPerMember.Int64())
	}

	var updated store.Incidental
	err := l.withTx(ctx, func(q store.Querier) error {
		if _, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: p.PurposeID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching incidental: %w", err)
		}

		var minimumPerMember *int64
		if p.MinimumPerMember != nil {
			v := p.MinimumPerMember.Int64()
			minimumPerMember = &v
		}
		var err error
		updated, err = q.UpdateIncidentalMinimum(ctx, store.UpdateIncidentalMinimumParams{
			MinimumPerMember: minimumPerMember, PurposeID: p.PurposeID,
		})
		if err != nil {
			return fmt.Errorf("updating incidental minimum: %w", err)
		}

		if err := putIncidentalRecipientsTx(ctx, q, p.FundID, p.PurposeID, p.RecipientMemberIDs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return store.Incidental{}, fmt.Errorf("setting incidental participation: %w", err)
	}
	return updated, nil
}

// CloseIncidentalAndRollParams is every argument CloseIncidentalAndRoll
// needs to close one envelope and roll its leftover into the fund's main
// purpose.
type CloseIncidentalAndRollParams struct {
	FundID    int64
	PurposeID int64 // the incidental purpose closing

	// AccountID is the account the reclass_purpose pair posts through on
	// both legs. Its choice is immaterial to correctness - a same-account
	// pair always nets to zero on that account regardless of which
	// account the original contributions actually came in through
	// (ADR-027) - so it is a plain parameter with no derivation behind
	// it.
	AccountID int64

	ClosedOn string // "YYYY-MM-DD", a real calendar date

	// Note is written to both legs of the roll, or to neither - the same
	// contract PostTransferBetweenAccountsParams.Note describes. It is the
	// treasurer's own sentence, never generated here: the ledger writes no
	// note of its own. A roll with no typed note is not left unexplained
	// any more, though - it gets a display label built at read time from
	// the transfer's own facts (#257), same as every other system-created
	// row.
	Note *string
}

// CloseIncidentalAndRoll closes one envelope and squares its purpose balance
// to exactly zero, in whichever direction that takes - one call, one withTx,
// per ADR-027, rolling per ADR-031.
//
// The leftover is IncidentalTotals' collected minus disbursed
// (money.Amount.Sub, checked for overflow) - mathematically PurposeBalance
// for this purpose_id, since both sum the same signed ledger over the same
// rows. Three outcomes:
//
//   - Leftover > 0 (collected more than disbursed): postTransferPairTx
//     posts a reclass_purpose pair - same AccountID on both legs, from the
//     incidental purpose to the fund's main purpose - and closed_on is set
//     in the same transaction. Returns the leftover, positive: rolled out.
//   - Leftover < 0 (disbursed more than collected): the pair runs the other
//     way, from the fund's main purpose into the incidental purpose, for
//     the shortfall (-leftover, itself overflow-checked). Returns the
//     leftover, still negative: covered from Kas Utama. This is ADR-031's
//     supersession of ADR-027's original negative-leftover branch, which
//     closed and posted nothing - left uncovered, that shortfall would
//     leave the envelope permanently negative once a report ever shows a
//     purpose balance (ADR-031's consequences).
//   - Leftover == 0: closes the envelope and posts nothing - not an error,
//     since an error inside withTx would roll the close back, and there is
//     nothing to square. Returns 0.
//   - Already closed (closed_on already set): returns
//     ErrIncidentalAlreadyClosed and posts nothing. A second roll would
//     move money that already moved.
//
// Every outcome leaves PurposeBalance(purposeID) at exactly zero - the
// single invariant ADR-031 makes testable in place of the sign-dependent
// rule ADR-027 shipped.
func (l *Ledger) CloseIncidentalAndRoll(ctx context.Context, p CloseIncidentalAndRollParams) (money.Amount, error) {
	if err := validateOccurredOn(p.ClosedOn); err != nil {
		return 0, err
	}

	var rolled money.Amount
	err := l.withTx(ctx, func(q store.Querier) error {
		envelope, err := q.GetIncidental(ctx, store.GetIncidentalParams{
			PurposeID: p.PurposeID, FundID: p.FundID,
		})
		if err != nil {
			return fmt.Errorf("fetching incidental: %w", err)
		}
		if envelope.ClosedOn != nil {
			return ErrIncidentalAlreadyClosed
		}

		totals, err := q.IncidentalTotals(ctx, store.IncidentalTotalsParams{
			FundID: p.FundID, PurposeID: p.PurposeID,
		})
		if err != nil {
			return fmt.Errorf("computing incidental totals: %w", err)
		}

		leftover, err := money.FromDB(totals.CollectedAmount).Sub(money.FromDB(totals.DisbursedAmount))
		if err != nil {
			return fmt.Errorf("computing incidental leftover: %w", err)
		}

		switch {
		case leftover > 0:
			mainID, err := mainPurposeID(ctx, q, p.FundID)
			if err != nil {
				return err
			}
			from := leg{AccountID: p.AccountID, PurposeID: p.PurposeID}
			to := leg{AccountID: p.AccountID, PurposeID: mainID}
			if _, err := l.postTransferPairTx(ctx, q, p.FundID, "reclass_purpose", from, to, leftover, p.ClosedOn, normalizeNote(p.Note), nil); err != nil {
				return fmt.Errorf("rolling incidental leftover: %w", err)
			}
			rolled = leftover

		case leftover < 0:
			mainID, err := mainPurposeID(ctx, q, p.FundID)
			if err != nil {
				return err
			}
			// The shortfall, as a positive amount to post: 0 - leftover,
			// not a bare unary minus - money.Amount.Sub is the checked
			// negation this package uses everywhere else, and it is the
			// one that gets math.MinInt64 right (Sub's own doc comment).
			covering, err := money.Amount(0).Sub(leftover)
			if err != nil {
				return fmt.Errorf("computing incidental shortfall: %w", err)
			}
			from := leg{AccountID: p.AccountID, PurposeID: mainID}
			to := leg{AccountID: p.AccountID, PurposeID: p.PurposeID}
			if _, err := l.postTransferPairTx(ctx, q, p.FundID, "reclass_purpose", from, to, covering, p.ClosedOn, normalizeNote(p.Note), nil); err != nil {
				return fmt.Errorf("covering incidental shortfall: %w", err)
			}
			rolled = leftover // stays negative: covered from Kas Utama.
		}

		closedOn := p.ClosedOn
		if _, err := q.CloseIncidental(ctx, store.CloseIncidentalParams{
			ClosedOn: &closedOn, PurposeID: p.PurposeID,
		}); err != nil {
			return fmt.Errorf("closing incidental: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return rolled, nil
}

// IncidentalRecipient names one member an envelope is for (ADR-034) -
// never expected to contribute themselves (see GetIncidentalParticipation).
type IncidentalRecipient struct {
	MemberID   int64
	MemberName string
}

// IncidentalDetail is one envelope together with the totals PRD section 7.5 shows
// for it - what it has collected and disbursed so far, summed straight from
// the ledger (CLAUDE.md rule 2) rather than tracked as a running balance on
// the row itself - and the recipients it names (ADR-034).
type IncidentalDetail struct {
	Incidental store.Incidental
	Collected  money.Amount
	Disbursed  money.Amount
	Recipients []IncidentalRecipient
}

// GetIncidentalDetail fetches one envelope and its collected/disbursed
// totals for GET /api/incidentals/{purposeID}.
//
// Both halves are fund-scoped. An id names a row, it does not prove the
// caller may see it: PRD section 6 allows a server to hold more than one
// fund, so an unscoped read here would be a cross-fund read the moment a
// second fund exists. A purpose_id belonging to another fund is
// sql.ErrNoRows - indistinguishable from an unknown id, which is the answer
// it should get.
//
// IncidentalActivityTotals, not IncidentalTotals: this screen wants what the
// occasion itself collected and spent, not CloseIncidentalAndRoll's
// net-including-rolls figure. Conflating the two - showing the roll's own
// leg as if it were a fresh contribution or disbursement - was #215; see
// that query's own comment for the seam ADR-031 draws between them.
func (l *Ledger) GetIncidentalDetail(ctx context.Context, fundID, purposeID int64) (IncidentalDetail, error) {
	envelope, err := l.q.GetIncidental(ctx, store.GetIncidentalParams{
		PurposeID: purposeID, FundID: fundID,
	})
	if err != nil {
		return IncidentalDetail{}, fmt.Errorf("fetching incidental: %w", err)
	}

	totals, err := l.q.IncidentalActivityTotals(ctx, store.IncidentalActivityTotalsParams{
		FundID: fundID, PurposeID: purposeID,
	})
	if err != nil {
		return IncidentalDetail{}, fmt.Errorf("computing incidental activity totals: %w", err)
	}

	recipientRows, err := l.q.ListIncidentalRecipients(ctx, purposeID)
	if err != nil {
		return IncidentalDetail{}, fmt.Errorf("listing incidental recipients: %w", err)
	}
	recipients := make([]IncidentalRecipient, 0, len(recipientRows))
	for _, r := range recipientRows {
		recipients = append(recipients, IncidentalRecipient{MemberID: r.MemberID, MemberName: r.MemberName})
	}

	return IncidentalDetail{
		Incidental: envelope,
		Collected:  money.FromDB(totals.CollectedAmount),
		Disbursed:  money.FromDB(totals.DisbursedAmount),
		Recipients: recipients,
	}, nil
}

// ReopenIncidental sets a closed envelope's closed_on back to NULL
// (ADR-031) - the deliberate, visible way back that keeps the guard in
// PostTransaction from turning "attribution needs flexibility" into "the
// envelope is stuck". The treasurer reopens, posts the late entry through
// the ordinary record form, and closes again; CloseIncidentalAndRoll's
// unfiltered IncidentalTotals then rolls only the net delta, since the
// first roll's own leg already counts toward the sum.
//
// Fund-scoped through the same GetIncidental join every other single-
// envelope read and write in this file uses, for the reason GetIncidentalDetail's
// own comment gives: an id names a row, not permission to see or change it.
// ErrIncidentalNotClosed answers an envelope that is not closed - reopening
// one twice is refused, not a silent no-op.
func (l *Ledger) ReopenIncidental(ctx context.Context, fundID, purposeID int64) (store.Incidental, error) {
	var reopened store.Incidental
	err := l.withTx(ctx, func(q store.Querier) error {
		envelope, err := q.GetIncidental(ctx, store.GetIncidentalParams{
			PurposeID: purposeID, FundID: fundID,
		})
		if err != nil {
			return fmt.Errorf("fetching incidental: %w", err)
		}
		if envelope.ClosedOn == nil {
			return ErrIncidentalNotClosed
		}

		reopened, err = q.ReopenIncidental(ctx, purposeID)
		if err != nil {
			return fmt.Errorf("reopening incidental: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.Incidental{}, err
	}
	return reopened, nil
}

// mainPurposeID finds the fund's one kind='main' purpose. purpose_single_main
// guarantees exactly one exists per fund, so ListPurposesByFund - already
// scoped by fund_id - is enough; no dedicated query is added for a lookup
// this small.
func mainPurposeID(ctx context.Context, q store.Querier, fundID int64) (int64, error) {
	purposes, err := q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return 0, fmt.Errorf("listing purposes: %w", err)
	}
	for _, p := range purposes {
		if p.Kind == "main" {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("fund %d has no main purpose", fundID)
}
