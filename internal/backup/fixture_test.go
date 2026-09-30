package backup

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/store"
)

// newTestDB is the same recipe internal/ledger's and internal/http's own
// fixture helpers use: a real, migrated in-memory database, so the export
// runs against genuine SQLite result codes and triggers rather than a fake.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(\":memory:\") = %v, want no error", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("Close() = %v, want no error", err)
		}
	})
	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return sqlDB
}

// receiptFilename is fixed, not crypto/rand-generated, so the golden fixture
// stays byte-identical across regenerations - production code
// (internal/http/receipts.go's randomReceiptFilename) still generates a
// real random name; only this test fixture pins one.
const receiptFilename = "0000000000000000000000000000ff.jpg"

// receiptFileBytes is the stub content written to uploadsDir under
// receiptFilename and asserted, byte for byte, inside the zip's own
// receipts/ entry - not a real JPEG, since Export never decodes it, only
// copies it.
var receiptFileBytes = []byte("stub receipt bytes for the golden fixture")

// buildFixture writes one complete, deterministic fund through store.Queries
// directly - not through internal/ledger - because every ledger write
// stamps CreatedAt with time.Now() (no injected clock exists yet), which
// would make a byte-exact golden comparison flaky by the second. Inserting
// through the same queries the ledger itself uses, in the schema's own
// dependency order (00001_schema.sql; see backup.go's own comment), is
// exactly what ADR-012 already says an importer must do - this is that
// order, exercised once, by hand, for a shape-pinning test rather than a
// restore.
//
// It deliberately touches every table this package exports at least once,
// including the two ADR-034 added most recently (incidental_recipient,
// incidental.minimum_per_member) - the fixture's whole job is to pin their
// shape in the export too, not just the tables that existed at M2.
func buildFixture(t *testing.T, sqlDB *sql.DB, uploadsDir string) {
	t.Helper()
	ctx := context.Background()
	q := store.New(sqlDB)

	if err := os.WriteFile(filepath.Join(uploadsDir, receiptFilename), receiptFileBytes, 0o600); err != nil {
		t.Fatalf("writing fixture receipt file: %v", err)
	}

	//nolint:gosec // not a credential - a fixed, publicly known fixture
	// string for a throwaway in-memory test database, matching
	// cmd/uruni/seed_e2e.go's own reasoning for its fixture password.
	if _, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "treasurer@example.org", PasswordHash: "argon2id$fake$hash", CreatedAt: 1700000000,
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", ReportSlug: "0000000000000000000fixture", CreatedAt: 1700000000,
	})
	if err != nil {
		t.Fatalf("CreateFund: %v", err)
	}

	cash, err := q.CreateAccount(ctx, store.CreateAccountParams{FundID: fund.ID, Kind: "cash", Name: "Tunai", CreatedAt: 1700000000})
	if err != nil {
		t.Fatalf("CreateAccount(cash): %v", err)
	}
	bank, err := q.CreateAccount(ctx, store.CreateAccountParams{FundID: fund.ID, Kind: "bank", Name: "Bank", CreatedAt: 1700000000})
	if err != nil {
		t.Fatalf("CreateAccount(bank): %v", err)
	}

	main, err := q.CreatePurpose(ctx, store.CreatePurposeParams{FundID: fund.ID, Kind: "main", Name: "Kas Utama", CreatedAt: 1700000000})
	if err != nil {
		t.Fatalf("CreatePurpose(main): %v", err)
	}
	passThrough, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fund.ID, Kind: "pass_through", Name: "Kas Bidang", CreatedAt: 1700000000,
	})
	if err != nil {
		t.Fatalf("CreatePurpose(pass_through): %v", err)
	}
	envelope, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fund.ID, Kind: "incidental", Name: "Amplop Contoh", CreatedAt: 1700000000,
	})
	if err != nil {
		t.Fatalf("CreatePurpose(incidental): %v", err)
	}

	tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{FundID: fund.ID, Name: "Pelaksana", CreatedAt: 1700000000})
	if err != nil {
		t.Fatalf("CreateDuesTier: %v", err)
	}
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
		TierID: tier.ID, Amount: 50000, EffectiveFrom: "2026-01", CreatedAt: 1700000000,
	}); err != nil {
		t.Fatalf("CreateDuesRate: %v", err)
	}

	joinedOn := "2026-01-01"
	andi, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fund.ID, Name: "Andi", TierID: &tier.ID, JoinedOn: &joinedOn, CreatedAt: 1700000000,
	})
	if err != nil {
		t.Fatalf("CreateMember(andi): %v", err)
	}
	inactiveOn := "2026-06-01"
	budi, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fund.ID, Name: "Budi", JoinedOn: &joinedOn, InactiveOn: &inactiveOn, CreatedAt: 1700000000,
	})
	if err != nil {
		t.Fatalf("CreateMember(budi): %v", err)
	}

	// Opening balances, one per account.
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "in", Amount: 500000,
		OccurredOn: "2026-01-01", Kind: "opening", CreatedAt: 1700000000,
	}); err != nil {
		t.Fatalf("CreateTransaction(opening cash): %v", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: bank.ID, PurposeID: main.ID, Direction: "in", Amount: 1000000,
		OccurredOn: "2026-01-01", Kind: "opening", CreatedAt: 1700000001,
	}); err != nil {
		t.Fatalf("CreateTransaction(opening bank): %v", err)
	}

	// A dues payment, then its reversal (ADR-029) - pins kind=dues and
	// kind=adjustment/reverses_transaction_id in the same fixture.
	duesPeriod := "2026-01"
	duesPayment, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "in", Amount: 50000,
		OccurredOn: "2026-01-10", Kind: "dues", MemberID: &andi.ID, DuesPeriod: &duesPeriod, CreatedAt: 1700000100,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(dues): %v", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "out", Amount: 50000,
		OccurredOn: "2026-01-11", Kind: "adjustment", MemberID: &andi.ID, DuesPeriod: &duesPeriod,
		ReversesTransactionID: &duesPayment.ID, CreatedAt: 1700000101,
	}); err != nil {
		t.Fatalf("CreateTransaction(dues reversal): %v", err)
	}

	// A named contribution to the envelope (ADR-034).
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: cash.ID, PurposeID: envelope.ID, Direction: "in", Amount: 30000,
		OccurredOn: "2026-02-01", Kind: "normal", MemberID: &andi.ID, CreatedAt: 1700000200,
	}); err != nil {
		t.Fatalf("CreateTransaction(contribution): %v", err)
	}

	// An ordinary, unnamed pass-through movement.
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: bank.ID, PurposeID: passThrough.ID, Direction: "in", Amount: 75000,
		OccurredOn: "2026-02-02", Kind: "normal", CreatedAt: 1700000201,
	}); err != nil {
		t.Fatalf("CreateTransaction(pass-through): %v", err)
	}

	// A transfer: cash to bank.
	transfer, err := q.CreateTransfer(ctx, store.CreateTransferParams{FundID: fund.ID, Kind: "between_accounts", CreatedAt: 1700000300})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "out", Amount: 100000,
		OccurredOn: "2026-02-03", Kind: "transfer", TransferID: &transfer.ID, CreatedAt: 1700000301,
	}); err != nil {
		t.Fatalf("CreateTransaction(transfer out): %v", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: bank.ID, PurposeID: main.ID, Direction: "in", Amount: 100000,
		OccurredOn: "2026-02-03", Kind: "transfer", TransferID: &transfer.ID, CreatedAt: 1700000302,
	}); err != nil {
		t.Fatalf("CreateTransaction(transfer in): %v", err)
	}

	// A reimbursement, settled, with a receipt attached to the claim.
	note := "Parkir"
	claim, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: fund.ID, MemberID: andi.ID, PurposeID: main.ID, Amount: 20000,
		IncurredOn: "2026-01-05", Note: &note, CreatedAt: 1700000400,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement: %v", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fund.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "out", Amount: 20000,
		OccurredOn: "2026-01-06", Kind: "reimbursement", ReimbursementID: &claim.ID, CreatedAt: 1700000401,
	}); err != nil {
		t.Fatalf("CreateTransaction(reimbursement settle): %v", err)
	}
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: fund.ID, ReimbursementID: &claim.ID, Path: receiptFilename, UploadedAt: 1700000500,
	}); err != nil {
		t.Fatalf("CreateReceipt: %v", err)
	}

	// A reconciliation with one matched and one left-open line.
	recon, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: fund.ID, PerformedAt: 1700001000, Note: nil, CreatedAt: 1700001000,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation: %v", err)
	}
	// Cash: 500000 (opening) + 50000 (dues) - 50000 (reversal) + 30000
	// (contribution) - 100000 (transfer out) - 20000 (reimbursement) = 410000.
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: fund.ID, ReconciliationID: recon.ID, AccountID: cash.ID,
		RecordedAmount: 410000, ActualAmount: 410000, DifferenceAmount: 0, Resolution: "matched",
	}); err != nil {
		t.Fatalf("CreateReconciliationLine(cash): %v", err)
	}
	// Bank: 1000000 (opening) + 75000 (pass-through) + 100000 (transfer in) = 1175000.
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: fund.ID, ReconciliationID: recon.ID, AccountID: bank.ID,
		RecordedAmount: 1175000, ActualAmount: 1180000, DifferenceAmount: 5000, Resolution: "left_open",
	}); err != nil {
		t.Fatalf("CreateReconciliationLine(bank): %v", err)
	}

	// The envelope's own row and its one recipient (ADR-034).
	targetAmount := int64(100000)
	minimum := int64(10000)
	if _, err := q.CreateIncidental(ctx, store.CreateIncidentalParams{
		PurposeID: envelope.ID, Occasion: "Contoh amplop", TargetAmount: &targetAmount,
		OpenedOn: "2026-02-01", MinimumPerMember: &minimum, CreatedAt: 1700000150,
	}); err != nil {
		t.Fatalf("CreateIncidental: %v", err)
	}
	if err := q.CreateIncidentalRecipient(ctx, store.CreateIncidentalRecipientParams{
		FundID: fund.ID, PurposeID: envelope.ID, MemberID: budi.ID,
	}); err != nil {
		t.Fatalf("CreateIncidentalRecipient: %v", err)
	}
}
