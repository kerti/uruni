// Package lock provides the single-instance advisory file lock that `uruni
// serve` takes at boot (#62).
//
// First-run setup has a domain-level singleton guard: it refuses a second
// fund (ErrFundAlreadyExists). It is not schema-backed (PRD section 6 allows
// more than one fund in the model, so no UNIQUE index exists to collide with,
// unlike ErrReimbursementAlreadySettled or ErrAlreadyReversed in
// internal/ledger/errors.go). So the in-process check is the entire guarantee,
// and ADR-004's SetMaxOpenConns(1) protects one *sql.DB, not one database
// file: nothing stops a second `uruni serve` opening the same file and
// running the same check at once. This package closes that: at most one
// process holds the lock, so at most one is ever in a position to make the
// check.
package lock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned by Acquire when another process holds the lock. A
// sentinel so callers and tests use errors.Is.
var ErrLocked = errors.New("lock: already held by another process")

// flock is unix.Flock, indirected so tests can drive the two failure
// branches below. Neither is reachable with real files: a non-EWOULDBLOCK
// error from an exclusive flock on a valid descriptor, and a failed unlock,
// both mean the kernel refused something it had accepted. They are handled
// because this lock is all that stands between two processes and the
// un-backstopped fund guard (see the package doc): a silent failure is worse
// than refusing to boot.
var flock = unix.Flock

// Lock is an acquired advisory file lock. The zero value is not usable; get
// one from Acquire.
type Lock struct {
	f *os.File
}

// PathFor derives the lock file path from the database path with ".lock"
// appended, beside the database, needing no config variable (ADR-019: the
// table grows only when a value cannot be derived). For the default URUNI_DB
// that is "./uruni.db.lock".
func PathFor(dbPath string) string {
	return dbPath + ".lock"
}

// Acquire takes an exclusive, non-blocking advisory lock on path, creating
// the file if absent. It returns an error wrapping ErrLocked if another live
// process holds it.
//
// flock, not a pidfile, on purpose: the lock lives on the open descriptor, so
// the kernel drops it when the holder exits for any reason (clean shutdown, a
// panic, or the SIGKILL `make server-stop` escalates to after its grace
// period, see Makefile). A pidfile has to notice and clean a stale entry;
// flock cannot wedge a restart with a lock the OS already reclaimed.
func Acquire(path string) (*Lock, error) {
	//nolint:gosec // path is derived from URUNI_DB, an operator-set config
	// value read once at boot (internal/config), not from request input -
	// the same trust boundary internal/db.Open already opens the database
	// file itself under.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}

	if err := flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s - another instance of uruni is already running against this database", ErrLocked, path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	return &Lock{f: f}, nil
}

// Release releases the lock and closes the descriptor. The lock file stays:
// flock locks an inode, not a filename, so the next Acquire reopens and locks
// it; deleting it would invite a race with a process that opened it a moment
// earlier and is waiting to lock it.
//
// Calling Release twice is a no-op, as a safety property: the first call
// closes the descriptor and the kernel may hand that number to the next file
// opened in the process. An unguarded second call would unlock that file, a
// stranger's lock released by a caller tidying its own. Serve releases
// through a defer, so an explicit Release followed by the deferred one is
// ordinary and must be safe.
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
