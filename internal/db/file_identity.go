package db

import (
	"errors"
	"fmt"
	"os"
)

// ErrDatabaseFileReplaced is what FileIdentity.Check returns when the path
// the server was started with no longer names the file it opened (#278).
var ErrDatabaseFileReplaced = errors.New("the database file was replaced or removed while the server holds it open")

// FileIdentity remembers which file the configured database path named when
// the server opened it, so a later check can tell whether the path still
// names that same file.
//
// SQLite holds the file by descriptor, not by name. Delete or replace the
// file underneath a running server and it keeps reading and writing the
// orphaned inode: balances add up, entries save, and every write since the
// swap is gone at the next restart, with nothing logged (#278). The in-app
// restore (ADR-012) replaces rows on the open connection and never does
// this; an operator copying a file or swapping a volume by hand does.
//
// os.SameFile compares device and inode on Unix, which is exactly the
// question; no syscall package needed.
type FileIdentity struct {
	path   string
	opened os.FileInfo
}

// IdentifyFile records the file path names right now. Call it after Open,
// which has already created the file if it was missing.
func IdentifyFile(path string) (*FileIdentity, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("identifying the database file at %s: %w", path, err)
	}
	return &FileIdentity{path: path, opened: fi}, nil
}

// Check re-stats the path and returns ErrDatabaseFileReplaced, wrapped with
// the path, when it is gone or names a different file than the one opened.
func (f *FileIdentity) Check() error {
	now, err := os.Stat(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s no longer exists", ErrDatabaseFileReplaced, f.path)
		}
		return fmt.Errorf("checking the database file at %s: %w", f.path, err)
	}
	if !os.SameFile(f.opened, now) {
		return fmt.Errorf("%w: %s is now a different file", ErrDatabaseFileReplaced, f.path)
	}
	return nil
}
