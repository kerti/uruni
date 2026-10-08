package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// participationFor finds one member's row in an Expected slice, mirroring
// dues_status_test.go's own statusFor.
func participationFor(t *testing.T, rows []MemberParticipation, memberID int64) (MemberParticipation, bool) {
	t.Helper()
	for _, r := range rows {
		if r.Member.ID == memberID {
			return r, true
		}
	}
	return MemberParticipation{}, false
}

func unexpectedFor(t *testing.T, rows []UnexpectedContribution, memberID int64) (UnexpectedContribution, bool) {
	t.Helper()
	for _, r := range rows {
		if r.Member.ID == memberID {
			return r, true
		}
	}
	return UnexpectedContribution{}, false
}

func strPtr(s string) *string { return &s }

// TestGetIncidentalParticipationExpectedIsSetByOpenedOnDate: expected is
// every member active on the day the envelope opened - joined_on <=
// opened_on, inactive_on NULL or > opened_on - tier ignored entirely
// (ADR-034), fixed by that one date regardless of what the roster looks
// like later.
func TestGetIncidentalParticipationExpectedIsSetByOpenedOnDate(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	const openedOn = "2026-08-01"
	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", openedOn)

	alwaysActive := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Always Active"})
	joinedBefore := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Joined Before", joinedOn: strPtr("2026-07-01")})
	joinedOnTheDay := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Joined On The Day", joinedOn: strPtr(openedOn)})
	joinedAfter := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Joined After", joinedOn: strPtr("2026-08-02")})
	leftBefore := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Left Before", inactiveOn: strPtr("2026-07-31")})
	leftOnTheDay := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Left On The Day", inactiveOn: strPtr(openedOn)})
	leftAfter := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Left After", inactiveOn: strPtr("2026-08-02")})

	participation, err := l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() = %v, want no error", err)
	}

	wantExpected := map[int64]bool{
		f.memberID:     true, // the fixture's own member, no joined/inactive dates
		alwaysActive:   true,
		joinedBefore:   true,
		joinedOnTheDay: true, // joined_on <= opened_on is expected, inclusive
		joinedAfter:    false,
		leftBefore:     false,
		leftOnTheDay:   false, // inactive_on must be STRICTLY > opened_on
		leftAfter:      true,
	}
	for memberID, want := range wantExpected {
		_, got := participationFor(t, participation.Expected, memberID)
		if got != want {
			t.Errorf("member %d expected = %v, want %v", memberID, got, want)
		}
	}
}

// TestGetIncidentalParticipationExcludesRecipients: a recipient is never
// expected, however active - and if they contribute anyway, they surface
// under Unexpected, never as a third state on the expected table.
func TestGetIncidentalParticipationExcludesRecipients(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	recipientID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "The Recipient"})

	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Sunatan", OpenedOn: "2026-08-01",
		RecipientMemberIDs: []int64{recipientID},
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}

	participation, err := l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() = %v, want no error", err)
	}
	if _, ok := participationFor(t, participation.Expected, recipientID); ok {
		t.Error("recipient found in Expected, want it excluded")
	}

	// The recipient gives anyway - still not "expected", but not ignored:
	// Sumbangan lain.
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-08-12", MemberID: &recipientID,
	}); err != nil {
		t.Fatalf("PostTransaction(recipient's own contribution) = %v, want no error", err)
	}

	participation, err = l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() after the recipient's own gift = %v, want no error", err)
	}
	if _, ok := participationFor(t, participation.Expected, recipientID); ok {
		t.Error("recipient found in Expected after contributing, want it still excluded")
	}
	got, ok := unexpectedFor(t, participation.Unexpected, recipientID)
	if !ok {
		t.Fatal("recipient's own contribution missing from Unexpected")
	}
	if got.ContributedAmount != 10_000 {
		t.Errorf("recipient's Unexpected ContributedAmount = %d, want 10000", got.ContributedAmount)
	}
}

// TestGetIncidentalParticipationLaterJoinerIsUnexpectedNotBelum: a member
// who joins after the envelope opened is not on the expected roster at all
// - if they give, it is Sumbangan lain, never "belum" for someone who was
// never asked.
func TestGetIncidentalParticipationLaterJoinerIsUnexpectedNotBelum(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
	laterJoiner := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Later Joiner", joinedOn: strPtr("2026-08-05")})

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 15_000, OccurredOn: "2026-08-12", MemberID: &laterJoiner,
	}); err != nil {
		t.Fatalf("PostTransaction(later joiner's contribution) = %v, want no error", err)
	}

	participation, err := l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() = %v, want no error", err)
	}
	if _, ok := participationFor(t, participation.Expected, laterJoiner); ok {
		t.Error("later joiner found in Expected, want them excluded (they joined after opened_on)")
	}
	got, ok := unexpectedFor(t, participation.Unexpected, laterJoiner)
	if !ok {
		t.Fatal("later joiner's contribution missing from Unexpected")
	}
	if got.ContributedAmount != 15_000 {
		t.Errorf("later joiner's Unexpected ContributedAmount = %d, want 15000", got.ContributedAmount)
	}
}

// TestGetIncidentalParticipationStates: sudah for anything given, belum for
// nothing, kurang only when a minimum is set and the sum is below it - a
// contribution below no stated minimum is Sudah, not Kurang.
func TestGetIncidentalParticipationStates(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	minimum := money.Amount(20_000)
	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Sunatan", OpenedOn: "2026-08-01", MinimumPerMember: &minimum,
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}

	gaveNothing := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Gave Nothing"})
	gaveBelowMinimum := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Gave Below Minimum"})
	gaveTheMinimum := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Gave The Minimum"})
	gaveMore := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Gave More"})

	for memberID, amount := range map[int64]money.Amount{
		gaveBelowMinimum: 10_000,
		gaveTheMinimum:   20_000,
		gaveMore:         30_000,
	} {
		if _, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
			Direction: "in", Amount: amount, OccurredOn: "2026-08-12", MemberID: &memberID,
		}); err != nil {
			t.Fatalf("PostTransaction(member %d) = %v, want no error", memberID, err)
		}
	}

	participation, err := l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() = %v, want no error", err)
	}

	wantState := map[int64]ParticipationState{
		gaveNothing:      ParticipationBelum,
		gaveBelowMinimum: ParticipationKurang,
		gaveTheMinimum:   ParticipationSudah,
		gaveMore:         ParticipationSudah,
	}
	for memberID, want := range wantState {
		got, ok := participationFor(t, participation.Expected, memberID)
		if !ok {
			t.Fatalf("member %d missing from Expected", memberID)
		}
		if got.State != want {
			t.Errorf("member %d State = %q, want %q", memberID, got.State, want)
		}
	}
}

// TestGetIncidentalParticipationNoMinimumMeansAnyGiftIsSudah: with no
// minimum set, any contribution at all is Sudah - Kurang is only
// representable once a minimum exists (ADR-034).
func TestGetIncidentalParticipationNoMinimumMeansAnyGiftIsSudah(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 1, OccurredOn: "2026-08-12", MemberID: &f.memberID,
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	participation, err := l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() = %v, want no error", err)
	}
	got, ok := participationFor(t, participation.Expected, f.memberID)
	if !ok {
		t.Fatal("member missing from Expected")
	}
	if got.State != ParticipationSudah {
		t.Errorf("State = %q, want %q - no minimum was ever set", got.State, ParticipationSudah)
	}
}

// TestSetIncidentalParticipationReplacesMinimumAndRecipients: both are
// mutable like occasion, and a set fully replaces the prior one rather than
// adding to it.
func TestSetIncidentalParticipationReplacesMinimumAndRecipients(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	firstRecipient := createDuesMember(t, q, f.fundID, duesMemberParams{name: "First Recipient"})
	secondRecipient := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Second Recipient"})

	minimum := money.Amount(15_000)
	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Sunatan", OpenedOn: "2026-08-01",
		MinimumPerMember: &minimum, RecipientMemberIDs: []int64{firstRecipient},
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}

	newMinimum := money.Amount(30_000)
	updated, err := l.SetIncidentalParticipation(ctx, SetIncidentalParticipationParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID,
		MinimumPerMember: &newMinimum, RecipientMemberIDs: []int64{secondRecipient},
	})
	if err != nil {
		t.Fatalf("SetIncidentalParticipation() = %v, want no error", err)
	}
	if updated.MinimumPerMember == nil || *updated.MinimumPerMember != 30_000 {
		t.Errorf("MinimumPerMember = %v, want 30000", updated.MinimumPerMember)
	}

	participation, err := l.GetIncidentalParticipation(ctx, f.fundID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("GetIncidentalParticipation() = %v, want no error", err)
	}
	if _, ok := participationFor(t, participation.Expected, firstRecipient); !ok {
		t.Error("first recipient not found in Expected - the old recipient set should have been fully replaced")
	}
	if _, ok := participationFor(t, participation.Expected, secondRecipient); ok {
		t.Error("second recipient found in Expected, want it excluded as the new recipient")
	}

	cleared, err := l.SetIncidentalParticipation(ctx, SetIncidentalParticipationParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID,
	})
	if err != nil {
		t.Fatalf("SetIncidentalParticipation(clearing) = %v, want no error", err)
	}
	if cleared.MinimumPerMember != nil {
		t.Errorf("MinimumPerMember after clearing = %v, want nil", cleared.MinimumPerMember)
	}
}

// TestSetIncidentalParticipationCorrectsTheTarget (#381): a target is an
// expectation, not a posted fact, so it can be set, changed and cleared - on
// a closed envelope too - without posting anything or moving a balance.
func TestSetIncidentalParticipationCorrectsTheTarget(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	target := money.Amount(500_000)
	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Sunatan", OpenedOn: "2026-08-01", TargetAmount: &target,
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}
	postEntry(t, l, f.fundID, f.cashID, envelope.PurposeID, "in", 40_000, "2026-08-02", nil)

	countRows := func() int {
		t.Helper()
		var n int
		if err := l.db.QueryRow(`SELECT count(*) FROM "transaction" WHERE fund_id = ?`, f.fundID).Scan(&n); err != nil {
			t.Fatalf("counting transactions: %v", err)
		}
		return n
	}
	rowsBefore := countRows()
	balanceBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}

	set := func(target *money.Amount) store.Incidental {
		t.Helper()
		updated, err := l.SetIncidentalParticipation(ctx, SetIncidentalParticipationParams{
			FundID: f.fundID, PurposeID: envelope.PurposeID, TargetAmount: target,
		})
		if err != nil {
			t.Fatalf("SetIncidentalParticipation(target %v) = %v, want no error", target, err)
		}
		return updated
	}

	raised := money.Amount(750_000)
	if got := set(&raised); got.TargetAmount == nil || *got.TargetAmount != 750_000 {
		t.Errorf("TargetAmount on an open envelope = %v, want 750000", got.TargetAmount)
	}
	if after := countRows(); after != rowsBefore {
		t.Errorf("transactions = %d after raising the target, want %d - a target posts nothing", after, rowsBefore)
	}

	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	rowsBefore = countRows() // the roll posts its own pair; nothing after it may

	lowered := money.Amount(300_000)
	if got := set(&lowered); got.TargetAmount == nil || *got.TargetAmount != 300_000 {
		t.Errorf("TargetAmount on a closed envelope = %v, want 300000", got.TargetAmount)
	}
	if got := set(nil); got.TargetAmount != nil {
		t.Errorf("TargetAmount after clearing = %v, want nil", got.TargetAmount)
	}

	if after := countRows(); after != rowsBefore {
		t.Errorf("transactions = %d after correcting the target, want %d - a target posts nothing", after, rowsBefore)
	}
	balanceAfter, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if balanceAfter != balanceBefore {
		t.Errorf("FundBalance = %d after correcting the target, want %d", balanceAfter, balanceBefore)
	}
}

func TestSetIncidentalParticipationRefusesANonPositiveTarget(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")

	for _, v := range []money.Amount{0, -1_000} {
		_, err := l.SetIncidentalParticipation(context.Background(), SetIncidentalParticipationParams{
			FundID: f.fundID, PurposeID: envelope.PurposeID, TargetAmount: &v,
		})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("SetIncidentalParticipation(target %d) = %v, want ErrInvalidArgument", v, err)
		}
	}
}

func TestSetIncidentalParticipationRefusesAnotherFundsEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	other := newSecondFund(t, l)
	theirs := openTestIncidental(t, l, other.fundID, "Other Collection", "2026-08-01")

	target := money.Amount(100_000)
	_, err := l.SetIncidentalParticipation(context.Background(), SetIncidentalParticipationParams{
		FundID: f.fundID, PurposeID: theirs.PurposeID, TargetAmount: &target,
	})
	if err == nil {
		t.Fatal("SetIncidentalParticipation(another fund's envelope) = nil, want an error")
	}
	got, err := store.New(l.db).GetIncidental(context.Background(), store.GetIncidentalParams{PurposeID: theirs.PurposeID, FundID: other.fundID})
	if err != nil {
		t.Fatalf("GetIncidental() = %v, want no error", err)
	}
	if got.TargetAmount != nil {
		t.Errorf("other fund's TargetAmount = %v, want untouched (nil)", got.TargetAmount)
	}
}
