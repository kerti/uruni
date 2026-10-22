package ledger

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kerti/uruni/internal/money"
)

// ADR-043 on the seven routes keyed after POST /api/transactions: each
// answers a repeat of the same key and request with the first result, equal by
// value (which is also the round trip of the stored type), and writes nothing.
// idempotency_test.go holds the wrapper's own cases (retention, purge, reuse,
// crash-between, twins) on PostTransaction; these are the per-method ones.

// countWhere counts the rows of table matching where, a fixed clause the test
// wrote.
func countWhere(t *testing.T, l *Ledger, table, where string) int {
	t.Helper()
	var n int
	if err := l.db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE ` + where).Scan(&n); err != nil {
		t.Fatalf("counting %s where %s = %v, want no error", table, where, err)
	}
	return n
}

// requireReplay checks the shape every replay shares: flagged, aged, and equal
// by value to the first answer.
func requireReplay[T any](t *testing.T, first, replayed T, again *Idempotency) {
	t.Helper()
	if !reflect.DeepEqual(replayed, first) {
		t.Errorf("replayed result = %+v, want %+v", replayed, first)
	}
	if !again.Replayed || again.AgeSeconds != 30 {
		t.Errorf("Replayed, AgeSeconds = %v, %d, want true, 30", again.Replayed, again.AgeSeconds)
	}
}

// A payment over three periods is one result of three rows. The replay answers
// all three, by value and in order, and posts no second set.
func TestPostDuesPaymentsReplayAnswersEveryPeriodAsOneResult(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	note := "Iuran \"Q3\" <rt> & Bu Sri\u00e9"
	build := func(idem *Idempotency) PostDuesPaymentsParams {
		return PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-08-12", Note: &note,
			Periods: []PeriodAmount{
				{DuesPeriod: "2026-06", Amount: 50_000},
				{DuesPeriod: "2026-07", Amount: 50_000},
				{DuesPeriod: "2026-08", Amount: money.MaxAmount - 100_000},
			},
			Idempotency: idem,
		}
	}

	first := keyed("dues", idemNow)
	posted, err := l.PostDuesPayments(ctx, build(first))
	if err != nil {
		t.Fatalf("first PostDuesPayments() = %v, want no error", err)
	}
	if first.Replayed {
		t.Error("the first payment was flagged a replay")
	}
	balance, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}

	again := keyed("dues", idemNow+30)
	replayed, err := l.PostDuesPayments(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated PostDuesPayments() = %v, want no error", err)
	}
	if len(replayed) != 3 {
		t.Fatalf("replayed rows = %d, want 3: one result for the whole payment", len(replayed))
	}
	requireReplay(t, posted, replayed, again)
	if n := countWhere(t, l, `"transaction"`, `kind = 'dues'`); n != 3 {
		t.Errorf("dues rows = %d, want 3: the repeat posted a second set", n)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != balance {
		t.Errorf("FundBalance() = %d, %v, want %d: the repeat moved money", got, err, balance)
	}
}

func TestPostTransferBetweenAccountsReplayPostsNoSecondPair(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	seedMain(t, l, f, 500_000)

	note := "Setor ke \"bank\" <BCA> & co"
	build := func(idem *Idempotency) PostTransferBetweenAccountsParams {
		return PostTransferBetweenAccountsParams{
			FundID: f.fundID, PurposeID: f.mainID, FromAccountID: f.cashID, ToAccountID: f.bankID,
			Amount: 200_000, OccurredOn: "2026-08-12", Note: &note, Idempotency: idem,
		}
	}

	posted, err := l.PostTransferBetweenAccounts(ctx, build(keyed("transfer", idemNow)))
	if err != nil {
		t.Fatalf("first PostTransferBetweenAccounts() = %v, want no error", err)
	}
	again := keyed("transfer", idemNow+30)
	replayed, err := l.PostTransferBetweenAccounts(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated PostTransferBetweenAccounts() = %v, want no error", err)
	}
	requireReplay(t, posted, replayed, again)

	if n := countTable(t, l, "transfer"); n != 1 {
		t.Errorf("transfers = %d, want 1", n)
	}
	if n := countWhere(t, l, `"transaction"`, `kind = 'transfer'`); n != 2 {
		t.Errorf("transfer legs = %d, want 2", n)
	}
	if got, err := l.AccountBalance(ctx, f.fundID, f.bankID); err != nil || got != 200_000 {
		t.Errorf("AccountBalance(bank) = %d, %v, want 200000: moved once", got, err)
	}
	if got, err := l.AccountBalance(ctx, f.fundID, f.cashID); err != nil || got != 300_000 {
		t.Errorf("AccountBalance(cash) = %d, %v, want 300000: moved once", got, err)
	}
}

// Moving the whole of a purpose's balance leaves nothing for a second move, so
// an unkeyed repeat is ErrPurposeMoveInsufficient; the keyed one is the first
// answer, since the transaction's checks do not run on a replay.
func TestPostPurposeMoveReplayPostsNoSecondPair(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	seedMain(t, l, f, 300_000)

	build := func(idem *Idempotency) PostPurposeMoveParams {
		return PostPurposeMoveParams{
			FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: f.incidenID, AccountID: f.cashID,
			Amount: 300_000, OccurredOn: "2026-08-12", Idempotency: idem,
		}
	}

	posted, err := l.PostPurposeMove(ctx, build(keyed("move", idemNow)))
	if err != nil {
		t.Fatalf("first PostPurposeMove() = %v, want no error", err)
	}
	again := keyed("move", idemNow+30)
	replayed, err := l.PostPurposeMove(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated PostPurposeMove() = %v, want the stored answer", err)
	}
	requireReplay(t, posted, replayed, again)

	if n := countTable(t, l, "transfer"); n != 1 {
		t.Errorf("transfers = %d, want 1", n)
	}
	if n := countWhere(t, l, `"transaction"`, `kind = 'transfer'`); n != 2 {
		t.Errorf("transfer legs = %d, want 2", n)
	}
	if got, err := l.PurposeBalance(ctx, f.fundID, f.incidenID); err != nil || got != 300_000 {
		t.Errorf("PurposeBalance(envelope) = %d, %v, want 300000: moved once", got, err)
	}

	if _, err := l.PostPurposeMove(ctx, build(nil)); !errors.Is(err, ErrPurposeMoveInsufficient) {
		t.Errorf("unkeyed repeat = %v, want ErrPurposeMoveInsufficient", err)
	}
}

// A repeat of a correction that worked is the first answer, not the
// ErrPurposeCorrectionNoop that its own result would earn unkeyed.
func TestPostPurposeCorrectionReplayPostsNoSecondPair(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	seedMain(t, l, f, 500_000)

	wrong, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "out", Amount: 40_000, OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	build := func(idem *Idempotency) PostPurposeCorrectionParams {
		return PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: wrong.ID, PurposeID: f.passID, Idempotency: idem}
	}
	posted, err := l.PostPurposeCorrection(ctx, build(keyed("correct", idemNow)))
	if err != nil {
		t.Fatalf("first PostPurposeCorrection() = %v, want no error", err)
	}
	again := keyed("correct", idemNow+30)
	replayed, err := l.PostPurposeCorrection(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated PostPurposeCorrection() = %v, want the stored answer", err)
	}
	requireReplay(t, posted, replayed, again)

	if n := countTable(t, l, "transfer"); n != 1 {
		t.Errorf("transfers = %d, want 1", n)
	}
	if n := countWhere(t, l, `"transaction"`, `kind = 'transfer'`); n != 2 {
		t.Errorf("correction legs = %d, want 2", n)
	}

	if _, err := l.PostPurposeCorrection(ctx, build(nil)); !errors.Is(err, ErrPurposeCorrectionNoop) {
		t.Errorf("unkeyed repeat = %v, want ErrPurposeCorrectionNoop", err)
	}
}

func TestOpenIncidentalReplayOpensNoSecondEnvelope(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	target := money.Amount(5_000_000)
	minimum := money.Amount(50_000)
	build := func(idem *Idempotency) OpenIncidentalParams {
		return OpenIncidentalParams{
			FundID: f.fundID, Occasion: "Sunatan \"Budi\" <RT 5> & keluarga", OpenedOn: "2026-08-01",
			TargetAmount: &target, MinimumPerMember: &minimum, RecipientMemberIDs: []int64{f.memberID},
			Idempotency: idem,
		}
	}

	opened, err := l.OpenIncidental(ctx, build(keyed("envelope", idemNow)))
	if err != nil {
		t.Fatalf("first OpenIncidental() = %v, want no error", err)
	}
	purposes := countTable(t, l, "purpose")

	again := keyed("envelope", idemNow+30)
	replayed, err := l.OpenIncidental(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated OpenIncidental() = %v, want no error", err)
	}
	requireReplay(t, opened, replayed, again)

	if n := countTable(t, l, "purpose"); n != purposes {
		t.Errorf("purposes = %d, want %d: the repeat opened a second one", n, purposes)
	}
	if n := countWhere(t, l, "incidental", `occasion = 'Sunatan "Budi" <RT 5> & keluarga'`); n != 1 {
		t.Errorf("envelopes of that occasion = %d, want 1", n)
	}
	if n := countTable(t, l, "incidental_recipient"); n != 1 {
		t.Errorf("recipients = %d, want 1", n)
	}
}

// entry_added is the resolution whose repeat did the most harm: a second fix and
// a line reading as an open gap that was not there (ADR-043 finding 3).
func TestTakeReconciliationReplayTakesNoSecondSnapshotAndPostsNoSecondFix(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	seedMain(t, l, f, 80_000)

	note := "Cek \"akhir bulan\" <ok> & Bu Sri\u00e9"
	build := func(idem *Idempotency) TakeReconciliationParams {
		return TakeReconciliationParams{
			FundID: f.fundID, Note: &note, Idempotency: idem,
			Counts: []AccountCount{{
				AccountID: f.cashID, ActualAmount: 100_000, Resolution: "entry_added",
				Fix: &Fix{PurposeID: f.mainID, Direction: "in", Amount: 20_000, OccurredOn: "2026-08-15"},
			}, f.bankZero()},
		}
	}

	snapshot, err := l.TakeReconciliation(ctx, build(keyed("count", idemNow)))
	if err != nil {
		t.Fatalf("first TakeReconciliation() = %v, want no error", err)
	}
	transactions := countTable(t, l, `"transaction"`)

	again := keyed("count", idemNow+30)
	replayed, err := l.TakeReconciliation(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated TakeReconciliation() = %v, want no error", err)
	}
	requireReplay(t, snapshot, replayed, again)

	if n := countTable(t, l, "reconciliation"); n != 1 {
		t.Errorf("reconciliations = %d, want 1", n)
	}
	if n := countTable(t, l, "reconciliation_line"); n != 2 {
		t.Errorf("reconciliation lines = %d, want 2", n)
	}
	if n := countTable(t, l, `"transaction"`); n != transactions {
		t.Errorf("transactions = %d, want %d: the repeat posted a second fix", n, transactions)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != 100_000 {
		t.Errorf("FundBalance() = %d, %v, want 100000: the fix landed once", got, err)
	}

	// What the handler reads after the commit is the snapshot's detail: the same
	// for the replay as for the first answer.
	detail, err := l.GetReconciliationDetail(ctx, f.fundID, replayed.ID)
	if err != nil {
		t.Fatalf("GetReconciliationDetail() on the replay = %v, want no error", err)
	}
	if len(detail.Lines) != 2 {
		t.Errorf("detail lines = %d, want 2", len(detail.Lines))
	}
}

// A location with a starting balance is the keyed create that moves money: a
// replay leaves one account, exactly one opening row, and the fund as the first
// call left it.
func TestCreateAccountReplayWithAStartingBalanceWritesNoSecondOpeningRow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	note := "Saldo \"awal\" <BRI> & co"
	build := func(idem *Idempotency) CreateAccountParams {
		return CreateAccountParams{
			FundID: f.fundID, Kind: "bank", Name: "Bank \"BRI\" & Co",
			OpeningBalance: &OpeningBalance{Amount: 500_000, OccurredOn: "2026-08-01", Note: &note},
			Idempotency:    idem,
		}
	}

	accounts := countTable(t, l, "account")
	created, err := l.CreateAccount(ctx, build(keyed("account", idemNow)))
	if err != nil {
		t.Fatalf("first CreateAccount() = %v, want no error", err)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != 500_000 {
		t.Fatalf("FundBalance() after the first = %d, %v, want 500000", got, err)
	}

	again := keyed("account", idemNow+30)
	replayed, err := l.CreateAccount(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated CreateAccount() = %v, want no error", err)
	}
	requireReplay(t, created, replayed, again)

	if n := countTable(t, l, "account"); n != accounts+1 {
		t.Errorf("accounts = %d, want %d: the repeat made a second location", n, accounts+1)
	}
	if n := countWhere(t, l, `"transaction"`, `kind = 'opening'`); n != 1 {
		t.Errorf("opening rows = %d, want exactly 1", n)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != 500_000 {
		t.Errorf("FundBalance() = %d, %v, want 500000: the repeat doubled the money", got, err)
	}
	if got, err := l.AccountBalance(ctx, f.fundID, created.ID); err != nil || got != 500_000 {
		t.Errorf("AccountBalance(new) = %d, %v, want 500000", got, err)
	}
}

// The route is keyed whatever the body: a create without a starting balance
// replays equal and spares an empty duplicate.
func TestCreateAccountReplayWithoutAStartingBalanceIsEqual(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	build := func(idem *Idempotency) CreateAccountParams {
		return CreateAccountParams{FundID: f.fundID, Kind: "cash", Name: "Kas kecil", Idempotency: idem}
	}

	accounts := countTable(t, l, "account")
	created, err := l.CreateAccount(ctx, build(keyed("plain", idemNow)))
	if err != nil {
		t.Fatalf("first CreateAccount() = %v, want no error", err)
	}
	again := keyed("plain", idemNow+30)
	replayed, err := l.CreateAccount(ctx, build(again))
	if err != nil {
		t.Fatalf("repeated CreateAccount() = %v, want no error", err)
	}
	requireReplay(t, created, replayed, again)

	if n := countTable(t, l, "account"); n != accounts+1 {
		t.Errorf("accounts = %d, want %d", n, accounts+1)
	}
	if n := countWhere(t, l, `"transaction"`, `kind = 'opening'`); n != 0 {
		t.Errorf("opening rows = %d, want 0", n)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != 0 {
		t.Errorf("FundBalance() = %d, %v, want 0", got, err)
	}
}

// A starting balance the ledger refuses is refused before the transaction opens,
// as an unkeyed create is, and under a key leaves no record: the retry with a
// corrected balance is a first write, not a 409.
func TestCreateAccountRefusedOpeningBalanceRecordsNoKey(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	accounts := countTable(t, l, "account")
	bad := CreateAccountParams{
		FundID: f.fundID, Kind: "bank", Name: "Bank baru",
		OpeningBalance: &OpeningBalance{Amount: -1, OccurredOn: "2026-08-01"},
		Idempotency:    keyed("refused", idemNow),
	}
	if _, err := l.CreateAccount(ctx, bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateAccount() with a negative balance = %v, want ErrInvalidArgument", err)
	}
	if n := countTable(t, l, "account"); n != accounts {
		t.Errorf("accounts = %d, want %d", n, accounts)
	}
	if n := countTable(t, l, "idempotency_key"); n != 0 {
		t.Errorf("idempotency_key rows = %d, want 0", n)
	}
}

// The same key under another request is ErrIdempotencyKeyReused on a new route
// as on the first, with nothing written beyond the first call.
func TestCreateAccountKeyReusedForAnotherRequestIsRefused(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.CreateAccount(ctx, CreateAccountParams{
		FundID: f.fundID, Kind: "cash", Name: "Kas kecil", Idempotency: keyed("reuse", idemNow),
	}); err != nil {
		t.Fatalf("first CreateAccount() = %v, want no error", err)
	}
	accounts := countTable(t, l, "account")

	other := keyed("reuse", idemNow+5)
	other.Fingerprint = idemFingerprint("another")
	_, err := l.CreateAccount(ctx, CreateAccountParams{
		FundID: f.fundID, Kind: "cash", Name: "Kas besar", Idempotency: other,
	})
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("CreateAccount() with another fingerprint = %v, want ErrIdempotencyKeyReused", err)
	}
	if n := countTable(t, l, "account"); n != accounts {
		t.Errorf("accounts = %d, want %d", n, accounts)
	}
}
