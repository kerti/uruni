package backup

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// ADR-050: the canonical export carries waivers, note included, and a restore
// reads them back. No format_version bump: a file without the array is a file
// with no waivers.

func TestWaiversRoundTripThroughABackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	zipBytes, want := exportFixture(t, srcDB, t.TempDir())

	if len(want.DuesWaivers) != 1 || want.DuesWaivers[0].Note == nil || *want.DuesWaivers[0].Note != "Rumah kebakaran" {
		t.Fatalf("export waivers = %+v, want the fixture's one, note included", want.DuesWaivers)
	}

	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}
	destDB := newTestDB(t)
	// The destination already holds a different waiver: a restore replaces, so it must go.
	buildFixture(t, destDB, t.TempDir())
	extra := "sementara"
	if _, err := store.New(destDB).CreateDuesWaiver(ctx, store.CreateDuesWaiverParams{
		FundID: want.Funds[0].ID, MemberID: want.DuesWaivers[0].MemberID, DuesPeriod: "2026-10", Note: &extra, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateDuesWaiver(extra) = %v", err)
	}
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	got, _, err := BuildDocument(ctx, destDB)
	if err != nil {
		t.Fatalf("BuildDocument(dest) = %v", err)
	}
	if !reflect.DeepEqual(got.DuesWaivers, want.DuesWaivers) {
		t.Errorf("restored waivers = %+v, want %+v (the destination's own waiver replaced)", got.DuesWaivers, want.DuesWaivers)
	}
	// A waiver is not money: the totals the restore proves are the same with and without.
	assertBalancesMatch(t, destDB, want)
}

func TestAFileWithoutTheWaiversArrayRestoresWithNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	_, want := exportFixture(t, srcDB, t.TempDir())

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["dues_waivers"]; !ok {
		t.Fatal("the export has no dues_waivers key; the test needs one to remove")
	}
	delete(doc, "dues_waivers")
	older, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := parseUploadBytes(t, zipOf(t, older, nil))
	if err != nil {
		t.Fatalf("ParseUpload(a file written before waivers) = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	got, _, err := BuildDocument(ctx, destDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DuesWaivers) != 0 {
		t.Errorf("restored waivers = %+v, want none", got.DuesWaivers)
	}
	assertBalancesMatch(t, destDB, want)
}
