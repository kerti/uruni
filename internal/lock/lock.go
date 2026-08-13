// Package lock provides the single-instance advisory file lock that `uruni
// serve` takes at boot (issue #62).
//
// M4's next slice adds a domain-level singleton guard: first-run setup
// refuses to create a second fund. That guard is deliberately *not*
// schema-backed — PRD §6 allows more than one fund in the model, so there is
// no UNIQUE index a second CREATE could collide with, unlike
// ErrOpeningBalanceExists or ErrReimbursementAlreadySettled
// (internal/ledger/errors.go), whose Go pre-checks sit on top of one. That
// makes the in-process check the entire guarantee, and ADR-004's
// SetMaxOpenConns(1) protects one *sql.DB, not one database file — nothing
// stops a second `uruni serve` process from opening the same file and
// running the same check at the same time. This package is what closes that:
// at most one process may hold the lock, so at most one process is ever in a
// position to make the domain check at all.
package lock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned by Acquire when another process already holds the
// lock. A sentinel, not a string match, so a caller (or a test) can branch on
// errors.Is rather than parsing the message.
var ErrLocked = errors.New("lock: already held by another process")

// flock is unix.Flock, indirected so the tests can drive the two failure
// branches below. Neither is reachable by any arrangement of real files: a
// non-EWOULDBLOCK error from an exclusive flock on a valid descriptor, and a
// failed unlock, both mean the kernel refused something it had already
// accepted. They are handled rather than ignored because this lock is the
// only thing standing between two processes and the un-backstopped fund
// guard (see the package doc) — a silent failure here is the one outcome
// worse than refusing to boot.
var flock = unix.Flock

// Lock is an acquired advisory file lock. The zero value is not usable —
// obtain one from Acquire.
type Lock struct {
	f *os.File
}

// PathFor derives the lock file path from the configured database path: the
// same path with ".lock" appended, so it sits beside the database file
// rather than needing a config variable of its own (ADR-019 — the runtime
// config table grows only when a value cannot be derived from what is
// already there). For the default URUNI_DB, that is "./uruni.db.lock".
func PathFor(dbPath string) string {
	return dbPath + ".lock"
}

// Acquire takes an exclusive, non-blocking advisory lock on path, creating
// the file if it does not exist. It returns an error wrapping ErrLocked if
// another live process already holds it.
//
// flock, not a pidfile, is the deliberate choice: the lock lives on the open
// file descriptor, so the kernel drops it the instant the holding process
// exits, for any reason — clean shutdown, a panic, or the SIGKILL
// `make server-stop` escalates to once its grace period elapses (Makefile).
// A pidfile scheme has to notice a stale entry and clean it up itself; flock
// needs no such check, which is what keeps a lock the OS already reclaimed
// from ever wedging a restart.
func Acquire(path string) (*Lock, error) {
	//nolint:gosec // path is derived from URUNI_DB, an operator-set config
	// value read once at boot (internal/config), not from request input —
	// the same trust boundary internal/db.Open already opens the database
	// file itself under.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}

	if err := flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s — another instance of uruni is already running against this database", ErrLocked, path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	return &Lock{f: f}, nil
}

// Release releases the lock and closes the underlying file descriptor. The
// lock file itself is left in place — flock locks an inode, not a filename,
// so the next Acquire simply reopens it and takes the lock again; deleting it
// here would only invite a race with a process that opened it a moment
// earlier and is still waiting to lock it.
//
// Calling Release twice is a no-op, and that is a safety property rather than
// a convenience: the first call closes the descriptor, and the kernel is free
// to hand that same number to the next file anything in the process opens. An
// unguarded second call would then unlock *that* file — a stranger's lock,
// released by a caller that believed it was tidying up its own. Since serve
// releases through a defer, the shape that reaches this is an ordinary one
// (an explicit Release followed by the deferred one), so it has to be safe
// rather than merely discouraged.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}

	f := l.f
	l.f = nil

	defer func() { _ = f.Close() }()
	if err := flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("releasing lock %s: %w", f.Name(), err)
	}
	return nil
}
