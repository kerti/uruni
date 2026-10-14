// rebuild.go is the file-level half of `uruni rebuild` (ADR-012, #516): the
// names, the scratch cleanup and the swap that puts a freshly built database
// file where the old one was. The row-level half is RestoreForRebuild
// (restore.go); the checks and the order they run in live in
// cmd/uruni/rebuild.go.

package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RebuildScratchPrefix starts the name of every file a rebuild writes into the
// database's directory before the swap: the build database and its sidecars,
// the finished copy, the temp files WriteFileAtomic makes. All of it is the
// rebuild's own, so a prefix sweep (RemoveRebuildScratch) is safe while the
// instance lock is held.
const RebuildScratchPrefix = ".uruni-rebuild-"

// ErrPreRebuildExists is the refusal when the name the old database would be
// kept under is already taken: that file is somebody's way back and is never
// overwritten.
var ErrPreRebuildExists = errors.New("backup: the file the old database would be kept as already exists")

// PreRebuildName is the base name the old database file is copied aside to:
// uruni.db.pre-<to-version>-<date>-<time> (Asia/Jakarta, like a dump's name),
// e.g. uruni.db.pre-v0.9.0-alpha.3-20261014-101500. It sits next to the
// database, on its own volume, so the way back is one rename away.
func PreRebuildName(dbPath, toVersion string, when time.Time) string {
	return fmt.Sprintf("%s.pre-%s-%s", filepath.Base(dbPath), cleanVersion(toVersion), when.In(jakarta).Format("20060102-150405"))
}

// RemoveRebuildScratch deletes every regular file in dir whose name starts
// with RebuildScratchPrefix, and reports how many it removed. It runs at the
// start of a rebuild (an interrupted earlier one leaves its scratch behind)
// and on every failure path.
func RemoveRebuildScratch(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("backup: reading %s: %w", dir, err)
	}
	removed := 0
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), RebuildScratchPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("backup: removing stale rebuild scratch %s: %w", e.Name(), err)
		}
		removed++
	}
	return removed, nil
}

// CheckDatabaseOwner refuses a database file owned by a different user than
// the one running: the file the rebuild renames into place would belong to the
// running user, and the user who serves the instance (the image's nonroot, a
// systemd unit) could then no longer write it. A path that does not exist is
// fine.
func CheckDatabaseOwner(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup: reading %s: %w", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := os.Getuid(); int(st.Uid) != uid {
		return fmt.Errorf("backup: %s is owned by uid %d but this process runs as uid %d; "+
			"run the rebuild as the user that runs the app, or the new file would not be writable by it", path, st.Uid, uid)
	}
	return nil
}

// ReplaceHooks lets a test stand in for the one syscall whose failure matters.
// Production passes the zero value.
type ReplaceHooks struct {
	// Rename replaces os.Rename for the final step only (scratch -> dbPath).
	Rename func(oldPath, newPath string) error
	// SyncDir replaces the directory fsync that follows that rename.
	SyncDir func(dir string) error
}

// ErrSwappedUnsynced is ReplaceDatabase's error when the rename succeeded and
// only the directory fsync after it failed: the new database is the live file,
// so the caller must treat the swap as done (and keep the receipts it wrote),
// not roll anything back.
var ErrSwappedUnsynced = errors.New("backup: the new database is in place but its directory could not be synced")

// ReplaceDatabase puts the finished, self-contained file at scratchPath where
// the database at dbPath is, after keeping the old one at asidePath.
//
// The caller has closed every handle on dbPath. The order is the point:
//
//  1. asidePath must not exist (ErrPreRebuildExists); it is never overwritten.
//  2. The old file is copied to asidePath through the atomic write helper
//     (temp, fsync, rename, directory fsync), so the way back is on disk before
//     anything about the live file changes. A -wal that is still non-empty
//     (the old process did not close cleanly) is kept as asidePath+"-wal", the
//     pair SQLite opens together.
//  3. scratchPath gets the old file's mode.
//  4. The old -wal, -shm and -journal are removed, and the directory is
//     fsynced, so no stale one can come back after a power cut beside the new
//     file and be replayed into it.
//  5. ONE rename, scratchPath onto dbPath, then the directory fsync. dbPath is
//     never absent: before the rename it is the old file, after it the new.
//
// With no old file (a first install), only steps 3 and 5 apply, and the new
// file gets mode 0600. A failure at any step leaves dbPath as it was found
// (apart from sidecars removed in step 4, which a clean close had already
// emptied); removing scratchPath is the caller's.
func ReplaceDatabase(dbPath, scratchPath, asidePath string, hooks ReplaceHooks) error {
	return replaceDatabase(osAtomicOps, hooks, dbPath, scratchPath, asidePath)
}

func replaceDatabase(ops atomicOps, hooks ReplaceHooks, dbPath, scratchPath, asidePath string) error {
	dir := filepath.Dir(dbPath)
	rename, syncDir := ops.rename, ops.syncDir
	if hooks.Rename != nil {
		rename = hooks.Rename
	}
	if hooks.SyncDir != nil {
		syncDir = hooks.SyncDir
	}

	old, err := os.Stat(dbPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Chmod(scratchPath, 0o600); err != nil {
			return fmt.Errorf("backup: setting the mode of the new database: %w", err)
		}
	case err != nil:
		return fmt.Errorf("backup: reading %s: %w", dbPath, err)
	default:
		if _, err := os.Lstat(asidePath); err == nil {
			return fmt.Errorf("%w: %s", ErrPreRebuildExists, asidePath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup: reading %s: %w", asidePath, err)
		}
		if err := copyAside(ops, dbPath, asidePath); err != nil {
			return err
		}
		if wal, err := os.Stat(dbPath + "-wal"); err == nil && wal.Size() > 0 {
			if err := copyAside(ops, dbPath+"-wal", asidePath+"-wal"); err != nil {
				return err
			}
		}
		if err := os.Chmod(scratchPath, old.Mode().Perm()); err != nil {
			return fmt.Errorf("backup: giving the new database the old one's mode: %w", err)
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("backup: removing the old database's %s file: %w", suffix, err)
			}
		}
		if err := ops.syncDir(dir); err != nil {
			return fmt.Errorf("backup: syncing %s after removing the old database's side files: %w", dir, err)
		}
	}

	if err := rename(scratchPath, dbPath); err != nil {
		return fmt.Errorf("backup: putting the new database in place: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("%w: syncing %s: %v", ErrSwappedUnsynced, dir, err) //nolint:errorlint // ErrSwappedUnsynced is what the caller matches
	}
	return nil
}

// copyAside copies src to dst atomically: the final name only ever appears
// holding a complete, fsynced copy.
func copyAside(ops atomicOps, src, dst string) error {
	dir := filepath.Dir(dst)
	err := writeFileAtomic(ops, dir, RebuildScratchPrefix+"aside-*", filepath.Base(dst), func(w io.Writer) error {
		f, err := os.Open(src) //nolint:gosec // src is the operator-configured database path (or its -wal), never request input
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return fmt.Errorf("backup: keeping %s as %s: %w", filepath.Base(src), filepath.Base(dst), err)
	}
	return nil
}

// NewestDate is the date the backup was taken, as BuildPreview shows it.
func NewestDate(doc Document) string { return newestMoment(doc) }
