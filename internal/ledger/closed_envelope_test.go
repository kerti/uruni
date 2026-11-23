package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ADR-031: a closed envelope refuses every posting. These are the paths #473
// found walking past it: a dues payment naming its purpose, and a
// reimbursement paid out to (or filed against) a closed envelope.

// closedEnvelopeWithClaim builds a closed envelope holding an open claim, with
// nothing collected, so its purpose balance is zero and a later payout is the
// -X ADR-031's Context names. Closing over an open claim is refused since
// ADR-051 (#846), so the claim is filed on Kas Utama, the empty envelope is
// closed for real, and the claim is then moved onto it in SQL: the state a
// close from before ADR-051 left behind, which these guards still meet.
func closedEnvelopeWithClaim(t *testing.T, l *Ledger, f fixture) (envelopePurposeID int64, claim store.Reimbursement) {
	t.Helper()
	ctx := context.Background()
	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
	claim, err := l.CreateReimbursement(ctx, CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID,
		Amount: 40_000, IncurredOn: "2026-08-05",
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}
	if _, _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if _, err := l.db.ExecContext(ctx, `UPDATE reimbursement SET purpose_id = ? WHERE id = ?`, envelope.PurposeID, claim.ID); err != nil {
		t.Fatalf("moving the claim onto the closed envelope: %v", err)
	}
	claim.PurposeID = envelope.PurposeID
	return envelope.PurposeID, claim
}

// ADR-051 (#846): an envelope cannot close while a claim on it is neither
// settled nor waived; settling or waiving it lets the close through, and
// nothing is posted or closed on the refusal.
func TestCloseIncidentalAndRollRefusesAnOutstandingClaimUntilSettledOrWaived(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, way := range []string{"settled", "waived"} {
		t.Run(way, func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 100_000, "2026-08-01")
			envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
			claim, err := l.CreateReimbursement(ctx, CreateReimbursementParams{
				FundID: f.fundID, MemberID: f.memberID, PurposeID: envelope.PurposeID,
				Amount: 40_000, IncurredOn: "2026-08-05",
			})
			if err != nil {
				t.Fatalf("CreateReimbursement() = %v, want no error", err)
			}
			closeParams := CloseIncidentalAndRollParams{
				FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
			}
			if _, _, err := l.CloseIncidentalAndRoll(ctx, closeParams); !errors.Is(err, ErrIncidentalHasOutstandingReimbursement) {
				t.Fatalf("CloseIncidentalAndRoll() with an open claim = %v, want ErrIncidentalHasOutstandingReimbursement", err)
			}
			got, err := store.New(l.db).GetIncidental(ctx, store.GetIncidentalParams{PurposeID: envelope.PurposeID, FundID: f.fundID})
			if err != nil {
				t.Fatalf("GetIncidental() = %v", err)
			}
			if got.ClosedOn != nil {
				t.Fatalf("closed_on = %q after the refused close, want NULL", *got.ClosedOn)
			}

			switch way {
			case "settled":
				if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
					FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-08-10",
				}); err != nil {
					t.Fatalf("SettleReimbursement() = %v, want no error", err)
				}
			case "waived":
				waivedOn := "2026-08-10"
				if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
					FundID: f.fundID, ReimbursementID: claim.ID, WaivedOn: &waivedOn, SetWaivedOn: true,
				}); err != nil {
					t.Fatalf("UpdateReimbursement(waive) = %v, want no error", err)
				}
			}
			if _, _, err := l.CloseIncidentalAndRoll(ctx, closeParams); err != nil {
				t.Fatalf("CloseIncidentalAndRoll() once the claim is %s = %v, want no error", way, err)
			}
		})
	}
}

func TestSettleReimbursementRefusesAClosedEnvelopeUntilReopened(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	envelopeID, claim := closedEnvelopeWithClaim(t, l, f)

	settle := SettleReimbursementParams{FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-08-21"}
	if _, err := l.SettleReimbursement(ctx, settle); !errors.Is(err, ErrIncidentalClosed) {
		t.Fatalf("SettleReimbursement() on a closed envelope = %v, want an error wrapping ErrIncidentalClosed", err)
	}
	if _, err := store.New(l.db).GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
		FundID: f.fundID, ReimbursementID: &claim.ID,
	}); err == nil {
		t.Fatal("a payout row exists after the refused settle, want none")
	}

	// Reopening is the way back (ADR-031): the payout then goes through.
	if _, err := l.ReopenIncidental(ctx, f.fundID, envelopeID); err != nil {
		t.Fatalf("ReopenIncidental() = %v, want no error", err)
	}
	if _, err := l.SettleReimbursement(ctx, settle); err != nil {
		t.Fatalf("SettleReimbursement() after reopening = %v, want no error", err)
	}
}

func TestCreateReimbursementRefusesAClosedEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	envelopeID, _ := closedEnvelopeWithClaim(t, l, f)

	_, err := l.CreateReimbursement(ctx, CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: envelopeID,
		Amount: 15_000, IncurredOn: "2026-08-22",
	})
	if !errors.Is(err, ErrIncidentalClosed) {
		t.Fatalf("CreateReimbursement() on a closed envelope = %v, want an error wrapping ErrIncidentalClosed", err)
	}

	if _, err := l.CreateReimbursement(ctx, CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID,
		Amount: 15_000, IncurredOn: "2026-08-22",
	}); err != nil {
		t.Fatalf("CreateReimbursement() on Kas Utama = %v, want no error", err)
	}
}

// Moving a claim onto a closed envelope is refused; moving one off it (the
// treasurer re-tagging a claim filed against the wrong envelope) is not, as it
// posts nothing and leaves the claim payable.
func TestUpdateReimbursementRefusesMovingOntoAClosedEnvelopeButNotOff(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	envelopeID, onEnvelope := closedEnvelopeWithClaim(t, l, f)

	onMain := createReimbursement(t, store.New(l.db), f, 20_000, "2026-08-22", nil)
	_, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: onMain.ID, PurposeID: &envelopeID,
	})
	if !errors.Is(err, ErrIncidentalClosed) {
		t.Fatalf("UpdateReimbursement(onto a closed envelope) = %v, want an error wrapping ErrIncidentalClosed", err)
	}

	moved, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: onEnvelope.ID, PurposeID: &f.mainID,
	})
	if err != nil {
		t.Fatalf("UpdateReimbursement(off a closed envelope) = %v, want no error", err)
	}
	if moved.PurposeID != f.mainID {
		t.Errorf("claim purpose = %d after moving it off, want Kas Utama %d", moved.PurposeID, f.mainID)
	}
}

// The server owns a dues payment's purpose: always Kas Utama, never one the
// caller names, so no envelope, open or closed, and no titipan can receive dues.
func TestPostDuesPaymentsLandOnKasUtama(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	rows, err := l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-10",
		Periods: []PeriodAmount{{DuesPeriod: "2026-07", Amount: 25_000}, {DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	for _, row := range rows {
		if row.PurposeID != f.mainID {
			t.Errorf("dues row %d purpose = %d, want Kas Utama %d", row.ID, row.PurposeID, f.mainID)
		}
	}
}

// Correcting a claim's amount, date or note sends its purpose back unchanged
// (Correct.tsx always does). A claim filed against an envelope since closed
// must still take that correction: only moving a claim onto a closed envelope
// is refused, and this one is already there.
func TestUpdateReimbursementCorrectsAClaimAlreadyOnAClosedEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	envelopeID, claim := closedEnvelopeWithClaim(t, l, f)

	amount := money.Amount(claim.Amount + 5_000)
	updated, err := l.UpdateReimbursement(context.Background(), UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, PurposeID: &envelopeID, Amount: &amount,
	})
	if err != nil {
		t.Fatalf("UpdateReimbursement(same closed purpose, new amount) = %v, want no error", err)
	}
	if updated.Amount != amount.Int64() {
		t.Errorf("claim amount = %d, want %d", updated.Amount, amount)
	}
}

// A Cek kas fix is a posting like any other: an `adjusted` or `entry_added`
// line naming a closed envelope is refused, and nothing of the snapshot is
// written (ADR-031).
func TestTakeReconciliationRefusesAFixOnAClosedEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 100_000, "2026-08-01")
	envelopeID, _ := closedEnvelopeWithClaim(t, l, f)

	for _, resolution := range []Resolution{ResolutionAdjusted, ResolutionEntryAdded} {
		_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
			FundID: f.fundID,
			Counts: []AccountCount{{
				AccountID: f.cashID, ActualAmount: 80_000, Resolution: resolution,
				Fix: &Fix{PurposeID: envelopeID, Direction: "out", Amount: 20_000, OccurredOn: "2026-08-31"},
			}, f.bankZero()},
		})
		if !errors.Is(err, ErrIncidentalClosed) {
			t.Errorf("TakeReconciliation(%s fix on a closed envelope) = %v, want an error wrapping ErrIncidentalClosed", resolution, err)
		}
	}
	if recs, err := store.New(l.db).ListReconciliationsByFund(ctx, f.fundID); err != nil || len(recs) != 0 {
		t.Errorf("reconciliations after the refusals = %d (err %v), want none", len(recs), err)
	}
}
