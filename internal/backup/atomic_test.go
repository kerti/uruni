package backup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// recordingOps wraps the real operations and logs the order they run in, so a
// test can assert "file sync, rename, directory sync" without a crash.
func recordingOps(log *[]string) atomicOps {
	return atomicOps{
		syncFile: func(f *os.File) error {
			*log = append(*log, "syncFile")
			return f.Sync()
		},
		rename: func(o, n string) error {
			*log = append(*log, "rename")
			return os.Rename(o, n)
		},
		syncDir: func(dir string) error {
			*log = append(*log, "syncDir")
			return syncDirectory(dir)
		},
	}
}

func writeString(s string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	}
}

func TestWriteFileAtomicSyncsTheFileBeforeTheRenameAndTheDirectoryAfter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var log []string

	if err := writeFileAtomic(recordingOps(&log), dir, ".tmp-*", "final.bin", writeString("payload")); err != nil {
		t.Fatalf("writeFileAtomic() = %v, want no error", err)
	}
	if want := []string{"syncFile", "rename", "syncDir"}; !reflect.DeepEqual(log, want) {
		t.Errorf("step order = %v, want %v", log, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "final.bin")) //nolint:gosec // a test temp dir
	if err != nil || string(got) != "payload" {
		t.Errorf("final file = (%q, %v), want payload", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("dir holds %d entries, want only the final file: %v", len(entries), entries)
	}
}

func TestWriteFileAtomicLeavesNoTempFileOnFailure(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")

	cases := []struct {
		name string
		ops  func(base atomicOps) atomicOps
		fill func(io.Writer) error
	}{
		{"fill fails", func(b atomicOps) atomicOps { return b }, func(io.Writer) error { return boom }},
		{"file sync fails", func(b atomicOps) atomicOps {
			b.syncFile = func(*os.File) error { return boom }
			return b
		}, writeString("x")},
		{"rename fails", func(b atomicOps) atomicOps {
			b.rename = func(string, string) error { return boom }
			return b
		}, writeString("x")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			var log []string
			err := writeFileAtomic(tc.ops(recordingOps(&log)), dir, ".tmp-*", "final.bin", tc.fill)
			if !errors.Is(err, boom) {
				t.Fatalf("writeFileAtomic() = %v, want it to wrap the failure", err)
			}
			assertDirEmpty(t, dir)
			for _, step := range log {
				if step == "syncDir" {
					t.Error("the directory was synced after a failed write")
				}
			}
		})
	}
}

// A failed directory sync is reported, but the file is already in place under
// its final name and is not rolled back.
func TestWriteFileAtomicReportsADirectorySyncFailureWithoutRollingBack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	boom := errors.New("boom")
	var log []string
	ops := recordingOps(&log)
	ops.syncDir = func(string) error { return boom }

	if err := writeFileAtomic(ops, dir, ".tmp-*", "final.bin", writeString("x")); !errors.Is(err, boom) {
		t.Fatalf("writeFileAtomic() = %v, want the directory sync failure", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "final.bin")); err != nil {
		t.Errorf("final file missing after a directory sync failure: %v", err)
	}
}

func TestSyncDirectoryWorksOnARealDirectoryAndToleratesUnsupported(t *testing.T) {
	t.Parallel()
	if err := syncDirectory(t.TempDir()); err != nil {
		t.Errorf("syncDirectory(real dir) = %v, want nil", err)
	}
	if err := syncDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("syncDirectory(missing dir) = nil, want the open error")
	}
	for _, e := range []error{syscall.EINVAL, syscall.ENOTSUP, syscall.ENOSYS, errors.ErrUnsupported} {
		if !dirSyncUnsupported(e) {
			t.Errorf("dirSyncUnsupported(%v) = false, want true", e)
		}
	}
	if dirSyncUnsupported(syscall.EIO) {
		t.Error("dirSyncUnsupported(EIO) = true, want false (a real I/O error must surface)")
	}
}
