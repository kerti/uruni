package ledger

import (
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/money"
)

// ErrAmountTooLarge is returned by every write that takes an amount when that
// amount is above money.MaxAmount, Rp 999.999.999.999 (#499).
//
// It is deliberately not a wrapper of ErrInvalidArgument: a too-large figure
// is well-formed, so the treasurer needs to be told the one thing to change
// (type a smaller number) rather than shown the generic "invalid argument".
// The wire gives it its own code, amount_too_large, and the SPA its own copy.
//
// The ceiling is enforced here and in the SPA's amount input. The schema does
// not carry a CHECK for it yet (that rides the schema epic, because it forces
// a migration of live instances), so this package is the only server-side
// guarantee, and a row written around it is not refused.
var ErrAmountTooLarge = errors.New("ledger: amount is above the ceiling")

// CheckAmountCeiling returns ErrAmountTooLarge, wrapped with the field's name,
// when a is above money.MaxAmount, and nil otherwise. It is the one place the
// ceiling is applied: every write path in this package calls it right after its
// own sign check, and the dues-rate routes (which write through queries, not
// through a Ledger method) call it before they do.
//
// The message names the field but never the value (ADR-022: amounts are not
// logged).
func CheckAmountCeiling(field string, a money.Amount) error {
	if a.AboveCeiling() {
		return fmt.Errorf("%w: %s", ErrAmountTooLarge, field)
	}
	return nil
}

// checkParticipationCeilings applies the ceiling to an envelope's two optional
// figures, the target and the per-member minimum. Nil means "not set" and has
// nothing to check.
func checkParticipationCeilings(target, minimum *money.Amount) error {
	if target != nil {
		if err := CheckAmountCeiling("target_amount", *target); err != nil {
			return err
		}
	}
	if minimum != nil {
		if err := CheckAmountCeiling("minimum_per_member", *minimum); err != nil {
			return err
		}
	}
	return nil
}
