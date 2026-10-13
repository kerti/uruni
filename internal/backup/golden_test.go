package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenPath is the checked-in fixture ADR-012 requires: "a checked-in golden
// uruni.json must round-trip exactly, so any schema edit that changes the
// export breaks a test and forces a conscious bump."
const goldenPath = "testdata/golden.json"

// TestGoldenFixtureMatchesExport is that test. buildFixture's deterministic
// data (fixed timestamps and receipt filename, one row per exported table) is
// built fresh against a migrated database, run through BuildDocument,
// marshaled as Export marshals it, and compared byte-for-byte with
// testdata/golden.json.
//
// A failure means a deliberate change to what the export contains (regenerate
// testdata/golden.json, which this test prints on failure, and bump
// FormatVersion in backup.go in the same PR) or an accidental one, which this
// exists to catch before it ships as a silent format change.
func TestGoldenFixtureMatchesExport(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	ctx := context.Background()

	doc, _, err := BuildDocument(ctx, sqlDB)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}

	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(doc) = %v, want no error", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(filepath.Join(goldenPath))
	if err != nil {
		t.Fatalf("reading %s: %v (does the fixture need to be generated?)", goldenPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("export does not match %s - if this is a deliberate shape change, "+
			"regenerate the fixture with the bytes below and bump FormatVersion:\n%s", goldenPath, got)
	}
}
