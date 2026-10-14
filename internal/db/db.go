// Package db owns the SQLite connection and the schema migrations that shape
// it: the plumbing under the store (internal/store holds the sqlc-generated
// queries, ADR-005).
//
// SQLite is the only engine through 0.x (ADR-004): no DATABASE_URL, no dialect
// abstraction, nothing written to be portable.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	// The pure-Go driver, registered as "sqlite": it lets the release image
	// build with CGO_ENABLED=0 and cross-compile for linux/arm64 without QEMU
	// (ADR-004); a cgo driver like mattn/go-sqlite3 would cost both.
	_ "modernc.org/sqlite"
)

// driverName is what modernc.org/sqlite registers itself as.
const driverName = "sqlite"

// pragmas are set on every connection via the DSN; the driver replays them
// each time the pool opens one. The set is ADR-004's:
//
//   - journal_mode=WAL: readers don't block the writer, so the unauthenticated
//     public report is served while the treasurer records.
//   - busy_timeout=5000: belt to SetMaxOpenConns(1)'s braces; a second process
//     (a backup reading the file, goose in another terminal) waits rather than
//     failing.
//   - foreign_keys=ON: SQLite leaves it off by default, per connection; the
//     ledger's references are real only if it is on.
//   - synchronous=NORMAL: safe under WAL (a crash cannot corrupt the database,
//     at worst losing the last commit to a full OS crash). FULL would fsync
//     every commit for a fund recording a few entries a week.
//
// Verified by querying them back in db_test.go: SQLite silently ignores a typo
// in a pragma name.
var pragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"foreign_keys(ON)",
	"synchronous(NORMAL)",
}

// Open opens the database at path, creating the file if absent, and verifies
// the connection. Nothing here migrates: callers run Up, so `migrate status`
// can report on a database it has not changed.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	sqlDB, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("opening the database at %s: %w", path, err)
	}

	// One connection, so every statement serializes and SQLITE_BUSY is
	// structurally impossible, not something the ledger retries around (ADR-004).
	// The trade is real: a public report request can queue behind a write. For a
	// few dozen members those reads are sub-millisecond; if latency proves
	// otherwise, the upgrade is a split writer(1)/reader(N) pool under WAL.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// sql.Open is lazy: without this an unwritable path or corrupt file surfaces
	// at the first query, not at boot where the operator is reading logs.
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("opening the database at %s: %w", path, err)
	}

	return sqlDB, nil
}

// OpenReadOnly opens an existing database file for reading only: it never
// creates the file, never sets a journal mode and so writes nothing, not even
// a -wal or -shm. For a file that must not change while it is checked, such as
// the finished rebuild file before it is swapped in (`uruni rebuild`); it is
// meant for a self-contained rollback-journal file like VACUUM INTO writes,
// not a live WAL database.
func OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	sqlDB, err := sql.Open(driverName, "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("opening the database at %s read-only: %w", path, err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("opening the database at %s read-only: %w", path, err)
	}
	return sqlDB, nil
}

// dsn builds the connection string: the file in SQLite's URI form, with the
// pragmas as query parameters the driver executes on each new connection.
//
// path is passed through as written (the operator's URUNI_DB). A '?' or '#'
// would need percent-encoding to survive URI parsing; no SQLite file is named
// so, and an escaping layer would obscure the DSN in every log line and error.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	return "file:" + path + "?" + q.Encode()
}
