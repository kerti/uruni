package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// The ledger's storage layer: what the database itself refuses, as opposed to
// what the application remembers to check. Every test here fails by *not*
// getting an error.

func createAccount(t *testing.T, sqlDB *sql.DB, fundID int64, kind, name string) int64 {
	t.Helper()
	a, err := store.New(sqlDB).CreateAccount(context.Background(), store.CreateAccountParams{
		FundID: fundID, Kind: kind, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateAccount(%d, %q) = %v, want no error", fundID, name, err)
	}
	return a.ID
}

func createPurpose(t *testing.T, sqlDB *sql.DB, fundID int64, kind, name string) int64 {
	t.Helper()
	p, err := store.New(sqlDB).CreatePurpose(context.Background(), store.CreatePurposeParams{
		FundID: fundID, Kind: kind, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreatePurpose(%d, %q) = %v, want no error", fundID, name, err)
	}
	return p.ID
}

func createMember(t *testing.T, sqlDB *sql.DB, fundID int64, name string) int64 {
	t.Helper()
	m, err := store.New(sqlDB).CreateMember(context.Background(), store.CreateMemberParams{
		FundID: fundID, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(%d, %q) = %v, want no error", fundID, name, err)
	}
	return m.ID
}

func createReimbursement(t *testing.T, sqlDB *sql.DB, fundID, memberID, purposeID, amount int64) int64 {
	t.Helper()
	r, err := store.New(sqlDB).CreateReimbursement(context.Background(), store.CreateReimbursementParams{
		FundID: fundID, MemberID: memberID, PurposeID: purposeID, Amount: amount,
		IncurredOn: "2026-08-01", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement(%d, %d) = %v, want no error", fundID, memberID, err)
	}
	return r.ID
}

func createTransfer(t *testing.T, sqlDB *sql.DB, fundID int64, kind string) int64 {
	t.Helper()
	tr, err := store.New(sqlDB).CreateTransfer(context.Background(), store.CreateTransferParams{
		FundID: fundID, Kind: kind, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransfer(%d, %q) = %v, want no error", fundID, kind, err)
	}
	return tr.ID
}

// ledgerFixture is one fund with the rows every ledger test needs under it.
type ledgerFixture struct {
	fundID, accountID, purposeID, memberID int64
}

func newLedgerFixture(t *testing.T, sqlDB *sql.DB, name, slug string) ledgerFixture {
	t.Helper()
	f := ledgerFixture{}
	f.fundID = createFund(t, sqlDB, name, slug)
	f.accountID = createAccount(t, sqlDB, f.fundID, "cash", "Kas tunai")
	f.purposeID = createPurpose(t, sqlDB, f.fundID, "main", "Kas Utama")
	f.memberID = createMember(t, sqlDB, f.fundID, "Bu Sri")
	return f
}

// post inserts a plain 'normal' entry and returns its id, for tests whose
// subject is something other than the entry itself.
func (f ledgerFixture) post(t *testing.T, sqlDB *sql.DB, direction string, amount int64) int64 {
	t.Helper()
	tx, err := store.New(sqlDB).CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: direction, Amount: amount, OccurredOn: "2026-08-12", Kind: "normal",
		CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(%s, %d) = %v, want no error", direction, amount, err)
	}
	return tx.ID
}

func TestPostedTransactionCannotBeUpdatedOrDeleted(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)
	txID := f.post(t, sqlDB, "in", 50000)

	const want = "transaction rows are immutable"

	if _, err := sqlDB.Exec(`UPDATE "transaction" SET amount = 1 WHERE id = ?`, txID); err == nil {
		t.Error("UPDATE on a posted transaction = nil error, want the trigger to abort it")
	} else if !strings.Contains(err.Error(), want) {
		t.Errorf("UPDATE error = %q, want it to contain %q", err, want)
	}

	if _, err := sqlDB.Exec(`DELETE FROM "transaction" WHERE id = ?`, txID); err == nil {
		t.Error("DELETE on a posted transaction = nil error, want the trigger to abort it")
	} else if !strings.Contains(err.Error(), want) {
		t.Errorf("DELETE error = %q, want it to contain %q", err, want)
	}

	// The row is still there and unchanged - the abort rolled nothing forward.
	got, err := store.New(sqlDB).GetTransaction(context.Background(), txID)
	if err != nil {
		t.Fatalf("GetTransaction(%d) = %v, want no error", txID, err)
	}
	if got.Amount != 50000 {
		t.Errorf("amount after the aborted UPDATE = %d, want 50000", got.Amount)
	}
}

func TestTransferCannotBeUpdatedOrDeleted(t *testing.T) {
	sqlDB := migratedTestDB(t)
	fundID := createFund(t, sqlDB, "Kas RT", validSlug)
	transferID := createTransfer(t, sqlDB, fundID, "between_accounts")

	const want = "transfer rows are immutable"

	// Re-labelling a transfer after the fact would rewrite what its two
	// transactions mean without touching either of them.
	if _, err := sqlDB.Exec(`UPDATE transfer SET kind = 'reclass_purpose' WHERE id = ?`, transferID); err == nil {
		t.Error("UPDATE on a transfer = nil error, want the trigger to abort it")
	} else if !strings.Contains(err.Error(), want) {
		t.Errorf("UPDATE error = %q, want it to contain %q", err, want)
	}

	if _, err := sqlDB.Exec(`DELETE FROM transfer WHERE id = ?`, transferID); err == nil {
		t.Error("DELETE on a transfer = nil error, want the trigger to abort it")
	} else if !strings.Contains(err.Error(), want) {
		t.Errorf("DELETE error = %q, want it to contain %q", err, want)
	}
}

func TestImmutableTablesStillAcceptInserts(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	// ADR-012's import restores a database by inserting rows, so the triggers
	// must guard UPDATE and DELETE only. (reconciliation and
	// reconciliation_line get the same pair, and this test, in M2.4.)
	if id := createTransfer(t, sqlDB, f.fundID, "between_accounts"); id == 0 {
		t.Error("CreateTransfer returned id 0, want a real row")
	}
	if id := f.post(t, sqlDB, "in", 1000); id == 0 {
		t.Error("CreateTransaction returned id 0, want a real row")
	}
}

func TestReimbursementIsSettledAtMostOnce(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	reimbID := createReimbursement(t, sqlDB, f.fundID, f.memberID, f.purposeID, 2000)
	settle := store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 2000, OccurredOn: "2026-08-12", Kind: "reimbursement",
		ReimbursementID: &reimbID, CreatedAt: 1,
	}

	if _, err := q.CreateTransaction(ctx, settle); err != nil {
		t.Fatalf("first settling transaction = %v, want no error", err)
	}
	if _, err := q.CreateTransaction(ctx, settle); err == nil {
		t.Fatal("second settling transaction for the same reimbursement = nil error, want the partial unique index to reject it")
	}
}

func TestReimbursementKindRequiresItsClaimAndAnOutwardDirection(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)
	reimbID := createReimbursement(t, sqlDB, f.fundID, f.memberID, f.purposeID, 2000)

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 2000, OccurredOn: "2026-08-12", Kind: "reimbursement",
		CreatedAt: 1,
	}); err == nil {
		t.Error("kind='reimbursement' with no reimbursement_id = nil error, want the CHECK to reject it")
	}

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "in", Amount: 2000, OccurredOn: "2026-08-12", Kind: "reimbursement",
		ReimbursementID: &reimbID, CreatedAt: 1,
	}); err == nil {
		t.Error("kind='reimbursement' with direction='in' = nil error, want the CHECK to reject it")
	}
}

func TestDuesFieldsBelongToDuesAndNothingElse(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)
	period := "2026-08"

	base := func() store.CreateTransactionParams {
		return store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
			Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", CreatedAt: 1,
		}
	}

	t.Run("valid dues payment", func(t *testing.T) {
		p := base()
		p.Kind, p.MemberID, p.DuesPeriod = "dues", &f.memberID, &period
		if _, err := q.CreateTransaction(ctx, p); err != nil {
			t.Errorf("a complete dues payment = %v, want no error", err)
		}
	})

	t.Run("dues without a member", func(t *testing.T) {
		p := base()
		p.Kind, p.DuesPeriod = "dues", &period
		if _, err := q.CreateTransaction(ctx, p); err == nil {
			t.Error("kind='dues' with no member_id = nil error, want the CHECK to reject it")
		}
	})

	t.Run("dues without a period", func(t *testing.T) {
		p := base()
		p.Kind, p.MemberID = "dues", &f.memberID
		if _, err := q.CreateTransaction(ctx, p); err == nil {
			t.Error("kind='dues' with no dues_period = nil error, want the CHECK to reject it")
		}
	})

	t.Run("dues paid outward", func(t *testing.T) {
		p := base()
		p.Kind, p.MemberID, p.DuesPeriod, p.Direction = "dues", &f.memberID, &period, "out"
		if _, err := q.CreateTransaction(ctx, p); err == nil {
			t.Error("kind='dues' with direction='out' = nil error, want the CHECK to reject it")
		}
	})

	t.Run("a normal entry carrying dues fields", func(t *testing.T) {
		p := base()
		p.Kind, p.MemberID, p.DuesPeriod = "normal", &f.memberID, &period
		if _, err := q.CreateTransaction(ctx, p); err == nil {
			t.Error("kind='normal' with member_id and dues_period = nil error, want the CHECK to reject it")
		}
	})

	t.Run("an impossible period", func(t *testing.T) {
		bad := "2026-13"
		p := base()
		p.Kind, p.MemberID, p.DuesPeriod = "dues", &f.memberID, &bad
		if _, err := q.CreateTransaction(ctx, p); err == nil {
			t.Error("dues_period '2026-13' = nil error, want the CHECK to reject it")
		}
	})
}

func TestAdjustmentStandsAloneWithNoReconciliation(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	// CLAUDE.md rule 3 makes every correction an adjusting entry, not only the
	// ones raised during a reconciliation. An earlier draft of the schema had a
	// CHECK forcing a Tuesday-afternoon correction to masquerade as 'normal'.
	note := "koreksi selisih kas"
	if _, err := store.New(sqlDB).CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 5000, OccurredOn: "2026-08-12", Kind: "adjustment",
		Note: &note, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("a standalone adjustment = %v, want no error", err)
	}
}

func TestTransferKindRequiresATransfer(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 100000, OccurredOn: "2026-08-12", Kind: "transfer",
		CreatedAt: 1,
	}); err == nil {
		t.Fatal("kind='transfer' with no transfer_id = nil error, want the CHECK to reject it")
	}

	// The pair itself: equal amounts, opposite directions, one transfer row, and
	// a fund total that has not moved.
	bankID := createAccount(t, sqlDB, f.fundID, "bank", "Rekening BRI")
	transferID := createTransfer(t, sqlDB, f.fundID, "between_accounts")
	f.post(t, sqlDB, "in", 100000)

	for _, leg := range []struct {
		accountID int64
		direction string
	}{{f.accountID, "out"}, {bankID, "in"}} {
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: leg.accountID, PurposeID: f.purposeID,
			Direction: leg.direction, Amount: 100000, OccurredOn: "2026-08-12",
			Kind: "transfer", TransferID: &transferID, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("transfer leg (%s) = %v, want no error", leg.direction, err)
		}
	}

	total, err := q.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if total != 100000 {
		t.Errorf("fund balance after a transfer = %d, want 100000 - a transfer moves money, it does not create or destroy it", total)
	}
}

func TestTransactionCannotBorrowAnotherFundsRow(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	a := newLedgerFixture(t, sqlDB, "Fund A", validSlug)
	b := newLedgerFixture(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")

	otherReimb := createReimbursement(t, sqlDB, b.fundID, b.memberID, b.purposeID, 2000)
	otherTransfer := createTransfer(t, sqlDB, b.fundID, "between_accounts")
	period := "2026-08"

	// Each row exists, so a single-column REFERENCES would accept every one of
	// these. Only the composite (fund_id, ...) form knows it is the wrong fund.
	tests := []struct {
		name  string
		mutex func(p *store.CreateTransactionParams)
	}{
		{"account", func(p *store.CreateTransactionParams) { p.AccountID = b.accountID }},
		{"purpose", func(p *store.CreateTransactionParams) { p.PurposeID = b.purposeID }},
		{"member", func(p *store.CreateTransactionParams) {
			p.Kind, p.MemberID, p.DuesPeriod, p.Direction = "dues", &b.memberID, &period, "in"
		}},
		{"reimbursement", func(p *store.CreateTransactionParams) {
			p.Kind, p.ReimbursementID, p.Direction = "reimbursement", &otherReimb, "out"
		}},
		{"transfer", func(p *store.CreateTransactionParams) {
			p.Kind, p.TransferID = "transfer", &otherTransfer
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := store.CreateTransactionParams{
				FundID: a.fundID, AccountID: a.accountID, PurposeID: a.purposeID,
				Direction: "out", Amount: 1000, OccurredOn: "2026-08-12", Kind: "normal",
				CreatedAt: 1,
			}
			tt.mutex(&p)
			if _, err := q.CreateTransaction(ctx, p); err == nil {
				t.Errorf("transaction in fund A referencing fund B's %s = nil error, want the composite FK to reject it", tt.name)
			}
		})
	}
}

func TestTransactionAmountMustBePositive(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	// direction carries the sign. A negative or zero amount is a second way to
	// say the same thing, and two ways is one too many.
	for _, amount := range []int64{0, -1000} {
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
			Direction: "out", Amount: amount, OccurredOn: "2026-08-12", Kind: "normal",
			CreatedAt: 1,
		}); err == nil {
			t.Errorf("CreateTransaction with amount %d = nil error, want the CHECK to reject it", amount)
		}
	}
}

func TestReceiptHasExactlyOneParent(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	txID := f.post(t, sqlDB, "out", 30000)
	reimbID := createReimbursement(t, sqlDB, f.fundID, f.memberID, f.purposeID, 2000)

	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: f.fundID, TransactionID: &txID, ReimbursementID: &reimbID,
		Path: "receipts/both.jpg", UploadedAt: 1,
	}); err == nil {
		t.Error("receipt with two parents = nil error, want the CHECK to reject it")
	}

	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: f.fundID, Path: "receipts/orphan.jpg", UploadedAt: 1,
	}); err == nil {
		t.Error("receipt with no parent = nil error, want the CHECK to reject it")
	}
}

func TestReceiptCanBeAttachedAfterTheEntryIsPosted(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	// The whole reason receipt is its own table: the ledger row is immutable, so
	// a photo taken later has nowhere to go if it lives as a column (ADR-011).
	txID := f.post(t, sqlDB, "out", 30000)
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: f.fundID, TransactionID: &txID, Path: "receipts/nota.jpg", UploadedAt: 2,
	}); err != nil {
		t.Fatalf("attaching a receipt to a posted transaction = %v, want no error", err)
	}

	got, err := q.ListReceiptsByTransaction(ctx, &txID)
	if err != nil {
		t.Fatalf("ListReceiptsByTransaction(%d) = %v, want no error", txID, err)
	}
	if len(got) != 1 || got[0].Path != "receipts/nota.jpg" {
		t.Errorf("ListReceiptsByTransaction(%d) = %+v, want one receipt at receipts/nota.jpg", txID, got)
	}
}

func TestBalancesSumTheLedgerAndLandAsInt64(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)
	bankID := createAccount(t, sqlDB, f.fundID, "bank", "Rekening BRI")

	f.post(t, sqlDB, "in", 500000)
	f.post(t, sqlDB, "out", 120000)
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: bankID, PurposeID: f.purposeID,
		Direction: "in", Amount: 250000, OccurredOn: "2026-08-12", Kind: "normal", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateTransaction on the bank account = %v, want no error", err)
	}

	// These assignments are the test: an uncast aggregate comes back as
	// interface{} and stops compiling here rather than at M3 (ADR-024).
	var fundBalance int64
	fundBalance, err := q.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBalance != 630000 {
		t.Errorf("FundBalance() = %d, want 630000", fundBalance)
	}

	var cashBalance int64
	cashBalance, err = q.AccountBalance(ctx, store.AccountBalanceParams{
		FundID: f.fundID, AccountID: f.accountID,
	})
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if cashBalance != 380000 {
		t.Errorf("AccountBalance(cash) = %d, want 380000", cashBalance)
	}

	// An empty fund is zero, not NULL and not an error - COALESCE earns its keep.
	empty := createFund(t, sqlDB, "Fund kosong", "bcdefghijklmnopqrstuvw")
	var emptyBalance int64
	emptyBalance, err = q.FundBalance(ctx, empty)
	if err != nil {
		t.Fatalf("FundBalance() on an empty fund = %v, want no error", err)
	}
	if emptyBalance != 0 {
		t.Errorf("FundBalance() on an empty fund = %d, want 0", emptyBalance)
	}
}

func TestOutstandingReimbursementsExcludeSettledAndWaived(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Kas RT", validSlug)

	outstanding := createReimbursement(t, sqlDB, f.fundID, f.memberID, f.purposeID, 2000)
	settled := createReimbursement(t, sqlDB, f.fundID, f.memberID, f.purposeID, 3000)
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 3000, OccurredOn: "2026-08-12", Kind: "reimbursement",
		ReimbursementID: &settled, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("settling a reimbursement = %v, want no error", err)
	}

	waivedOn := "2026-08-10"
	if _, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: f.purposeID, Amount: 5000,
		IncurredOn: "2026-08-01", WaivedOn: &waivedOn, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateReimbursement (waived) = %v, want no error", err)
	}

	got, err := q.ListOutstandingReimbursementsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListOutstandingReimbursementsByFund() = %v, want no error", err)
	}
	if len(got) != 1 || got[0].ID != outstanding {
		t.Errorf("outstanding reimbursements = %+v, want only id %d", got, outstanding)
	}

	var total int64
	total, err = q.OutstandingReimbursementTotal(ctx, f.fundID)
	if err != nil {
		t.Fatalf("OutstandingReimbursementTotal() = %v, want no error", err)
	}
	if total != 2000 {
		t.Errorf("OutstandingReimbursementTotal() = %d, want 2000", total)
	}
}

func TestReimbursementRejectsAnotherFundsMemberOrPurpose(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	a := newLedgerFixture(t, sqlDB, "Fund A", validSlug)
	b := newLedgerFixture(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")

	if _, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: a.fundID, MemberID: b.memberID, PurposeID: a.purposeID, Amount: 2000,
		IncurredOn: "2026-08-01", CreatedAt: 1,
	}); err == nil {
		t.Error("reimbursement naming another fund's member = nil error, want the composite FK to reject it")
	}

	if _, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: a.fundID, MemberID: a.memberID, PurposeID: b.purposeID, Amount: 2000,
		IncurredOn: "2026-08-01", CreatedAt: 1,
	}); err == nil {
		t.Error("reimbursement naming another fund's purpose = nil error, want the composite FK to reject it")
	}
}
