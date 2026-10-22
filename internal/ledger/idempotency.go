package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// Idempotency is the key a keyed write rides in on (ADR-043). A nil
// *Idempotency means unkeyed, which is every caller but the HTTP layer's
// keyed routes: tests, the CLI seed and restore.
//
// The HTTP layer fills the first four fields; the ledger fills the last two when
// the answer is a replay. A caller therefore reads Replayed only after the call
// returned nil.
//
// It rides inside the params, not around the call, because a caller's own
// transaction would make the ledger's BeginTx wait for the only connection: the
// check, the write and the record must share the ledger's withTx (ADR-043).
type Idempotency struct {
	Key string
	// Fingerprint is the SHA-256 of the request, 64 hex characters (the HTTP
	// layer's to compute). The same key with another fingerprint is
	// ErrIdempotencyKeyReused.
	Fingerprint string
	// Now and ExpiresAt are UTC unix seconds: the HTTP layer's clock when the
	// request arrived, and the instant the record stops answering. A record is
	// expired from ExpiresAt on, not after it.
	Now       int64
	ExpiresAt int64

	// Replayed is set when the result is the stored one of an earlier identical
	// request and nothing was written. AgeSeconds is how long ago that was.
	Replayed   bool
	AgeSeconds int64
}

// withIdempotency runs write in the ledger's own transaction and, when idem is
// non-nil, makes it safe to repeat (ADR-043):
//
//  1. Check. A live record for the key with the same fingerprint is decoded into
//     T and returned, write never running and nothing changing. With another
//     fingerprint it is ErrIdempotencyKeyReused. With none, every expired record
//     is purged (which also clears an expired record of this very key, which a
//     plain insert would trip over) and the write goes on.
//  2. Write, exactly as an unkeyed call does.
//  3. Record the result as JSON.
//  4. Commit: the rows and the record land together or not at all. A record the
//     table refuses rolls the write back.
//
// The result is stored as the Go value the method returns, not as the HTTP
// reply, so T must be a store type (integers, strings and pointers to them),
// which round-trips through JSON without loss. write takes the transaction's
// own querier, the ...Tx idiom, never a second withTx.
//
// No "in progress" state is needed: under ADR-004's single connection a
// concurrent twin runs after the first has committed and finds its record. If a
// writer/reader split ever lands, the check must run in a write-locking
// (BEGIN IMMEDIATE) transaction instead.
func withIdempotency[T any](ctx context.Context, l *Ledger, idem *Idempotency, write func(q store.Querier) (T, error)) (T, error) {
	var result T
	err := l.withTx(ctx, func(q store.Querier) error {
		if idem == nil {
			var err error
			result, err = write(q)
			return err
		}

		rec, err := q.GetIdempotencyKey(ctx, store.GetIdempotencyKeyParams{Key: idem.Key, Now: idem.Now})
		switch {
		case err == nil:
			if rec.Fingerprint != idem.Fingerprint {
				return ErrIdempotencyKeyReused
			}
			var stored T
			if err := json.Unmarshal(rec.Result, &stored); err != nil {
				return fmt.Errorf("decoding the stored result of an idempotency key: %w", err)
			}
			result = stored
			idem.Replayed = true
			idem.AgeSeconds = max(idem.Now-rec.CreatedAt, 0)
			return nil
		case errors.Is(err, sql.ErrNoRows):
			if err := q.DeleteExpiredIdempotencyKeys(ctx, idem.Now); err != nil {
				return fmt.Errorf("purging expired idempotency keys: %w", err)
			}
		default:
			return fmt.Errorf("reading an idempotency key: %w", err)
		}

		result, err = write(q)
		if err != nil {
			return err
		}

		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encoding the result for an idempotency key: %w", err)
		}
		if err := q.CreateIdempotencyKey(ctx, store.CreateIdempotencyKeyParams{
			Key: idem.Key, Fingerprint: idem.Fingerprint, Result: encoded, CreatedAt: idem.Now, ExpiresAt: idem.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("recording an idempotency key: %w", err)
		}
		return nil
	})
	if err != nil {
		if idem != nil {
			idem.Replayed = false // a replay whose commit failed answered nothing
		}
		var zero T
		return zero, err
	}
	return result, nil
}
