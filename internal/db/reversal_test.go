package db

import (
	"context"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// ADR-042's reversal rule at the schema: a reversal may target a dues or normal
// row, must go the other way, and must copy amount, location, member and period
// (null-safe, so an unnamed row can be reversed).

// TestAReversalMatrixAgainstTheSchema posts one original of each shape and then
// every reversal the ADR says is accepted or refused, straight at the table.
func TestAReversalMatrixAgainstTheSchema(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelope := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")
	otherAccount := createAccount(t, sqlDB, f.fundID, "bank", "Bank")
	period := "2026-08"

	create := func(p store.CreateTransactionParams) (store.Transaction, error) {
		p.FundID = f.fundID
		if p.OccurredOn == "" {
			p.OccurredOn = "2026-08-12"
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = 1
		}
		return q.CreateTransaction(ctx, p)
	}
	mustPost := func(p store.CreateTransactionParams) store.Transaction {
		t.Helper()
		row, err := create(p)
		if err != nil {
			t.Fatalf("posting %+v = %v, want no error", p, err)
		}
		return row
	}
	// Each case reverses its own fresh original, so the reversed-once index is
	// never what a refusal is about, except in the one case that is.
	ordinaryOut := func() store.Transaction {
		return mustPost(store.CreateTransactionParams{AccountID: f.accountID, PurposeID: f.purposeID,
			Direction: "out", Amount: 40000, Kind: "normal"})
	}
	ordinaryIn := func() store.Transaction {
		return mustPost(store.CreateTransactionParams{AccountID: f.accountID, PurposeID: f.purposeID,
			Direction: "in", Amount: 40000, Kind: "normal"})
	}
	contribution := func() store.Transaction {
		return mustPost(store.CreateTransactionParams{AccountID: f.accountID, PurposeID: envelope,
			Direction: "in", Amount: 25000, Kind: "normal", MemberID: &f.memberID})
	}
	duesPayment := func() store.Transaction {
		return mustPost(store.CreateTransactionParams{AccountID: f.accountID, PurposeID: f.purposeID,
			Direction: "in", Amount: 25000, Kind: "dues", MemberID: &f.memberID, DuesPeriod: &period})
	}
	// reversalOf is the row the ledger would post for orig, to be bent per case.
	reversalOf := func(orig store.Transaction) store.CreateTransactionParams {
		dir := "out"
		if orig.Direction == "out" {
			dir = "in"
		}
		return store.CreateTransactionParams{
			AccountID: orig.AccountID, PurposeID: orig.PurposeID, Direction: dir, Amount: orig.Amount,
			Kind: "adjustment", MemberID: orig.MemberID, DuesPeriod: orig.DuesPeriod,
			ReversesTransactionID: &orig.ID,
		}
	}

	type result struct {
		name   string
		build  func() store.CreateTransactionParams
		accept bool
	}
	cases := []result{
		{"an ordinary out, reversed by an in", func() store.CreateTransactionParams { return reversalOf(ordinaryOut()) }, true},
		{"an ordinary in, reversed by an out", func() store.CreateTransactionParams { return reversalOf(ordinaryIn()) }, true},
		{"a named contribution, reversed by an out carrying its member", func() store.CreateTransactionParams { return reversalOf(contribution()) }, true},
		{"a dues payment, reversed by an out carrying its member and period", func() store.CreateTransactionParams { return reversalOf(duesPayment()) }, true},
		{"a reversal dated before its original (a restore must not refuse a live file)", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryOut())
			p.OccurredOn = "2026-08-01"
			return p
		}, true},

		{"a same-direction 'reversal' of an out", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryOut())
			p.Direction = "out"
			return p
		}, false},
		{"a same-direction 'reversal' of an in", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryIn())
			p.Direction = "in"
			return p
		}, false},
		{"another amount", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryOut())
			p.Amount--
			return p
		}, false},
		{"another location", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryOut())
			p.AccountID = otherAccount
			return p
		}, false},
		{"an unnamed reversal carrying a member", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryIn())
			p.MemberID = &f.memberID
			return p
		}, false},
		{"a named reversal without its member", func() store.CreateTransactionParams {
			p := reversalOf(contribution())
			p.MemberID = nil
			return p
		}, false},
		{"a dues reversal without its period", func() store.CreateTransactionParams {
			p := reversalOf(duesPayment())
			p.DuesPeriod = nil
			return p
		}, false},
		{"a contribution reversal carrying a period", func() store.CreateTransactionParams {
			p := reversalOf(contribution())
			p.DuesPeriod = &period
			return p
		}, false},
		{"an unnamed reversal carrying a period", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryOut())
			p.DuesPeriod = &period
			return p
		}, false},
		{"an opening target", func() store.CreateTransactionParams {
			orig := mustPost(store.CreateTransactionParams{AccountID: otherAccount, PurposeID: f.purposeID,
				Direction: "in", Amount: 10000, Kind: "opening"})
			return reversalOf(orig)
		}, false},
		{"an adjustment target", func() store.CreateTransactionParams {
			orig := mustPost(store.CreateTransactionParams{AccountID: f.accountID, PurposeID: f.purposeID,
				Direction: "out", Amount: 7000, Kind: "adjustment"})
			return reversalOf(orig)
		}, false},
		{"a reversal of a reversal", func() store.CreateTransactionParams {
			return reversalOf(mustPost(reversalOf(ordinaryOut())))
		}, false},
		{"a non-adjustment claiming to reverse", func() store.CreateTransactionParams {
			p := reversalOf(ordinaryOut())
			p.Kind = "normal"
			return p
		}, false},
	}
	for _, tc := range cases {
		_, err := create(tc.build())
		switch {
		case tc.accept && err != nil:
			t.Errorf("%s = %v, want it accepted", tc.name, err)
		case !tc.accept && err == nil:
			t.Errorf("%s = nil error, want the schema to refuse it", tc.name)
		}
	}

	t.Run("a second reversal of one row", func(t *testing.T) {
		orig := ordinaryOut()
		mustPost(reversalOf(orig))
		if _, err := create(reversalOf(orig)); err == nil {
			t.Error("a second reversal = nil error, want the reversed-once index to refuse it")
		}
	})

	t.Run("a reversal naming another fund's row", func(t *testing.T) {
		other := newLedgerFixture(t, sqlDB, "Other Fund", "zyxwvutsrqponmlkjihgfe")
		foreign, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: other.fundID, AccountID: other.accountID, PurposeID: other.purposeID,
			Direction: "out", Amount: 40000, OccurredOn: "2026-08-12", Kind: "normal", CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("posting the other fund's row = %v, want no error", err)
		}
		p := reversalOf(foreign)
		p.AccountID, p.PurposeID = f.accountID, f.purposeID
		if _, err := create(p); err == nil {
			t.Error("a reversal naming another fund's row = nil error, want the composite FK to refuse it")
		}
	})
}

// TestTheReversalTriggerNamesItsRule pins the message, so a refused restore says
// what is wrong with the row.
func TestTheReversalTriggerNamesItsRule(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	orig := f.post(t, sqlDB, "out", 40000)

	_, err := store.New(sqlDB).CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 40000, OccurredOn: "2026-08-12", Kind: "adjustment",
		ReversesTransactionID: &orig, CreatedAt: 1,
	})
	if err == nil {
		t.Fatal("a same-direction reversal = nil error, want the trigger to refuse it")
	}
	if !strings.Contains(err.Error(), "must reverse a dues payment or a normal transaction") {
		t.Errorf("error = %q, want it to name the reversal rule", err)
	}
}
