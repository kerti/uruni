package ledger

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kerti/uruni/internal/money"
)

// The instant the tests hand in as the HTTP layer's clock, in UTC unix seconds:
// the ledger reads no wall clock for a record's life, so the tests control it.
const idemNow int64 = 1_800_000_000

// idemKey and idemFingerprint build values the table's CHECKs accept: a key of
// 16 to 64 characters and a fingerprint of exactly 64.
func idemKey(name string) string         { return name + strings.Repeat("k", 16) }
func idemFingerprint(name string) string { return (name + strings.Repeat("f", 64))[:64] }

func keyed(name string, now int64) *Idempotency {
	return &Idempotency{Key: idemKey(name), Fingerprint: idemFingerprint(name), Now: now, ExpiresAt: now + 86_400}
}

func countTable(t *testing.T, l *Ledger, table string) int {
	t.Helper()
	var n int
	if err := l.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s = %v, want no error", table, err)
	}
	return n
}

func keyedPost(f fixture, amount money.Amount, idem *Idempotency) PostTransactionParams {
	return PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: amount, OccurredOn: "2026-08-12", Idempotency: idem,
	}
}

// ADR-043: the same key with the same request answers the first result, by
// value, and posts nothing.
func TestPostTransactionReplayReturnsTheFirstResultAndPostsNothing(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	// A note with quotes, markup and an accent, and the largest amount there is:
	// the stored result must round-trip both without loss.
	note := "Kas \"RT\" <5> & Bu Sri\u00e9"
	first := keyedPost(f, money.MaxAmount, keyed("replay", idemNow))
	first.Note = &note
	posted, err := l.PostTransaction(ctx, first)
	if err != nil {
		t.Fatalf("first PostTransaction() = %v, want no error", err)
	}
	if first.Idempotency.Replayed {
		t.Error("the first post was flagged a replay")
	}

	again := keyed("replay", idemNow+30)
	second := keyedPost(f, money.MaxAmount, again)
	second.Note = &note
	replayed, err := l.PostTransaction(ctx, second)
	if err != nil {
		t.Fatalf("repeated PostTransaction() = %v, want no error", err)
	}
	if !reflect.DeepEqual(replayed, posted) {
		t.Errorf("replayed result = %+v, want %+v", replayed, posted)
	}
	if !again.Replayed || again.AgeSeconds != 30 {
		t.Errorf("Replayed, AgeSeconds = %v, %d, want true, 30", again.Replayed, again.AgeSeconds)
	}
	if n := countTable(t, l, `"transaction"`); n != 1 {
		t.Errorf("transactions = %d, want 1: the repeat posted a second row", n)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != money.MaxAmount {
		t.Errorf("FundBalance() = %d, %v, want %d: the repeat moved money", got, err, money.MaxAmount)
	}
}

// The same key under another request is a 409 at the wire; here, the sentinel,
// with nothing written and the first record untouched.
func TestPostTransactionKeyReusedForAnotherRequestIsRefused(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostTransaction(ctx, keyedPost(f, 10_000, keyed("reuse", idemNow))); err != nil {
		t.Fatalf("first PostTransaction() = %v, want no error", err)
	}

	other := keyed("reuse", idemNow+5)
	other.Fingerprint = idemFingerprint("another")
	_, err := l.PostTransaction(ctx, keyedPost(f, 20_000, other))
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("PostTransaction() with another fingerprint = %v, want ErrIdempotencyKeyReused", err)
	}
	if other.Replayed {
		t.Error("a refused reuse was flagged a replay")
	}
	if n := countTable(t, l, `"transaction"`); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
	if n := countTable(t, l, "idempotency_key"); n != 1 {
		t.Errorf("idempotency_key rows = %d, want 1", n)
	}
}

// A record answers until expires_at and not a second after it: at expires_at it
// is expired (the boundary), and a write under the same key then goes through
// over the expired row without tripping the primary key.
func TestPostTransactionRetentionBoundary(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	first := keyed("window", idemNow)
	if _, err := l.PostTransaction(ctx, keyedPost(f, 10_000, first)); err != nil {
		t.Fatalf("first PostTransaction() = %v, want no error", err)
	}

	justBefore := keyed("window", first.ExpiresAt-1)
	if _, err := l.PostTransaction(ctx, keyedPost(f, 10_000, justBefore)); err != nil {
		t.Fatalf("PostTransaction() one second before expiry = %v, want no error", err)
	}
	if !justBefore.Replayed {
		t.Error("one second before expires_at the record did not answer")
	}
	if n := countTable(t, l, `"transaction"`); n != 1 {
		t.Fatalf("transactions = %d, want 1 after the replay", n)
	}

	atExpiry := keyed("window", first.ExpiresAt)
	if _, err := l.PostTransaction(ctx, keyedPost(f, 10_000, atExpiry)); err != nil {
		t.Fatalf("PostTransaction() at expires_at = %v, want no error: an expired row of the same key must not trip the primary key", err)
	}
	if atExpiry.Replayed {
		t.Error("at expires_at the record still answered; expires_at equal to now is expired")
	}
	if n := countTable(t, l, `"transaction"`); n != 2 {
		t.Errorf("transactions = %d, want 2: an expired key is a new request", n)
	}
	if n := countTable(t, l, "idempotency_key"); n != 1 {
		t.Errorf("idempotency_key rows = %d, want 1: the expired row was replaced, not kept beside it", n)
	}
}

// The purge rides the write that needs the room: a keyed write that finds no
// record deletes every expired row, other keys' included, and leaves live ones.
func TestPostTransactionPurgesEveryExpiredRecord(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	for _, name := range []string{"oldone", "oldtwo"} {
		if _, err := l.PostTransaction(ctx, keyedPost(f, 1_000, keyed(name, idemNow))); err != nil {
			t.Fatalf("PostTransaction(%s) = %v, want no error", name, err)
		}
	}
	live := keyed("live", idemNow+43_200)
	if _, err := l.PostTransaction(ctx, keyedPost(f, 1_000, live)); err != nil {
		t.Fatalf("PostTransaction(live) = %v, want no error", err)
	}
	if n := countTable(t, l, "idempotency_key"); n != 3 {
		t.Fatalf("idempotency_key rows = %d, want 3 before the purge", n)
	}

	// 24 hours and a second after the first two were made: they expired, "live" did not.
	if _, err := l.PostTransaction(ctx, keyedPost(f, 1_000, keyed("fresh", idemNow+86_401))); err != nil {
		t.Fatalf("PostTransaction(fresh) = %v, want no error", err)
	}
	var keys []string
	rows, err := l.db.Query(`SELECT key FROM idempotency_key ORDER BY key`)
	if err != nil {
		t.Fatalf("reading the keys = %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scanning a key = %v", err)
		}
		keys = append(keys, k)
	}
	want := []string{idemKey("fresh"), idemKey("live")}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys after the purge = %v, want %v", keys, want)
	}
}

// An unkeyed post records nothing: nil Idempotency is every existing caller.
func TestPostTransactionWithoutAKeyRecordsNothing(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	for range 2 {
		if _, err := l.PostTransaction(context.Background(), keyedPost(f, 1_000, nil)); err != nil {
			t.Fatalf("PostTransaction() = %v, want no error", err)
		}
	}
	if n := countTable(t, l, "idempotency_key"); n != 0 {
		t.Errorf("idempotency_key rows = %d, want 0", n)
	}
	if n := countTable(t, l, `"transaction"`); n != 2 {
		t.Errorf("transactions = %d, want 2: with no key a repeat is two rows", n)
	}
}

// Only a commit records. A refused write leaves no key, so its retry is judged
// afresh against the state of the day.
func TestPostTransactionRefusalRecordsNoKey(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	bad := keyedPost(f, 10_000, keyed("refused", idemNow))
	bad.AccountID = 999_999 // no such location
	if _, err := l.PostTransaction(ctx, bad); err == nil {
		t.Fatal("PostTransaction() on an unknown account = nil, want an error")
	}
	if n := countTable(t, l, "idempotency_key"); n != 0 {
		t.Fatalf("idempotency_key rows = %d after a refusal, want 0", n)
	}

	retry := keyed("refused", idemNow+10)
	if _, err := l.PostTransaction(ctx, keyedPost(f, 10_000, retry)); err != nil {
		t.Fatalf("retry PostTransaction() = %v, want no error", err)
	}
	if retry.Replayed {
		t.Error("the retry of a refused write was flagged a replay")
	}
	if n := countTable(t, l, `"transaction"`); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
}

// The crash-between test (ADR-043): the key and the row land together or not at
// all. The insert is handed a record the table's own CHECK refuses (expires_at
// not after created_at), and the row the write had already made goes with it.
func TestPostTransactionRecordFailureRollsTheWriteBack(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	idem := keyed("crash", idemNow)
	idem.ExpiresAt = idem.Now // the CHECK wants expires_at > created_at
	if _, err := l.PostTransaction(ctx, keyedPost(f, 10_000, idem)); err == nil {
		t.Fatal("PostTransaction() with a record the table refuses = nil, want an error")
	}
	if n := countTable(t, l, `"transaction"`); n != 0 {
		t.Errorf("transactions = %d, want 0: the write must roll back with the record", n)
	}
	if n := countTable(t, l, "idempotency_key"); n != 0 {
		t.Errorf("idempotency_key rows = %d, want 0", n)
	}
	if got, err := l.FundBalance(ctx, f.fundID); err != nil || got != 0 {
		t.Errorf("FundBalance() = %d, %v, want 0", got, err)
	}
}

// Twenty requests on one key, at once: one row, nineteen replays. The single
// connection runs them one at a time and the later ones find the first's record,
// so there is no in-progress state (ADR-043). Run under -race by make check.
func TestPostTransactionConcurrentTwinsPostOnce(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	const twins = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		replays int
		failed  []error
	)
	for range twins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idem := keyed("twins", idemNow)
			_, err := l.PostTransaction(ctx, keyedPost(f, 10_000, idem))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, err)
			}
			if idem.Replayed {
				replays++
			}
		}()
	}
	wg.Wait()

	if len(failed) != 0 {
		t.Fatalf("%d twins failed, first: %v", len(failed), failed[0])
	}
	if replays != twins-1 {
		t.Errorf("replays = %d, want %d", replays, twins-1)
	}
	if n := countTable(t, l, `"transaction"`); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
}

// A replay re-runs argument validation but not the business-state checks inside
// the transaction: a retry of a post that succeeded is answered even when the
// envelope it went to has closed since.
func TestPostTransactionReplayIsAnsweredAfterTheEnvelopeClosed(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-08-01")
	req := keyedPost(f, 10_000, keyed("closed", idemNow))
	req.PurposeID = envelope.PurposeID
	posted, err := l.PostTransaction(ctx, req)
	if err != nil {
		t.Fatalf("first PostTransaction() = %v, want no error", err)
	}
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-20",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	again := keyedPost(f, 10_000, keyed("closed", idemNow+1))
	again.PurposeID = envelope.PurposeID
	replayed, err := l.PostTransaction(ctx, again)
	if err != nil {
		t.Fatalf("repeated PostTransaction() = %v, want the stored answer", err)
	}
	if !reflect.DeepEqual(replayed, posted) {
		t.Errorf("replayed = %+v, want %+v", replayed, posted)
	}

	// The same body under no key is judged by today's state, and refused.
	if _, err := l.PostTransaction(ctx, func() PostTransactionParams { p := again; p.Idempotency = nil; return p }()); !errors.Is(err, ErrIncidentalClosed) {
		t.Errorf("unkeyed post to a closed envelope = %v, want ErrIncidentalClosed", err)
	}
}
