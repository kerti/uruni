package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #632: a restore enforces the date window's floor (the schema's CHECKs) and
// nothing else (ADR-041: existing rows are never invalid). A file with a date
// below it is refused at the first row that has one and the whole restore rolls
// back.

const belowDay, belowMonth = "1999-12-31", "1999-12"

// floorMutations puts a below-floor date in one column of a parsed upload. Every
// column the schema floors is here (the audit in internal/db fails when a new one
// lacks a CHECK).
var floorMutations = []struct {
	column string
	mutate func(d *Document)
}{
	{"transaction.occurred_on", func(d *Document) { d.Transactions[0].OccurredOn = belowDay }},
	{"transaction.dues_period", func(d *Document) {
		for i := range d.Transactions {
			if d.Transactions[i].DuesPeriod != nil {
				p := belowMonth
				d.Transactions[i].DuesPeriod = &p
				return
			}
		}
		panic("the fixture holds no dues payment")
	}},
	{"reimbursement.incurred_on", func(d *Document) { d.Reimbursements[0].IncurredOn = belowDay }},
	{"reimbursement.waived_on", func(d *Document) { v := belowDay; d.Reimbursements[0].WaivedOn = &v }},
	{"incidental.opened_on", func(d *Document) { d.Incidentals[0].OpenedOn = belowDay }},
	{"incidental.closed_on", func(d *Document) { v := belowDay; d.Incidentals[0].ClosedOn = &v }},
	{"member.joined_on", func(d *Document) { d.Members[0].JoinedOn = belowDay }},
	{"member.inactive_on", func(d *Document) { v := belowDay; d.Members[0].InactiveOn = &v }},
	{"account.inactive_on", func(d *Document) { v := belowDay; d.Accounts[0].InactiveOn = &v }},
	{"dues_rate.effective_from", func(d *Document) { d.DuesRates[0].EffectiveFrom = belowMonth }},
	{"member_tier.effective_from", func(d *Document) { d.MemberTiers[0].EffectiveFrom = belowMonth }},
}

func TestRestoreRefusesADateBelowTheFloorAndRollsBack(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	zipBytes, _ := exportFixture(t, srcDB, srcUploads)

	for _, m := range floorMutations {
		t.Run(m.column, func(t *testing.T) {
			t.Parallel()
			parsed, err := parseUploadBytes(t, zipBytes)
			if err != nil {
				t.Fatalf("ParseUpload() = %v, want no error", err)
			}
			m.mutate(&parsed.Document)
			// A name the destination does not have, so a restore that half-applied
			// would show.
			parsed.Document.Funds[0].Name = "Dari cadangan"

			destDB := newTestDB(t)
			destUploads := t.TempDir()
			buildFixture(t, destDB, destUploads)
			before, _, err := BuildDocument(context.Background(), destDB)
			if err != nil {
				t.Fatalf("BuildDocument(dest) = %v, want no error", err)
			}

			err = Restore(context.Background(), destDB, destUploads, t.TempDir(), parsed, time.Unix(1800000000, 0))
			if err == nil {
				t.Fatalf("Restore(%s = a day below the floor) = nil error, want the floor CHECK to refuse it", m.column)
			}
			if msg := err.Error(); !strings.Contains(msg, "CHECK constraint failed") || !strings.Contains(msg, "2000-01") {
				t.Errorf("Restore(%s below the floor) error = %q, want the floor CHECK (it quotes 2000-01)", m.column, msg)
			}

			after, _, err := BuildDocument(context.Background(), destDB)
			if err != nil {
				t.Fatalf("BuildDocument(dest) after the refusal = %v, want no error", err)
			}
			assertDocumentsEqual(t, after, before)
		})
	}
}

// The floor itself restores: 1 January 2000 and January 2000 are inside the
// window, so the boundary is exact on the restore side too.
func TestRestoreAcceptsADateOnTheFloor(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	zipBytes, _ := exportFixture(t, srcDB, srcUploads)
	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}
	d := &parsed.Document
	d.Transactions[0].OccurredOn = "2000-01-01"
	d.Reimbursements[0].IncurredOn = "2000-01-01"
	d.Incidentals[0].OpenedOn = "2000-01-01"
	d.Members[0].JoinedOn = "2000-01-01"
	d.DuesRates[0].EffectiveFrom = "2000-01"
	d.MemberTiers[0].EffectiveFrom = "2000-01"

	restoreOrFatal(t, newTestDB(t), t.TempDir(), t.TempDir(), parsed)
}
