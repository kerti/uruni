package db

import (
	"context"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// ADR-034: a contribution may name its member on the ledger row - the CHECKs
// alone cannot see purpose.kind, so which purpose a member may be tagged to,
// and what a reversal may target, is a BEFORE INSERT trigger. These tests
// exercise that trigger and its own CHECK relaxation directly against the
// schema, the same "what the database itself refuses" shape ledger_test.go's
// own TestDuesFieldsBelongToDuesAndNothingElse already uses for ADR-029.

func TestMemberOnANormalRowOutsideAnEnvelopeIsRefused(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)

	// f.purposeID is kind='main' (newLedgerFixture's own setup) - a member
	// name only belongs on an incidental purpose (ADR-034).
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	}); err == nil {
		t.Error("a named normal row tagged to Kas Utama = nil error, want the trigger to reject it")
	} else if !strings.Contains(err.Error(), "incidental purpose") {
		t.Errorf("error = %q, want it to name the incidental-purpose rule", err)
	}
}

func TestOutgoingNamedNormalRowIsRefused(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelopeID := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")

	// direction='out' fails the CHECK itself (ADR-034's relaxation only
	// covers direction='in'), before the trigger is ever reached.
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-12", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	}); err == nil {
		t.Error("an outgoing named row = nil error, want the CHECK to reject it")
	}
}

func TestNamedContributionOnAnEnvelopeIsAccepted(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelopeID := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	}); err != nil {
		t.Errorf("a named contribution to an envelope = %v, want no error", err)
	}
}

func TestContributionReversalCarryingAPeriodIsRefused(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelopeID := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")

	contribution, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the contribution to reverse = %v, want no error", err)
	}

	period := "2026-08"
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-13", Kind: "adjustment",
		MemberID: &f.memberID, DuesPeriod: &period, ReversesTransactionID: &contribution.ID,
		CreatedAt: 1,
	}); err == nil {
		t.Error("a contribution reversal carrying a period = nil error, want the trigger to reject it")
	}
}

func TestDuesReversalMissingItsPeriodIsRefused(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	period := "2026-08"

	payment, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "dues",
		MemberID: &f.memberID, DuesPeriod: &period, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the dues payment to reverse = %v, want no error", err)
	}

	// member_id matches the payment, but dues_period is missing - the CHECK
	// alone would now accept this (ADR-034 dropped its own dues_period
	// requirement), so this is exactly what the trigger exists to catch.
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-13", Kind: "adjustment",
		MemberID: &f.memberID, ReversesTransactionID: &payment.ID, CreatedAt: 1,
	}); err == nil {
		t.Error("a dues reversal with no dues_period = nil error, want the trigger to reject it")
	}
}

func TestDuesReversalStillWorksUnderTheWidenedTrigger(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	period := "2026-08"

	payment, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "dues",
		MemberID: &f.memberID, DuesPeriod: &period, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the dues payment to reverse = %v, want no error", err)
	}

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-13", Kind: "adjustment",
		MemberID: &f.memberID, DuesPeriod: &period, ReversesTransactionID: &payment.ID,
		CreatedAt: 1,
	}); err != nil {
		t.Errorf("a correctly-shaped dues reversal = %v, want no error", err)
	}
}

func TestContributionReversalWithNoPeriodIsAccepted(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelopeID := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")

	contribution, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the contribution to reverse = %v, want no error", err)
	}

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-13", Kind: "adjustment",
		MemberID: &f.memberID, ReversesTransactionID: &contribution.ID, CreatedAt: 1,
	}); err != nil {
		t.Errorf("a correctly-shaped contribution reversal = %v, want no error", err)
	}
}

func TestReversalMemberMustMatchTheOriginal(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelopeID := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")
	otherMemberID := createMember(t, sqlDB, f.fundID, "Someone Else")

	contribution, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the contribution to reverse = %v, want no error", err)
	}

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelopeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-13", Kind: "adjustment",
		MemberID: &otherMemberID, ReversesTransactionID: &contribution.ID, CreatedAt: 1,
	}); err == nil {
		t.Error("a reversal naming a different member than the original = nil error, want the trigger to reject it")
	}
}

func TestIncidentalRecipientCascadesFromMemberDeleteAndScopesByFund(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelopeID := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")
	if _, err := q.CreateIncidental(ctx, store.CreateIncidentalParams{
		PurposeID: envelopeID, Occasion: "Sunatan", OpenedOn: "2026-08-01", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateIncidental = %v, want no error", err)
	}

	if err := q.CreateIncidentalRecipient(ctx, store.CreateIncidentalRecipientParams{
		FundID: f.fundID, PurposeID: envelopeID, MemberID: f.memberID,
	}); err != nil {
		t.Fatalf("CreateIncidentalRecipient = %v, want no error", err)
	}

	// Another fund's member cannot be named a recipient of this fund's
	// envelope - the composite FK to purpose is what makes that a schema
	// violation rather than an application-remembered rule.
	other := newLedgerFixture(t, sqlDB, "Other Fund", "bcdefghijklmnopqrstuvw")
	if err := q.CreateIncidentalRecipient(ctx, store.CreateIncidentalRecipientParams{
		FundID: f.fundID, PurposeID: envelopeID, MemberID: other.memberID,
	}); err == nil {
		t.Error("a recipient from another fund = nil error, want the composite FK to reject it")
	}

	recipients, err := q.ListIncidentalRecipients(ctx, envelopeID)
	if err != nil {
		t.Fatalf("ListIncidentalRecipients = %v, want no error", err)
	}
	if len(recipients) != 1 || recipients[0].MemberID != f.memberID {
		t.Fatalf("recipients = %+v, want exactly [%d]", recipients, f.memberID)
	}

	if err := q.DeleteMember(ctx, f.memberID); err != nil {
		t.Fatalf("DeleteMember = %v, want no error - a recipient is a choice, not a record, and ON DELETE CASCADE should let it go", err)
	}

	recipients, err = q.ListIncidentalRecipients(ctx, envelopeID)
	if err != nil {
		t.Fatalf("ListIncidentalRecipients after the member delete = %v, want no error", err)
	}
	if len(recipients) != 0 {
		t.Errorf("recipients after the member delete = %+v, want none - ON DELETE CASCADE should have removed the row", recipients)
	}
}

func TestIncidentalMinimumPerMemberMustBePositiveWhenSet(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	purposeID := createPurpose(t, sqlDB, fundID, "incidental", "Sunatan")

	zero := int64(0)
	if _, err := q.CreateIncidental(ctx, store.CreateIncidentalParams{
		PurposeID: purposeID, Occasion: "Sunatan", OpenedOn: "2026-08-01",
		MinimumPerMember: &zero, CreatedAt: 1,
	}); err == nil {
		t.Error("minimum_per_member 0 = nil error, want the CHECK to reject it")
	}

	positive := int64(25000)
	purposeID2 := createPurpose(t, sqlDB, fundID, "incidental", "Sunatan 2")
	if _, err := q.CreateIncidental(ctx, store.CreateIncidentalParams{
		PurposeID: purposeID2, Occasion: "Sunatan 2", OpenedOn: "2026-08-01",
		MinimumPerMember: &positive, CreatedAt: 1,
	}); err != nil {
		t.Errorf("minimum_per_member 25000 = %v, want no error", err)
	}
}
