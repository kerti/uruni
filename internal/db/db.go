// Package db owns the SQLite connection and the schema migrations that shape it.
// It is the plumbing under the store: internal/store holds the sqlc-generated
// queries (ADR-005), this package hands them something to run against.
//
// SQLite is the only engine through 0.x (ADR-004) — there is no DATABASE_URL, no
// dialect abstraction, and nothing here is written to be portable.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	// The pure-Go driver, registered as "sqlite". Pure Go is what lets the
	// release image build with CGO_ENABLED=0 and cross-compile for linux/arm64
	// without QEMU (ADR-004); a cgo driver like mattn/go-sqlite3 would cost both.
	_ "modernc.org/sqlite"
)

// driverName is what modernc.org/sqlite registers itself as.
const driverName = "sqlite"

// pragmas are set on every connection, via the DSN — the driver replays them
// each time it opens one, so a connection the pool retires and reopens is
// configured identically. The set is ADR-004's, and each earns its place:
//
//   - journal_mode=WAL — readers don't block the writer, which is what lets the
//     unauthenticated public report be served while the treasurer records.
//   - busy_timeout=5000 — belt to SetMaxOpenConns(1)'s braces: a second process
//     (a backup reading the file, goose in another terminal) waits rather than
//     failing instantly.
//   - foreign_keys=ON — SQLite leaves this *off* by default, per connection. The
//     ledger's references are only real if it is on.
//   - synchronous=NORMAL — safe under WAL: a crash cannot corrupt the database,
//     at the cost of possibly losing the last commit to a full OS crash. FULL
//     would fsync every commit for a fund that records a few entries a week.
//
// Verified by querying them back in db_test.go rather than trusted from the DSN:
// a typo in a pragma name is silently ignored by SQLite.
var pragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"foreign_keys(ON)",
	"synchronous(NORMAL)",
}

// Open opens the database at path, creating the file if it does not exist, and
// verifies the connection before returning it. Nothing here migrates — callers
// run Up themselves, so `migrate status` can report on a database it has not
// changed.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	sqlDB, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("opening the database at %s: %w", path, err)
	}

	// One connection, so every statement serializes and SQLITE_BUSY is
	// structurally impossible rather than something the ledger retries around
	// (ADR-004). The trade is real: an unauthenticated report request can queue
	// behind a write. On a fund of a few dozen members those reads are
	// sub-millisecond; if latency ever proves otherwise, the documented upgrade
	// is a split writer(1)/reader(N) pool under WAL.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// sql.Open is lazy — without this an unwritable path or a corrupt file
	// surfaces at the first query instead of at boot, where the operator is
	// still reading the logs.
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("opening the database at %s: %w", path, err)
	}

	return sqlDB, nil
}

// dsn builds the connection string: the file in SQLite's URI form, with the
// pragmas as query parameters the driver executes on each new connection.
//
// path is passed through as written, which is the operator's own value from
// URUNI_DB. A path containing '?' or '#' would need percent-encoding to survive
// URI parsing; no SQLite file is named that way, and inventing an escaping layer
// for it would obscure the DSN in every log line and error.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	return "file:" + path + "?" + q.Encode()
}
