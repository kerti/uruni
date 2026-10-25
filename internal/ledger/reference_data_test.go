package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// --- reference data writes ---------------------------------------------------
//
// Each write carries the caller's input into a column, so the schema refuses it
// and the ledger names the refusal. Every refusal here also checks that nothing
// was written.

// otherFund is a second fund, for the "an id of another fund is not found" cases.
func otherFund(t *testing.T, q *store.Queries) store.Fund {
	t.Helper()
	fund, err := q.CreateFund(context.Background(), store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	return fund
}

// postNormalOn posts one ordinary entry, which makes its account (and member, if
// named) referenced for good.
func postNormalOn(t *testing.T, l *Ledger, f fixture, accountID int64) store.Transaction {
	t.Helper()
	posted, err := l.PostTransaction(context.Background(), PostTransactionParams{
		FundID: f.fundID, AccountID: accountID, PurposeID: f.mainID,
		Direction: DirectionIn, Amount: money.Amount(10_000), OccurredOn: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}
	return posted
}

func tierNames(t *testing.T, q *store.Queries, fundID int64) []string {
	t.Helper()
	tiers, err := q.ListDuesTiersByFund(context.Background(), fundID)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		names = append(names, tier.Name)
	}
	return names
}

func TestCreateDuesTierStampsTheLedgersClock(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t).at(augustNow)
	f := newFixture(t, l)

	tier, err := l.CreateDuesTier(context.Background(), CreateDuesTierParams{FundID: f.fundID, Name: "Biasa"})
	if err != nil {
		t.Fatalf("CreateDuesTier() = %v, want no error", err)
	}
	if tier.Name != "Biasa" || tier.FundID != f.fundID || tier.CreatedAt != augustNow.Unix() {
		t.Errorf("CreateDuesTier() = %+v, want Biasa in fund %d created at %d", tier, f.fundID, augustNow.Unix())
	}
}

func TestCreateDuesTierRefusesWhatTheSchemaRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	createDuesTier(t, q, f.fundID, "Biasa")

	tests := []struct {
		name   string
		params CreateDuesTierParams
		want   *Error
	}{
		{"a name the fund already has", CreateDuesTierParams{FundID: f.fundID, Name: "Biasa"}, ErrValueTaken},
		{"a blank name", CreateDuesTierParams{FundID: f.fundID, Name: "  "}, ErrDataConstraint},
		{"a fund that does not exist", CreateDuesTierParams{FundID: f.fundID + 99, Name: "Khusus"}, ErrReferenceMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := l.CreateDuesTier(ctx, tc.params)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateDuesTier() = %v, want %v", err, tc.want)
			}
			if got := tierNames(t, q, f.fundID); len(got) != 1 {
				t.Errorf("tiers = %v, want only Biasa: a refused create writes nothing", got)
			}
		})
	}
}

func TestRenameDuesTier(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	biasa := createDuesTier(t, q, f.fundID, "Biasa")
	createDuesTier(t, q, f.fundID, "Khusus")
	createDuesRate(t, q, biasa, 10_000, "2026-01")

	renamed, err := l.RenameDuesTier(ctx, RenameDuesTierParams{FundID: f.fundID, TierID: biasa, Name: "Reguler"})
	if err != nil {
		t.Fatalf("RenameDuesTier() = %v, want no error", err)
	}
	if renamed.ID != biasa || renamed.Name != "Reguler" {
		t.Errorf("RenameDuesTier() = %+v, want tier %d named Reguler", renamed, biasa)
	}
	if rates, err := q.ListDuesRatesByTier(ctx, biasa); err != nil || len(rates) != 1 {
		t.Errorf("rates after a rename = %v (%v), want the 1 rate untouched", rates, err)
	}

	if _, err := l.RenameDuesTier(ctx, RenameDuesTierParams{FundID: f.fundID, TierID: biasa, Name: "Khusus"}); !errors.Is(err, ErrValueTaken) {
		t.Errorf("RenameDuesTier(onto another tier's name) = %v, want ErrValueTaken", err)
	}
	if _, err := l.RenameDuesTier(ctx, RenameDuesTierParams{FundID: f.fundID, TierID: biasa, Name: ""}); !errors.Is(err, ErrDataConstraint) {
		t.Errorf("RenameDuesTier(blank) = %v, want ErrDataConstraint", err)
	}
	if got := tierNames(t, q, f.fundID); len(got) != 2 || got[0] != "Khusus" && got[1] != "Khusus" {
		t.Errorf("tiers = %v, want Khusus and Reguler: refused renames change nothing", got)
	}

	other := otherFund(t, q)
	if _, err := l.RenameDuesTier(ctx, RenameDuesTierParams{FundID: other.ID, TierID: biasa, Name: "Dicuri"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RenameDuesTier(another fund's tier) = %v, want sql.ErrNoRows", err)
	}
}

// A tier any member_tier row names, a superseded one included, is not deletable
// (ADR-040); the refusal is named and the tier's rates survive it.
func TestDeleteDuesTierNamesTheForeignKeyRefusal(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	tier := createDuesTier(t, q, f.fundID, "Biasa")
	createDuesRate(t, q, tier, 10_000, "2026-01")
	createDuesMember(t, q, f.fundID, duesMemberParams{name: "Andi", tierID: &tier})

	err := l.DeleteDuesTier(context.Background(), f.fundID, tier)
	if !errors.Is(err, ErrReferencedByOtherRecords) {
		t.Fatalf("DeleteDuesTier(a tier a member is in) = %v, want ErrReferencedByOtherRecords", err)
	}
	if rates, err := q.ListDuesRatesByTier(context.Background(), tier); err != nil || len(rates) != 1 {
		t.Errorf("rates after the refused delete = %v (%v), want the 1 rate back", rates, err)
	}
}

func TestDeleteReimbursementNamesTheForeignKeyRefusal(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)
	if _, err := l.CreateReceipt(context.Background(), CreateReceiptParams{FundID: f.fundID, ReimbursementID: &claim.ID, Path: "a.jpg"}); err != nil {
		t.Fatalf("CreateReceipt() = %v, want no error", err)
	}

	err := l.DeleteReimbursement(context.Background(), f.fundID, claim.ID)
	if !errors.Is(err, ErrReferencedByOtherRecords) {
		t.Fatalf("DeleteReimbursement(a claim with a photo) = %v, want ErrReferencedByOtherRecords", err)
	}
	if _, err := q.GetReimbursement(context.Background(), store.GetReimbursementParams{ID: claim.ID, FundID: f.fundID}); err != nil {
		t.Errorf("the claim after the refused delete = %v, want it still there", err)
	}
}

func TestCreatePassThroughPurposePinsTheKind(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t).at(augustNow)
	f := newFixture(t, l)
	ctx := context.Background()

	purpose, err := l.CreatePassThroughPurpose(ctx, CreatePassThroughPurposeParams{FundID: f.fundID, Name: "Iuran RW"})
	if err != nil {
		t.Fatalf("CreatePassThroughPurpose() = %v, want no error", err)
	}
	if purpose.Kind != string(PurposeKindPassThrough) || purpose.Name != "Iuran RW" || purpose.CreatedAt != augustNow.Unix() {
		t.Errorf("CreatePassThroughPurpose() = %+v, want a pass_through named Iuran RW created at %d", purpose, augustNow.Unix())
	}
	// A purpose has no uniqueness on its name.
	if _, err := l.CreatePassThroughPurpose(ctx, CreatePassThroughPurposeParams{FundID: f.fundID, Name: "Iuran RW"}); err != nil {
		t.Errorf("CreatePassThroughPurpose(a name already used) = %v, want no error", err)
	}

	before, err := store.New(l.db).ListPurposesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.CreatePassThroughPurpose(ctx, CreatePassThroughPurposeParams{FundID: f.fundID, Name: " "}); !errors.Is(err, ErrDataConstraint) {
		t.Errorf("CreatePassThroughPurpose(blank) = %v, want ErrDataConstraint", err)
	}
	after, err := store.New(l.db).ListPurposesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("purposes = %d, want %d: a refused create writes nothing", len(after), len(before))
	}
}

func TestRenamePurpose(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	renamed, err := l.RenamePurpose(ctx, RenamePurposeParams{FundID: f.fundID, PurposeID: f.passID, Name: "Titipan RW"})
	if err != nil {
		t.Fatalf("RenamePurpose(a pass-through) = %v, want no error", err)
	}
	if renamed.Name != "Titipan RW" || renamed.Kind != string(PurposeKindPassThrough) {
		t.Errorf("RenamePurpose() = %+v, want the pass-through named Titipan RW", renamed)
	}

	nameOf := func(id int64) string {
		t.Helper()
		p, err := q.GetPurposeForFund(ctx, store.GetPurposeForFundParams{ID: id, FundID: f.fundID})
		if err != nil {
			t.Fatal(err)
		}
		return p.Name
	}

	if _, err := l.RenamePurpose(ctx, RenamePurposeParams{FundID: f.fundID, PurposeID: f.mainID, Name: "Kas Bidang"}); !errors.Is(err, ErrPurposeNotRenameable) {
		t.Errorf("RenamePurpose(Kas Utama) = %v, want ErrPurposeNotRenameable", err)
	}
	if got := nameOf(f.mainID); got != "Primary Cash" {
		t.Errorf("Kas Utama's name = %q after the refused rename, want it unchanged", got)
	}

	// An incidental's name is also incidental.occasion: only RenameIncidental moves both.
	if _, err := l.RenamePurpose(ctx, RenamePurposeParams{FundID: f.fundID, PurposeID: f.incidenID, Name: "Acara"}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("RenamePurpose(an incidental) = %v, want ErrInvalidArgument", err)
	}
	if got := nameOf(f.incidenID); got != "John's birthday" {
		t.Errorf("the incidental's name = %q after the refused rename, want it unchanged", got)
	}

	if _, err := l.RenamePurpose(ctx, RenamePurposeParams{FundID: f.fundID, PurposeID: f.passID, Name: ""}); !errors.Is(err, ErrDataConstraint) {
		t.Errorf("RenamePurpose(blank) = %v, want ErrDataConstraint", err)
	}
	if got := nameOf(f.passID); got != "Titipan RW" {
		t.Errorf("the pass-through's name = %q after the refused rename, want it unchanged", got)
	}

	other := otherFund(t, q)
	if _, err := l.RenamePurpose(ctx, RenamePurposeParams{FundID: other.ID, PurposeID: f.passID, Name: "Dicuri"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RenamePurpose(another fund's purpose) = %v, want sql.ErrNoRows", err)
	}
}

// Renaming moves no money: a pass-through's balance is the same before and after.
func TestRenamePurposeMovesNoMoney(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: DirectionIn, Amount: money.Amount(40_000), OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatal(err)
	}
	fundBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := l.RenamePurpose(ctx, RenamePurposeParams{FundID: f.fundID, PurposeID: f.passID, Name: "Titipan RW"}); err != nil {
		t.Fatal(err)
	}
	passAfter, err := l.PurposeBalance(ctx, f.fundID, f.passID)
	if err != nil {
		t.Fatal(err)
	}
	fundAfter, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if passAfter != 40_000 || fundAfter != fundBefore {
		t.Errorf("after a rename pass-through = %d, fund = %d, want 40000 and %d", passAfter, fundAfter, fundBefore)
	}
}

func TestUpdateFund(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	makassar, tokyo, blank, renamed := "Asia/Makassar", "Asia/Tokyo", " ", "Kas RT 9"

	got, err := l.UpdateFund(ctx, UpdateFundParams{FundID: f.fundID, Name: &renamed})
	if err != nil {
		t.Fatalf("UpdateFund(name) = %v, want no error", err)
	}
	if got.Name != renamed || got.TimeZone != "Asia/Jakarta" {
		t.Errorf("UpdateFund(name) = %q in %q, want the new name and the old zone", got.Name, got.TimeZone)
	}
	got, err = l.UpdateFund(ctx, UpdateFundParams{FundID: f.fundID, TimeZone: &makassar})
	if err != nil {
		t.Fatalf("UpdateFund(zone) = %v, want no error", err)
	}
	if got.Name != renamed || got.TimeZone != makassar {
		t.Errorf("UpdateFund(zone) = %q in %q, want the old name and the new zone", got.Name, got.TimeZone)
	}

	for name, p := range map[string]UpdateFundParams{
		"a blank name":                  {FundID: f.fundID, Name: &blank},
		"a zone outside the three":      {FundID: f.fundID, TimeZone: &tokyo},
		"a good name and a bad zone":    {FundID: f.fundID, Name: strp("Lain"), TimeZone: &tokyo},
		"a bad name and a good zone":    {FundID: f.fundID, Name: &blank, TimeZone: strp("Asia/Jayapura")},
		"neither name nor zone is fine": {FundID: f.fundID},
	} {
		_, err := l.UpdateFund(ctx, p)
		if name == "neither name nor zone is fine" {
			if err != nil {
				t.Errorf("UpdateFund(%s) = %v, want no error", name, err)
			}
			continue
		}
		if !errors.Is(err, ErrDataConstraint) {
			t.Errorf("UpdateFund(%s) = %v, want ErrDataConstraint", name, err)
		}
	}
	fund, err := l.q.GetFund(ctx, f.fundID)
	if err != nil {
		t.Fatal(err)
	}
	if fund.Name != renamed || fund.TimeZone != makassar {
		t.Errorf("fund = %q in %q after the refusals, want %q in %q: a refused update changes neither", fund.Name, fund.TimeZone, renamed, makassar)
	}
}

func TestDeleteAccount(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	if err := l.DeleteAccount(ctx, DeleteAccountParams{FundID: f.fundID, AccountID: f.bankID}); err != nil {
		t.Fatalf("DeleteAccount(an unused location) = %v, want no error", err)
	}
	if _, err := q.GetAccountForFund(ctx, store.GetAccountForFundParams{ID: f.bankID, FundID: f.fundID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the deleted location = %v, want sql.ErrNoRows", err)
	}

	postNormalOn(t, l, f, f.cashID)
	if err := l.DeleteAccount(ctx, DeleteAccountParams{FundID: f.fundID, AccountID: f.cashID}); !errors.Is(err, ErrReferencedByOtherRecords) {
		t.Errorf("DeleteAccount(a location with a posted row) = %v, want ErrReferencedByOtherRecords", err)
	}
	if _, err := q.GetAccountForFund(ctx, store.GetAccountForFundParams{ID: f.cashID, FundID: f.fundID}); err != nil {
		t.Errorf("the refused location = %v, want it still there", err)
	}
	if bal, err := l.AccountBalance(ctx, f.fundID, f.cashID); err != nil || bal != 10_000 {
		t.Errorf("the location's balance = %d (%v), want 10000: a refused delete moves no money", bal, err)
	}

	other := otherFund(t, q)
	if err := l.DeleteAccount(ctx, DeleteAccountParams{FundID: other.ID, AccountID: f.cashID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteAccount(another fund's location) = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteMember(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t).at(augustNow)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tier := createDuesTier(t, q, f.fundID, "Biasa")
	duplicate, err := l.CreateMember(ctx, CreateMemberParams{FundID: f.fundID, Name: "Jane", TierID: &tier, JoinedOn: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.DeleteMember(ctx, DeleteMemberParams{FundID: f.fundID, MemberID: duplicate.ID}); err != nil {
		t.Fatalf("DeleteMember(an unused member with a tier) = %v, want no error", err)
	}
	if rows := tierRows(t, l, duplicate.ID); len(rows) != 0 {
		t.Errorf("member_tier rows = %v, want none: the tier history goes with the member", rows)
	}

	// A claim names the member for good.
	createReimbursement(t, q, f, 75_000, "2026-08-01", nil)
	if err := l.DeleteMember(ctx, DeleteMemberParams{FundID: f.fundID, MemberID: f.memberID}); !errors.Is(err, ErrReferencedByOtherRecords) {
		t.Errorf("DeleteMember(a member a claim names) = %v, want ErrReferencedByOtherRecords", err)
	}
	if _, err := q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: f.memberID, FundID: f.fundID}); err != nil {
		t.Errorf("the refused member = %v, want still there", err)
	}

	other := otherFund(t, q)
	if err := l.DeleteMember(ctx, DeleteMemberParams{FundID: other.ID, MemberID: f.memberID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteMember(another fund's member) = %v, want sql.ErrNoRows", err)
	}
}

// The clock the ledger stamps with is its own, whatever the wall clock says.
func TestReferenceDataWritesStampTheLedgersClockNotTheWalls(t *testing.T) {
	t.Parallel()
	past := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	l := newTestLedger(t).at(past)
	f := newFixture(t, l)

	tier, err := l.CreateDuesTier(context.Background(), CreateDuesTierParams{FundID: f.fundID, Name: "Biasa"})
	if err != nil {
		t.Fatal(err)
	}
	if tier.CreatedAt != past.Unix() {
		t.Errorf("CreatedAt = %d, want %d", tier.CreatedAt, past.Unix())
	}
}
