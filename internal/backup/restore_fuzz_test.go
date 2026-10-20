package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// restoreSentinels is every typed refusal ParseUpload documents; a fuzzed
// input that fails must fail as one of these, never as a bare error.
var restoreSentinels = []error{
	ErrFormatVersionNewer, ErrFormatVersionOlder, ErrMalformedBackup, ErrTooManyEntries,
	ErrUploadTooLarge, ErrUnzippedTooLarge, ErrBadReceiptImage, ErrTotalsMismatch,
}

func isRestoreSentinel(err error) bool {
	for _, s := range restoreSentinels {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// FuzzRestoreInspect (#525): ParseUpload is the inspect step for an untrusted
// backup upload. Whatever the bytes, it must not panic, must refuse with one
// of the package's sentinel errors, and on success must hand back the staging
// path it was given.
func FuzzRestoreInspect(f *testing.F) {
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		f.Fatalf("reading %s: %v", goldenPath, err)
	}
	jpg := realJPEGBytes(f)
	f.Add(zipOf(f, minimalDocJSON(f, FormatVersion), nil))
	f.Add(zipOf(f, minimalDocJSON(f, FormatVersion+1), nil))
	f.Add(zipOf(f, minimalDocJSON(f, FormatVersion-1), nil))
	f.Add(zipOf(f, golden, map[string][]byte{"0000000000000000000000000000ff.jpg": jpg}))
	f.Add(zipOf(f, golden, map[string][]byte{"0000000000000000000000000000ff.jpg": []byte("not an image")}))
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})

	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, "upload.zip")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("writing fuzz input: %v", err)
		}
		parsed, err := ParseUpload(path)
		if err != nil {
			if !isRestoreSentinel(err) {
				t.Fatalf("ParseUpload() error = %v, want one of the package's sentinel errors", err)
			}
			return
		}
		if parsed.StagingPath != path {
			t.Fatalf("ParseUpload() StagingPath = %q, want %q", parsed.StagingPath, path)
		}
		if parsed.Document.FormatVersion != FormatVersion {
			t.Fatalf("ParseUpload() FormatVersion = %d, want %d", parsed.Document.FormatVersion, FormatVersion)
		}
	})
}

// FuzzValidateReceiptImageBytes (#525): the decode check on a receipt image
// inside an uploaded backup refuses with ErrBadReceiptImage or accepts; it
// never panics and never answers with anything else.
func FuzzValidateReceiptImageBytes(f *testing.F) {
	f.Add(realJPEGBytes(f))
	f.Add([]byte("not an image"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if err := validateReceiptImageBytes(data); err != nil && !errors.Is(err, ErrBadReceiptImage) {
			t.Fatalf("validateReceiptImageBytes() error = %v, want ErrBadReceiptImage", err)
		}
	})
}
