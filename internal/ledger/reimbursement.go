package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// CreateReimbursementParams is every argument CreateReimbursement needs to
// file one claim: a member fronted Amount on IncurredOn, for PurposeID.
type CreateReimbursementParams struct {
	FundID     int64
	MemberID   int64
	PurposeID  int64
	Amount     money.Amount // must be > 0
	IncurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string
}

// CreateReimbursement files a claim. It posts nothing (an unsettled claim is
// off the ledger, ADR-024), but a claim against a closed envelope can only
// end in a refused payout, so it is refused with ErrIncidentalClosed
// (ADR-031): reopen the envelope first. An incurred_on that is not a real date
// is ErrInvalidArgument, one outside the plausible window ErrDateOutOfRange;
// every other rule (a member or purpose naming no row) is the schema's to
// refuse.
func (l *Ledger) CreateReimbursement(ctx context.Context, p CreateReimbursementParams) (store.Reimbursement, error) {
	if p.Amount <= 0 {
		return store.Reimbursement{}, invalidField("amount", "amount must be positive, got %d", p.Amount.Int64())
	}
	if err := CheckAmountCeiling("amount", p.Amount); err != nil {
		return store.Reimbursement{}, err
	}
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return store.Reimbursement{}, err
	}
	if err := validateOccurredOn("incurred_on", p.IncurredOn, window.entry()); err != nil {
		return store.Reimbursement{}, err
	}

	var created store.Reimbursement
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if err := refuseClosedIncidental(ctx, q, p.PurposeID, ErrIncidentalClosed); err != nil {
			return err
		}
		var err error
		created, err = q.CreateReimbursement(ctx, store.CreateReimbursementParams{
			FundID: p.FundID, MemberID: p.MemberID, PurposeID: p.PurposeID,
			Amount: p.Amount.Int64(), IncurredOn: p.IncurredOn, Note: p.Note,
			CreatedAt: l.now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Reimbursement{}, fmt.Errorf("creating reimbursement: %w", err)
	}
	return created, nil
}

// SettleReimbursementParams is every argument SettleReimbursement needs to pay
// out one outstanding claim.
//
// There is no Amount or PurposeID field: both come from the reimbursement row,
// never the caller. A settlement paying a different figure would not settle
// that claim.
type SettleReimbursementParams struct {
	FundID          int64
	ReimbursementID int64
	AccountID       int64  // which account pays the claim out
	OccurredOn      string // "YYYY-MM-DD", a real calendar date - the settle date, not incurred_on
}

// SettleReimbursement writes one kind='reimbursement', direction='out' row
// paying out a claim that has been off the ledger, and returns the created
// row.
//
// A member fronting money does not move the kas, so there is no ledger row
// until this call; that is why reimbursement is its own table and the
// recorded balance still matches the wallet in between (ADR-024). It posts
// exactly one row: the payout, dated on the settle date (OccurredOn), not
// incurred_on, which stays on the reimbursement row as when the member spent
// the money.
//
// Two pre-checks run in the transaction before the write, each giving a named
// error rather than closing a race: under ADR-004's SetMaxOpenConns(1) no
// second connection can interleave a write, so neither is a lock or closes a
// window the schema does not already close (as in
// ErrAlreadyReversed).
//
//   - If the claim has WaivedOn set, ErrReimbursementWaived: a claim that
//     will never be repaid cannot be settled.
//   - If GetReimbursementSettlement finds a kind='reimbursement' row for this
//     claim, ErrReimbursementAlreadySettled. The reimbursement_settled_once
//     partial unique index is the guarantee, even for a caller inserting
//     through raw store.Queries; the pre-check only gives a clean error
//     instead of a raw "UNIQUE constraint failed".
//
// A retired AccountID (inactive_on set) is ErrAccountInactive: the claim stays
// unsettled and nothing is written (#444). A claim whose purpose is a closed
// envelope is ErrIncidentalClosed (ADR-031, #473): the payout would take the
// envelope's balance below the zero its close squared it to, so the treasurer
// reopens it, settles, and closes it again.
func (l *Ledger) SettleReimbursement(ctx context.Context, p SettleReimbursementParams) (store.Transaction, error) {
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return store.Transaction{}, err
	}
	if err := validateOccurredOn("occurred_on", p.OccurredOn, window.entry()); err != nil {
		return store.Transaction{}, err
	}

	var posted store.Transaction
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		claim, err := unsettledClaim(ctx, q, p.FundID, p.ReimbursementID)
		if err != nil {
			return err
		}

		if claim.WaivedOn != nil {
			return ErrReimbursementWaived
		}

		// The payout copies the claim's amount, so a claim filed before the
		// ceiling existed (or restored from a backup) is refused here too.
		if err := CheckAmountCeiling("amount", money.FromDB(claim.Amount)); err != nil {
			return err
		}

		if err := refuseInactiveAccount(ctx, q, p.FundID, p.AccountID); err != nil {
			return err
		}

		if err := refuseClosedIncidental(ctx, q, claim.PurposeID, ErrIncidentalClosed); err != nil {
			return err
		}

		// The settlement row stores no note (#257: nothing generated is stored in
		// transaction.note; a system row explains itself through a display label
		// built from facts on the row, here the claim it settles). The claim's note
		// stays on the reimbursement row; a client shows it as the settlement's
		// "note line" by following reimbursement_id.
		posted, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:          p.FundID,
			AccountID:       p.AccountID,
			PurposeID:       claim.PurposeID,
			Direction:       string(DirectionOut),
			Amount:          claim.Amount,
			OccurredOn:      p.OccurredOn,
			Kind:            string(TransactionKindReimbursement),
			ReimbursementID: &p.ReimbursementID,
			CreatedAt:       l.now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("settling reimbursement: %w", err)
	}
	return posted, nil
}

// UpdateReimbursementParams is every argument UpdateReimbursement needs. Each
// optional field is a pointer meaning "leave alone" when nil, except the two
// nullable columns, where nil is ambiguous between "leave alone" and "clear
// it", so those carry a Set flag, as store.UpdateReimbursementParams and
// PATCH /api/members do.
type UpdateReimbursementParams struct {
	FundID          int64
	ReimbursementID int64

	MemberID   *int64
	PurposeID  *int64
	Amount     *money.Amount // must be > 0 when set
	IncurredOn *string       // "YYYY-MM-DD", a real calendar date, when set

	Note    *string
	SetNote bool

	WaivedOn    *string // "YYYY-MM-DD" to waive, nil with SetWaivedOn to un-waive
	SetWaivedOn bool
}

// UpdateReimbursement corrects a claim that has not been settled, and is how
// a claim is waived: waiving sets one column, so it rides on the correction,
// which makes un-waiving (SetWaivedOn with a nil WaivedOn) free.
//
// This is an UPDATE, not an adjusting entry, and no hole in CLAUDE.md rule 3:
// an unsettled claim is off the ledger (ADR-024). The schema agrees:
// "transaction", transfer, reconciliation and reconciliation_line carry
// immutability triggers and reimbursement deliberately does not.
//
// Settlement is the boundary. Settling copies the claim's amount and
// purpose_id onto an immutable transaction, so a later correction would let
// the claim disagree with the row that paid it, two records that both look
// authoritative. Once settled, the only correction is an ordinary adjusting
// entry, and this method returns ErrReimbursementAlreadySettled.
//
// The check and write share one transaction as in SettleReimbursement: not a
// lock (SetMaxOpenConns(1) makes interleaving impossible) but one coherent
// read-then-decide.
//
// Moving the claim onto a closed envelope is ErrIncidentalClosed, as
// CreateReimbursement refuses filing one there (#473). Moving it off one is
// allowed: that posts nothing and leaves the claim payable.
//
// IncurredOn and WaivedOn are checked against the plausible window, with one
// exception: a correction re-sends the claim's incurred_on unchanged (the form
// always does), and a date that is not being changed is not the caller's to
// defend. A claim dated ahead of today (a zone change, ADR-041) still takes a
// fixed amount. Only the ceiling is waived; the floor is the schema's CHECK.
func (l *Ledger) UpdateReimbursement(ctx context.Context, p UpdateReimbursementParams) (store.Reimbursement, error) {
	if p.Amount != nil && *p.Amount <= 0 {
		return store.Reimbursement{}, invalidField("amount", "amount must be positive, got %d", p.Amount.Int64())
	}
	if p.Amount != nil {
		if err := CheckAmountCeiling("amount", *p.Amount); err != nil {
			return store.Reimbursement{}, err
		}
	}
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return store.Reimbursement{}, err
	}
	if p.WaivedOn != nil {
		if err := validateOccurredOn("waived_on", *p.WaivedOn, window.entry()); err != nil {
			return store.Reimbursement{}, err
		}
	}
	if p.IncurredOn != nil {
		// The shape and the floor always; the ceiling is read inside, once the
		// stored date can be compared.
		if err := validateOccurredOn("incurred_on", *p.IncurredOn, noCeiling); err != nil {
			return store.Reimbursement{}, err
		}
	}

	var updated store.Reimbursement
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		claim, err := unsettledClaim(ctx, q, p.FundID, p.ReimbursementID)
		if err != nil {
			return err
		}
		if p.IncurredOn != nil && *p.IncurredOn != claim.IncurredOn {
			if err := validateOccurredOn("incurred_on", *p.IncurredOn, window.entry()); err != nil {
				return err
			}
		}
		// Only a move is guarded: a correction re-sends the claim's purpose
		// unchanged, and a claim already on a closed envelope must still take a fixed
		// amount, date or note.
		if p.PurposeID != nil && *p.PurposeID != claim.PurposeID {
			if err := refuseClosedIncidental(ctx, q, *p.PurposeID, ErrIncidentalClosed); err != nil {
				return err
			}
		}

		args := store.UpdateReimbursementParams{
			ID:         p.ReimbursementID,
			MemberID:   p.MemberID,
			PurposeID:  p.PurposeID,
			IncurredOn: p.IncurredOn,
		}
		if p.Amount != nil {
			amount := p.Amount.Int64()
			args.Amount = &amount
		}
		if p.SetNote {
			args.SetNote = 1
			args.Note = p.Note
		}
		if p.SetWaivedOn {
			args.SetWaivedOn = 1
			args.WaivedOn = p.WaivedOn
		}

		updated, err = q.UpdateReimbursement(ctx, args)
		return err
	})
	if err != nil {
		return store.Reimbursement{}, fmt.Errorf("updating reimbursement: %w", err)
	}
	return updated, nil
}

// DeleteReimbursement removes a claim that should never have existed (a
// typo), not a debt anyone forgave: a waived claim is waived, not deleted, so
// the record keeps saying it happened.
//
// Settled claims are refused as in UpdateReimbursement, and harder: the
// payout row references this claim's id and can never be deleted, so removal
// would leave the ledger pointing at nothing.
func (l *Ledger) DeleteReimbursement(ctx context.Context, fundID, reimbursementID int64) error {
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := unsettledClaim(ctx, q, fundID, reimbursementID); err != nil {
			return err
		}
		return q.DeleteReimbursement(ctx, reimbursementID)
	})
	if err != nil {
		return fmt.Errorf("deleting reimbursement: %w", err)
	}
	return nil
}

// unsettledClaim fetches the claim and returns it only if no payout row
// references it yet, else ErrReimbursementAlreadySettled. It is the one place
// the "mutable only until settled" rule is written; settle, update and delete
// all use it, so they cannot drift.
//
// A missing claim is sql.ErrNoRows from GetReimbursement, wrapped and left to
// the caller's mapper, as SettleReimbursement always produced.
//
// The check is not a lock: under SetMaxOpenConns(1) no second connection can
// interleave, and reimbursement_settled_once remains the guarantee for
// settling; this names the case.
func unsettledClaim(ctx context.Context, q store.Querier, fundID, reimbursementID int64) (store.Reimbursement, error) {
	claim, err := q.GetReimbursement(ctx, store.GetReimbursementParams{
		ID: reimbursementID, FundID: fundID,
	})
	if err != nil {
		return store.Reimbursement{}, fmt.Errorf("fetching reimbursement: %w", err)
	}

	_, err = q.GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
		FundID:          fundID,
		ReimbursementID: &reimbursementID,
	})
	if err == nil {
		return store.Reimbursement{}, ErrReimbursementAlreadySettled
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return store.Reimbursement{}, fmt.Errorf("checking for an existing settlement: %w", err)
	}
	return claim, nil
}
