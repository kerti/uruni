package ledger

import (
	"context"
	"fmt"
	"strings"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// OpenIncidentalParams is every argument OpenIncidental needs to open one
// envelope.
//
// There is no separate purpose-name field: Occasion becomes both the purpose
// row's name and the incidental row's occasion, since PRD 7.5 treats them as
// one label.
type OpenIncidentalParams struct {
	FundID       int64
	Occasion     string        // non-empty; becomes purpose.name and incidental.occasion
	TargetAmount *money.Amount // nil = no target (the schema allows NULL); must be > 0 if set
	OpenedOn     string        // "YYYY-MM-DD", a real calendar date

	// MinimumPerMember is the one figure every expected member is asked to give
	// (ADR-034). nil means no minimum; if set it must be > 0, like TargetAmount.
	MinimumPerMember *money.Amount

	// RecipientMemberIDs are the members this envelope is for (ADR-034), never
	// expected to contribute. nil and an empty slice both mean none.
	RecipientMemberIDs []int64

	// Idempotency, when non-nil, makes opening the envelope safe to repeat
	// (ADR-043).
	Idempotency *Idempotency
}

// OpenIncidental writes one purpose row (kind='incidental') and the 1:1
// incidental row inside one withTx, and returns the created incidental.
//
// They go together because nothing in the schema ties the two inserts, and
// two separate store.Queries calls would let a crash between them strand an
// orphan purpose: a tag that appears in purpose lists and accepts
// transactions but has no occasion, target or opened_on (#42). Same
// atomicity argument as ADR-027 makes for CloseIncidentalAndRoll.
//
// With p.Idempotency set (ADR-043), a repeat returns the first envelope. A
// purpose has no uniqueness on its name and no route removes one, so an unkeyed
// repeat leaves a twin for good.
func (l *Ledger) OpenIncidental(ctx context.Context, p OpenIncidentalParams) (store.Incidental, error) {
	if strings.TrimSpace(p.Occasion) == "" {
		return store.Incidental{}, invalidField("occasion", "occasion must not be empty")
	}
	if p.TargetAmount != nil && *p.TargetAmount <= 0 {
		return store.Incidental{}, invalidField("target_amount", "target_amount must be positive when set, got %d", p.TargetAmount.Int64())
	}
	if p.MinimumPerMember != nil && *p.MinimumPerMember <= 0 {
		return store.Incidental{}, invalidField("minimum_per_member", "minimum_per_member must be positive when set, got %d", p.MinimumPerMember.Int64())
	}
	if err := checkParticipationCeilings(p.TargetAmount, p.MinimumPerMember); err != nil {
		return store.Incidental{}, err
	}
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return store.Incidental{}, err
	}
	if err := validateOccurredOn("opened_on", p.OpenedOn, window.entry()); err != nil {
		return store.Incidental{}, err
	}

	created, err := withIdempotency(ctx, l, p.Idempotency, func(ctx context.Context, q store.Querier) (store.Incidental, error) {
		now := l.now().Unix()

		purpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
			FundID: p.FundID, Kind: string(PurposeKindIncidental), Name: p.Occasion, CreatedAt: now,
		})
		if err != nil {
			return store.Incidental{}, fmt.Errorf("creating purpose: %w", err)
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

		incidental, err := q.CreateIncidental(ctx, store.CreateIncidentalParams{
			PurposeID: purpose.ID, Occasion: p.Occasion, TargetAmount: targetAmount,
			OpenedOn: p.OpenedOn, MinimumPerMember: minimumPerMember, CreatedAt: now,
		})
		if err != nil {
			return store.Incidental{}, fmt.Errorf("creating incidental: %w", err)
		}

		if err := putIncidentalRecipientsTx(ctx, q, p.FundID, purpose.ID, p.RecipientMemberIDs); err != nil {
			return store.Incidental{}, err
		}
		return incidental, nil
	})
	if err != nil {
		return store.Incidental{}, fmt.Errorf("opening incidental: %w", err)
	}
	return created, nil
}

// putIncidentalRecipientsTx replaces purposeID's recipient set with
// memberIDs inside the caller's open store.Querier (ADR-027's ...Tx
// composition rule). It never checks the envelope's state: recipients are
// mutable like occasion, never a posted fact (ADR-034), so OpenIncidental and
// SetIncidentalParticipation share this one primitive.
//
// No dedup and no pre-check against member: a repeated id or another fund's
// member is a caller mistake that surfaces as an ordinary constraint
// violation (ADR-027's Amendments); the primary key on (fund_id, purpose_id,
// member_id) and the composite FK are the guarantees.
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

// RenameIncidentalParams is every argument RenameIncidental needs.
type RenameIncidentalParams struct {
	FundID    int64
	PurposeID int64
	Occasion  string // non-empty; becomes purpose.name and incidental.occasion, same as OpenIncidentalParams.Occasion
}

// RenameIncidental corrects a mistyped occasion (#264). The occasion is stored
// twice, as purpose.name and incidental.occasion (PRD 7.5 treats them as one
// label), so a rename moving only one would leave the pair half-corrected.
// Both updates run inside one withTx.
//
// This posts no ledger entry and moves no money: a posted transaction
// references a purpose by id and nothing in the ledger reads the text, so no
// history is rewritten (as UpdatePurposeName does).
//
// Fund-scoped through the GetIncidental fetch every single-envelope write in
// this file opens with: an id names a row, it does not prove the caller may
// see it, so the two unscoped updates only touch a purpose_id proven to
// belong to FundID.
func (l *Ledger) RenameIncidental(ctx context.Context, p RenameIncidentalParams) (store.Incidental, error) {
	if strings.TrimSpace(p.Occasion) == "" {
		return store.Incidental{}, invalidField("occasion", "occasion must not be empty")
	}

	var renamed store.Incidental
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
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

// SetIncidentalParticipationParams is every argument
// SetIncidentalParticipation needs (ADR-034).
type SetIncidentalParticipationParams struct {
	FundID    int64
	PurposeID int64

	// TargetAmount nil clears the target; a set value must be > 0 (#381).
	TargetAmount *money.Amount

	// MinimumPerMember nil clears the minimum; a set value must be > 0.
	MinimumPerMember *money.Amount

	// RecipientMemberIDs fully replaces the recipient set, not a delta. nil and
	// an empty slice both mean no recipients.
	RecipientMemberIDs []int64
}

// SetIncidentalParticipation replaces one envelope's target_amount,
// minimum_per_member and incidental_recipient rows in one withTx (ADR-034,
// #381). All three are mutable like occasion; nothing here is a posted fact,
// so none checks closed_on: a closed envelope's expectation can still be
// corrected, and only a posting is what ADR-031 refuses.
//
// Fund-scoped through the GetIncidental fetch, as RenameIncidental is.
func (l *Ledger) SetIncidentalParticipation(ctx context.Context, p SetIncidentalParticipationParams) (store.Incidental, error) {
	if p.TargetAmount != nil && *p.TargetAmount <= 0 {
		return store.Incidental{}, invalidField("target_amount", "target_amount must be positive when set, got %d", p.TargetAmount.Int64())
	}
	if p.MinimumPerMember != nil && *p.MinimumPerMember <= 0 {
		return store.Incidental{}, invalidField("minimum_per_member", "minimum_per_member must be positive when set, got %d", p.MinimumPerMember.Int64())
	}
	if err := checkParticipationCeilings(p.TargetAmount, p.MinimumPerMember); err != nil {
		return store.Incidental{}, err
	}

	var updated store.Incidental
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: p.PurposeID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching incidental: %w", err)
		}

		var err error
		updated, err = q.UpdateIncidentalExpectations(ctx, store.UpdateIncidentalExpectationsParams{
			TargetAmount: amountToDB(p.TargetAmount), MinimumPerMember: amountToDB(p.MinimumPerMember), PurposeID: p.PurposeID,
		})
		if err != nil {
			return fmt.Errorf("updating incidental expectations: %w", err)
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

// CloseIncidentalAndRollParams is every argument CloseIncidentalAndRoll needs
// to close one envelope and roll its leftover into the main purpose.
type CloseIncidentalAndRollParams struct {
	FundID    int64
	PurposeID int64 // the incidental purpose closing

	// AccountID is the account the reclass_purpose pair posts through on both
	// legs. The choice is immaterial: a same-account pair nets to zero on that
	// account whatever account the contributions came in through (ADR-027).
	AccountID int64

	ClosedOn string // "YYYY-MM-DD", a real calendar date

	// Note is written to both legs of the roll, or neither, as
	// PostTransferBetweenAccountsParams.Note describes. It is the treasurer's
	// sentence; the ledger writes none. A roll with no note gets a display label
	// built at read time from the transfer's facts (#257).
	Note *string
}

// CloseIncidentalAndRoll closes one envelope and squares its purpose balance
// to exactly zero, in whichever direction that takes: one call, one withTx
// (ADR-027), rolling per ADR-031.
//
// The leftover is IncidentalTotals' collected minus disbursed
// (money.Amount.Sub, overflow-checked), mathematically PurposeBalance for
// this purpose_id. Outcomes:
//
//   - Leftover > 0: postTransferPairTx posts a reclass_purpose pair, same
//     AccountID on both legs, from the incidental purpose to the main
//     purpose, and closed_on is set in the same transaction. Returns the
//     leftover: rolled out.
//   - Leftover < 0 (disbursed more than collected): the pair runs the other
//     way, from the main purpose into the incidental purpose, for the
//     shortfall (-leftover, overflow-checked). Returns the leftover, still
//     negative: covered from Kas Utama. ADR-031 supersedes ADR-027's branch
//     that closed and posted nothing, which would leave the envelope
//     negative once a report shows purpose balances.
//   - Leftover == 0: closes the envelope and posts nothing. Not an error,
//     since an error inside withTx would roll the close back. Returns 0.
//   - Already closed: returns ErrIncidentalAlreadyClosed and posts nothing; a
//     second roll would move money that already moved.
//
// A retired AccountID is ErrAccountInactive when a leftover has to be posted
// on it (either direction); a zero leftover posts nothing and ignores the
// account (#444). Nothing is closed on refusal.
//
// Every outcome leaves PurposeBalance(purposeID) at exactly zero, the
// invariant ADR-031 makes testable in place of ADR-027's sign-dependent rule.
func (l *Ledger) CloseIncidentalAndRoll(ctx context.Context, p CloseIncidentalAndRollParams) (money.Amount, error) {
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return 0, err
	}
	if err := validateOccurredOn("closed_on", p.ClosedOn, window.entry()); err != nil {
		return 0, err
	}

	var rolled money.Amount
	reason := reasonRoll
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
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

		// A retired location is refused only when there is something to post on it.
		// A zero leftover touches no account, so a stale default must not block the
		// close (#444).
		if leftover != 0 {
			if err := refuseInactiveAccount(ctx, q, p.FundID, p.AccountID); err != nil {
				return err
			}
		}

		switch {
		case leftover > 0:
			mainID, err := mainPurposeID(ctx, q, p.FundID)
			if err != nil {
				return err
			}
			from := leg{AccountID: p.AccountID, PurposeID: p.PurposeID}
			to := leg{AccountID: p.AccountID, PurposeID: mainID}
			if _, err := l.postTransferPairTx(ctx, q, transferPair{
				FundID: p.FundID, Kind: TransferKindReclassPurpose, From: from, To: to,
				Amount: leftover, OccurredOn: p.ClosedOn, Note: normalizeNote(p.Note), Reason: &reason,
			}); err != nil {
				return fmt.Errorf("rolling incidental leftover: %w", err)
			}
			rolled = leftover

		case leftover < 0:
			mainID, err := mainPurposeID(ctx, q, p.FundID)
			if err != nil {
				return err
			}
			// The shortfall as a positive amount: 0 - leftover via the checked
			// money.Amount.Sub, which gets math.MinInt64 right, not a bare unary minus.
			covering, err := money.Amount(0).Sub(leftover)
			if err != nil {
				return fmt.Errorf("computing incidental shortfall: %w", err)
			}
			from := leg{AccountID: p.AccountID, PurposeID: mainID}
			to := leg{AccountID: p.AccountID, PurposeID: p.PurposeID}
			if _, err := l.postTransferPairTx(ctx, q, transferPair{
				FundID: p.FundID, Kind: TransferKindReclassPurpose, From: from, To: to,
				Amount: covering, OccurredOn: p.ClosedOn, Note: normalizeNote(p.Note), Reason: &reason,
			}); err != nil {
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

// IncidentalRecipient names one member an envelope is for (ADR-034), never
// expected to contribute (see GetIncidentalParticipation).
type IncidentalRecipient struct {
	MemberID   int64
	MemberName string
}

// IncidentalDetail is one envelope with the totals PRD section 7.5 shows -
// collected and disbursed so far, summed from the ledger (CLAUDE.md rule 2),
// not a running balance on the row - and its recipients (ADR-034).
type IncidentalDetail struct {
	Incidental store.Incidental
	Collected  money.Amount
	Disbursed  money.Amount

	// Balance is what the envelope holds now (PurposeBalance, rolls included).
	// It is not Collected minus Disbursed once an envelope has been closed and
	// reopened: those leave a prior roll's leg out (#215). Zero on a closed
	// envelope, by ADR-031's invariant.
	Balance money.Amount

	Recipients []IncidentalRecipient
}

// GetIncidentalDetail fetches one envelope and its collected/disbursed
// totals for GET /api/incidentals/{purposeID}.
//
// Both halves are fund-scoped: PRD section 6 allows more than one fund, so
// an unscoped read would cross funds. Another fund's purpose_id is
// sql.ErrNoRows, indistinguishable from an unknown id.
//
// IncidentalActivityTotals, not IncidentalTotals: this screen wants what the
// occasion itself collected and spent, not the net-including-rolls figure
// CloseIncidentalAndRoll uses. Showing the roll's leg as a fresh contribution
// or disbursement was #215; see that query for the seam ADR-031 draws.
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

	balance, err := l.PurposeBalance(ctx, fundID, purposeID)
	if err != nil {
		return IncidentalDetail{}, err
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
		Balance:    balance,
		Recipients: recipients,
	}, nil
}

// ReopenIncidental sets a closed envelope's closed_on back to NULL
// (ADR-031), the visible way back that keeps PostTransaction's guard from
// leaving an envelope stuck. The treasurer reopens, posts the late entry,
// and closes again; CloseIncidentalAndRoll's unfiltered IncidentalTotals
// then rolls only the net delta, since the first roll's leg already counts.
//
// Fund-scoped through the GetIncidental join, as GetIncidentalDetail is.
// ErrIncidentalNotClosed answers an envelope that is not closed; reopening
// twice is refused, not a silent no-op.
func (l *Ledger) ReopenIncidental(ctx context.Context, fundID, purposeID int64) (store.Incidental, error) {
	var reopened store.Incidental
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
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
// guarantees exactly one per fund, so the fund-scoped ListPurposesByFund is
// enough.
func mainPurposeID(ctx context.Context, q store.Querier, fundID int64) (int64, error) {
	purposes, err := q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return 0, fmt.Errorf("listing purposes: %w", err)
	}
	for _, p := range purposes {
		if p.Kind == string(PurposeKindMain) {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("fund %d has no main purpose", fundID)
}

// amountToDB is the nullable money column's write side: nil stays NULL, a
// set amount is its integer rupiah.
func amountToDB(a *money.Amount) *int64 {
	if a == nil {
		return nil
	}
	v := a.Int64()
	return &v
}
