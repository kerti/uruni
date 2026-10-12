package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// atomicOps are the three steps of WriteFileAtomic whose order is the whole
// point. Production uses osAtomicOps; a test swaps in a recorder to assert
// that the file is synced before the rename and the directory after it.
type atomicOps struct {
	syncFile func(*os.File) error
	rename   func(oldPath, newPath string) error
	syncDir  func(dir string) error
}

var osAtomicOps = atomicOps{
	syncFile: func(f *os.File) error { return f.Sync() },
	rename:   os.Rename,
	syncDir:  syncDirectory,
}

// WriteFileAtomic is the one durable-write shape every file this app must not
// lose goes through: dump zips (writeDump, writeDailyIfNeeded), receipt
// uploads (internal/http) and receipts restored from a backup. fill writes
// the content into a temp file created in dir itself (a rename across
// filesystems is not atomic); the temp file is then
//
//  1. fsynced, so its bytes are on disk before it can ever be named,
//  2. renamed to filepath.Join(dir, name), and
//  3. dir is fsynced, so the rename itself survives a power loss.
//
// Without step 1 a crash can leave the final name pointing at an empty or
// short file - and retention would then prune good dumps in its favour.
// Without step 3 the file can vanish from the directory after a crash even
// though its bytes were safe.
//
// tempPattern is os.CreateTemp's pattern (the "*" is replaced). On any error
// before the rename the temp file is removed. Errors are returned unwrapped
// from the step that failed except for context added here; callers add
// their own. If only step 3 fails the file is already in place under its
// final name: the error is returned (durability is not proven) but nothing
// is rolled back, since deleting a fully written file would be worse.
func WriteFileAtomic(dir, tempPattern, name string, fill func(io.Writer) error) error {
	return writeFileAtomic(osAtomicOps, dir, tempPattern, name, fill)
}

func writeFileAtomic(ops atomicOps, dir, tempPattern, name string, fill func(io.Writer) error) error {
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := fill(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpPath, err)
	}
	if err := ops.syncFile(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpPath, err)
	}
	if err := ops.rename(tmpPath, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpPath, name, err)
	}
	renamed = true

	if err := ops.syncDir(dir); err != nil {
		return fmt.Errorf("syncing directory %s after renaming %s: %w", dir, name, err)
	}
	return nil
}

// writeBytesAtomic is WriteFileAtomic for content already in memory.
func writeBytesAtomic(dir, tempPattern, name string, data []byte) error {
	return WriteFileAtomic(dir, tempPattern, name, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// syncDirectory fsyncs a directory so a rename into it is durable. Some
// platforms and filesystems cannot fsync a directory (Windows, a few network
// and FUSE mounts answer EINVAL or ENOTSUP); that is "this volume gives no
// such guarantee", not a failed write, so it is swallowed. A real I/O error
// (EIO, ENOSPC) is returned. macOS and Linux ext4/xfs/btrfs support it.
func syncDirectory(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // dir is the operator-configured backup or uploads directory
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil && !dirSyncUnsupported(syncErr) {
		return syncErr
	}
	return closeErr
}

func dirSyncUnsupported(err error) bool {
	return errors.Is(err, errors.ErrUnsupported) ||
		errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.ENOSYS)
}
