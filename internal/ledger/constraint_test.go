package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
)

// A posted row's immutability trigger raises a constraint error of its own
// (SQLITE_CONSTRAINT_TRIGGER). It names no caller mistake, so refuseWrite and
// refuseDelete must hand it back untouched, never dressed as one of their
// refusals.
func TestConstraintRefusalsLeaveATriggerErrorAlone(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	tx := postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, money.Amount(100_000), "2026-01-01")

	ctx := context.Background()
	_, updateErr := l.db.ExecContext(ctx, `UPDATE "transaction" SET note = 'x' WHERE id = ?`, tx.ID)
	_, deleteErr := l.db.ExecContext(ctx, `DELETE FROM "transaction" WHERE id = ?`, tx.ID)

	for _, tc := range []struct {
		name string
		raw  error
		got  error
	}{
		{"refuseWrite on an UPDATE", updateErr, refuseWrite(updateErr)},
		{"refuseDelete on a DELETE", deleteErr, refuseDelete(deleteErr)},
	} {
		if tc.raw == nil {
			t.Fatalf("%s: the trigger did not fire", tc.name)
		}
		if tc.got != tc.raw {
			t.Errorf("%s = %v, want the trigger's error unchanged", tc.name, tc.got)
		}
		for _, named := range []error{ErrDataConstraint, ErrValueTaken, ErrReferenceMissing, ErrReferencedByOtherRecords} {
			if errors.Is(tc.got, named) {
				t.Errorf("%s matched %v, want no constraint refusal", tc.name, named)
			}
		}
	}
}
