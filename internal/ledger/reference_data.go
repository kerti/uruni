package ledger

// The reference data the treasurer names and may correct: the fund's own name
// and zone, dues tiers, pass-through purposes, and the removal of a duplicate
// location or member. None of it is a posted fact, so a correction here is an
// UPDATE or a DELETE, never an adjusting entry (CLAUDE.md rule 3 binds the
// ledger, not the labels around it).
//
// Each write carries the caller's input straight into a column, so the schema's
// CHECK and UNIQUE refuse a blank name, a duplicate tier or a zone outside the
// three, and the foreign keys refuse a delete anything still names. Those are not
// pre-checked (ADR-027); refuseWrite and refuseDelete give the refusals their
// domain names. Every method that names an existing row resolves it within the
// fund first, so an id from another fund is sql.ErrNoRows, as a read of it
// would be.

import (
	"context"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// CreateDuesTierParams is every argument CreateDuesTier needs.
type CreateDuesTierParams struct {
	FundID int64
	Name   string // not pre-validated: the CHECK refuses a blank one, UNIQUE (fund_id, name) a duplicate
}

// CreateDuesTier adds a tier to the fund. A blank name is ErrDataConstraint and
// a name the fund already has is ErrValueTaken; nothing is written.
func (l *Ledger) CreateDuesTier(ctx context.Context, p CreateDuesTierParams) (store.DuesTier, error) {
	var tier store.DuesTier
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		tier, err = q.CreateDuesTier(ctx, store.CreateDuesTierParams{FundID: p.FundID, Name: p.Name, CreatedAt: l.now().Unix()})
		return err
	})
	if err != nil {
		return store.DuesTier{}, fmt.Errorf("creating dues tier: %w", refuseWrite(err))
	}
	return tier, nil
}

// RenameDuesTierParams is every argument RenameDuesTier needs.
type RenameDuesTierParams struct {
	FundID int64
	TierID int64
	Name   string // as CreateDuesTierParams.Name
}

// RenameDuesTier renames a tier, the only field of one a correction can change.
// Its rates and the members in it are untouched. The refusals are
// CreateDuesTier's; a tier of another fund is sql.ErrNoRows.
func (l *Ledger) RenameDuesTier(ctx context.Context, p RenameDuesTierParams) (store.DuesTier, error) {
	var tier store.DuesTier
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := q.GetDuesTierForFund(ctx, store.GetDuesTierForFundParams{ID: p.TierID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching dues tier: %w", err)
		}
		var err error
		tier, err = q.UpdateDuesTier(ctx, store.UpdateDuesTierParams{ID: p.TierID, Name: p.Name})
		return err
	})
	if err != nil {
		return store.DuesTier{}, fmt.Errorf("renaming dues tier: %w", refuseWrite(err))
	}
	return tier, nil
}

// CreatePassThroughPurposeParams is every argument CreatePassThroughPurpose
// needs. There is no kind: it is pinned here, so a caller cannot ask for a second
// 'main' (purpose_single_main) or an incidental without its lifecycle row.
type CreatePassThroughPurposeParams struct {
	FundID int64
	Name   string // not pre-validated: the CHECK refuses a blank one
}

// CreatePassThroughPurpose adds a purpose for money the fund holds but does not
// own (PRD 7.6). A blank name is ErrDataConstraint. An incidental is opened with
// OpenIncidental instead, which writes its lifecycle row in the same transaction.
func (l *Ledger) CreatePassThroughPurpose(ctx context.Context, p CreatePassThroughPurposeParams) (store.Purpose, error) {
	var purpose store.Purpose
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		purpose, err = q.CreatePurpose(ctx, store.CreatePurposeParams{
			FundID: p.FundID, Kind: string(PurposeKindPassThrough), Name: p.Name, CreatedAt: l.now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Purpose{}, fmt.Errorf("creating pass-through purpose: %w", refuseWrite(err))
	}
	return purpose, nil
}

// RenamePurposeParams is every argument RenamePurpose needs.
type RenamePurposeParams struct {
	FundID    int64
	PurposeID int64
	Name      string // not pre-validated: the CHECK refuses a blank one
}

// RenamePurpose corrects a pass-through purpose's name. A posted transaction
// references a purpose by id and nothing in the ledger reads the text, so no
// history is rewritten.
//
// The fund's Kas Utama is ErrPurposeNotRenameable. An incidental's name is also
// stored as incidental.occasion, so renaming it here would leave the pair
// half-corrected: it is ErrInvalidArgument, and RenameIncidental moves both. A
// blank name is ErrDataConstraint; a purpose of another fund is sql.ErrNoRows.
func (l *Ledger) RenamePurpose(ctx context.Context, p RenamePurposeParams) (store.Purpose, error) {
	var purpose store.Purpose
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		current, err := q.GetPurposeForFund(ctx, store.GetPurposeForFundParams{ID: p.PurposeID, FundID: p.FundID})
		if err != nil {
			return fmt.Errorf("fetching purpose: %w", err)
		}
		switch PurposeKind(current.Kind) {
		case PurposeKindMain:
			return ErrPurposeNotRenameable
		case PurposeKindIncidental:
			return fmt.Errorf("%w: an incidental is renamed with RenameIncidental", ErrInvalidArgument)
		}
		purpose, err = q.UpdatePurposeName(ctx, store.UpdatePurposeNameParams{ID: p.PurposeID, Name: p.Name})
		return err
	})
	if err != nil {
		return store.Purpose{}, fmt.Errorf("renaming purpose: %w", refuseWrite(err))
	}
	return purpose, nil
}

// UpdateFundParams is every argument UpdateFund needs. A nil field is left
// alone, as the PATCH body's absent key is; both nil changes nothing.
type UpdateFundParams struct {
	FundID   int64
	Name     *string
	TimeZone *string // not pre-validated: the CHECK admits only the three zones (ADR-041)
}

// UpdateFund renames the fund and/or changes its time zone. A display label and
// a zone rewrite nothing posted: every day and month is derived from the zone
// (ADR-041), so setting the old one back restores every earlier reading. A blank
// name or a zone outside the three is ErrDataConstraint. currency and
// report_slug are not settable here; the slug changes only through
// ReplaceReportSlug.
func (l *Ledger) UpdateFund(ctx context.Context, p UpdateFundParams) (store.Fund, error) {
	var fund store.Fund
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		fund, err = q.UpdateFund(ctx, store.UpdateFundParams{ID: p.FundID, Name: p.Name, TimeZone: p.TimeZone})
		return err
	})
	if err != nil {
		return store.Fund{}, fmt.Errorf("updating fund: %w", refuseWrite(err))
	}
	return fund, nil
}

// DeleteAccountParams is every argument DeleteAccount needs.
type DeleteAccountParams struct {
	FundID    int64
	AccountID int64
}

// DeleteAccount removes a location added by mistake: a duplicate, never one used
// then retired (inactive_on). No pre-check for rows naming it: the composite
// foreign keys refuse, and a count first would only race them. A location any
// transaction, count line or other row names is ErrReferencedByOtherRecords and
// stays.
func (l *Ledger) DeleteAccount(ctx context.Context, p DeleteAccountParams) error {
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := q.GetAccountForFund(ctx, store.GetAccountForFundParams{ID: p.AccountID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching account: %w", err)
		}
		return q.DeleteAccount(ctx, p.AccountID)
	})
	if err != nil {
		return fmt.Errorf("deleting account: %w", refuseDelete(err))
	}
	return nil
}

// DeleteMemberParams is every argument DeleteMember needs.
type DeleteMemberParams struct {
	FundID   int64
	MemberID int64
}

// DeleteMember removes a member added by mistake: a duplicate, never one who
// left (inactive_on). The member's tier history goes with it (member_tier
// cascades). No pre-check for rows naming it: a member any posted row, claim or
// envelope contribution names is ErrReferencedByOtherRecords and stays.
func (l *Ledger) DeleteMember(ctx context.Context, p DeleteMemberParams) error {
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: p.MemberID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching member: %w", err)
		}
		return q.DeleteMember(ctx, p.MemberID)
	})
	if err != nil {
		return fmt.Errorf("deleting member: %w", refuseDelete(err))
	}
	return nil
}
