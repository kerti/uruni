package ledger

import (
	"fmt"

	"github.com/kerti/uruni/internal/money"
)

// ErrAmountTooLarge is returned by every write taking an amount above
// money.MaxAmount, Rp999.999.999.999 (#499).
//
// It does not wrap ErrInvalidArgument: a too-large figure is well-formed, and
// the treasurer needs the one thing to change (type a smaller number), not the
// generic "invalid argument". The wire gives it amount_too_large, the SPA its
// own copy.
//
// The ceiling is enforced here and in the SPA's amount input. The schema has no
// CHECK for it yet (it rides the schema epic, as it forces a migration of live
// instances), so this package is the only server-side guarantee, and a row
// written around it is not refused.
var ErrAmountTooLarge = refuse("ledger: amount is above the ceiling",
	ClassInvalid, "amount_too_large",
	"An amount cannot be larger than 999,999,999,999 rupiah.")

// CheckAmountCeiling returns ErrAmountTooLarge, wrapped with the field's name,
// when a is above money.MaxAmount, else nil. It is the one place the ceiling
// is applied: every write path here calls it after its sign check, rate writes
// (CreateDuesRate, CorrectDuesRate) included.
//
// The message names the field, never the value (ADR-022: amounts are not
// logged).
func CheckAmountCeiling(field string, a money.Amount) error {
	if a.AboveCeiling() {
		return fmt.Errorf("%w: %s", ErrAmountTooLarge, field)
	}
	return nil
}

// checkParticipationCeilings applies the ceiling to an envelope's two optional
// figures, the target and per-member minimum. Nil means unset.
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
