package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// realJPEGBytes is a genuine, decodable 1x1 JPEG. buildFixture's
// receiptFileBytes is not a real image (Export only copies it), which
// restore's ADR-011 decode check cares about, so tests that round-trip
// through ParseUpload replace the stub bytes with this, on disk only.
func realJPEGBytes(t testing.TB) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encoding fixture JPEG: %v", err)
	}
	return buf.Bytes()
}

// exportFixture is the golden test's recipe (golden_test.go): a deterministic
// fund built through store.Queries, exported through BuildDocument/BuildZip,
// ready to feed back into ParseUpload/Restore as if just uploaded.
func exportFixture(t *testing.T, sqlDB *sql.DB, uploadsDir string) (zipBytes []byte, doc Document) {
	t.Helper()
	ctx := context.Background()
	doc, receipts, err := BuildDocument(ctx, sqlDB)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}
	zipBytes, _, _, err = BuildZip(doc, receipts, uploadsDir)
	if err != nil {
		t.Fatalf("BuildZip() = %v, want no error", err)
	}
	return zipBytes, doc
}

// assertBalancesMatch compares every figure doc.Totals names against what
// destDB holds after a restore: the round trip's proof, independent of
// Restore's internal verifyTotals.
func assertBalancesMatch(t *testing.T, destDB *sql.DB, doc Document) {
	t.Helper()
	ctx := context.Background()
	l := ledger.New(destDB)

	for _, ft := range doc.Totals.Funds {
		got, err := l.FundBalance(ctx, ft.FundID)
		if err != nil {
			t.Fatalf("FundBalance(%d) = %v, want no error", ft.FundID, err)
		}
		if got.Int64() != ft.Balance {
			t.Errorf("fund %d balance = %d, want %d", ft.FundID, got.Int64(), ft.Balance)
		}
	}
	for _, at := range doc.Totals.Accounts {
		fundID := int64(0)
		for _, a := range doc.Accounts {
			if a.ID == at.AccountID {
				fundID = a.FundID
			}
		}
		got, err := l.AccountBalance(ctx, fundID, at.AccountID)
		if err != nil {
			t.Fatalf("AccountBalance(%d) = %v, want no error", at.AccountID, err)
		}
		if got.Int64() != at.Balance {
			t.Errorf("account %d balance = %d, want %d", at.AccountID, got.Int64(), at.Balance)
		}
	}
	for _, pt := range doc.Totals.Purposes {
		fundID := int64(0)
		for _, p := range doc.Purposes {
			if p.ID == pt.PurposeID {
				fundID = p.FundID
			}
		}
		got, err := l.PurposeBalance(ctx, fundID, pt.PurposeID)
		if err != nil {
			t.Fatalf("PurposeBalance(%d) = %v, want no error", pt.PurposeID, err)
		}
		if got.Int64() != pt.Balance {
			t.Errorf("purpose %d balance = %d, want %d", pt.PurposeID, got.Int64(), pt.Balance)
		}
	}
}

// assertDocumentsEqual compares two exports byte-for-byte via their canonical
// JSON, as TestGoldenFixtureMatchesExport does, except Users, cleared on both
// sides first: the login ruling (ADR-012, #325) makes the live user row and
// the file's Users independent on purpose.
func assertDocumentsEqual(t *testing.T, got, want Document) {
	t.Helper()
	got.Users = []User{}
	want.Users = []User{}
	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshaling got: %v", err)
	}
	wantJSON, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("marshaling want: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("restored document does not match the original:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func restoreOrFatal(t *testing.T, destDB *sql.DB, uploadsDir, backupDir string, parsed ParsedUpload) {
	t.Helper()
	if err := Restore(context.Background(), destDB, uploadsDir, backupDir, parsed, time.Unix(1800000000, 0)); err != nil {
		t.Fatalf("Restore() = %v, want no error", err)
	}
}

// TestRestoreRoundTripGoldenFixture is ADR-012's requirement: export -> fresh
// database -> import -> identical balances, on the fixture that carries a
// reversal, a transfer, a named contribution, a settled reimbursement with its
// receipt, and a reconciliation.
func TestRestoreRoundTripGoldenFixture(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	realReceipt := realJPEGBytes(t)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realReceipt, 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	zipBytes, wantDoc := exportFixture(t, srcDB, srcUploads)

	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}

	destDB := newTestDB(t)
	destUploads := t.TempDir()
	backupDir := t.TempDir()
	restoreOrFatal(t, destDB, destUploads, backupDir, parsed)

	assertBalancesMatch(t, destDB, wantDoc)

	ctx := context.Background()
	gotDoc, _, err := BuildDocument(ctx, destDB)
	if err != nil {
		t.Fatalf("BuildDocument(dest) = %v, want no error", err)
	}
	assertDocumentsEqual(t, gotDoc, wantDoc)

	//nolint:gosec // destUploads and receiptFilename are this test's fixture values, never request input
	gotReceipt, err := os.ReadFile(filepath.Join(destUploads, receiptFilename))
	if err != nil {
		t.Fatalf("reading restored receipt file: %v", err)
	}
	if string(gotReceipt) != string(realReceipt) {
		t.Errorf("restored receipt bytes do not match the original")
	}
}

// TestRestoreRoundTripWithClosedEnvelope adds the shape buildFixture's
// envelope leaves out: a closed incidental (ADR-034's closed_on).
func TestRestoreRoundTripWithClosedEnvelope(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}

	q := store.New(srcDB)
	// buildFixture's envelope purpose is the sole incidental it wrote: list the
	// fund's incidentals and close it.
	funds, err := q.ListFunds(context.Background())
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = (%v, %v), want exactly one fixture fund", funds, err)
	}
	incidentals, err := q.ListIncidentalsByFund(context.Background(), funds[0].ID)
	if err != nil || len(incidentals) != 1 {
		t.Fatalf("ListIncidentalsByFund() = (%v, %v), want exactly one fixture incidental", incidentals, err)
	}
	if _, err := q.CloseIncidental(context.Background(), store.CloseIncidentalParams{
		ClosedOn: strPtr("2026-03-01"), PurposeID: incidentals[0].PurposeID,
	}); err != nil {
		t.Fatalf("CloseIncidental() = %v, want no error", err)
	}

	zipBytes, wantDoc := exportFixture(t, srcDB, srcUploads)
	if wantDoc.Incidentals[0].ClosedOn == nil {
		t.Fatalf("fixture incidental was not actually closed before export")
	}

	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}

	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	assertBalancesMatch(t, destDB, wantDoc)
	gotDoc, _, err := BuildDocument(context.Background(), destDB)
	if err != nil {
		t.Fatalf("BuildDocument(dest) = %v, want no error", err)
	}
	assertDocumentsEqual(t, gotDoc, wantDoc)
}

func strPtr(s string) *string { return &s }

// minimalDocJSON is the smallest valid uruni.json ParseUpload accepts (every
// "always an array" field present, formatVersion correct); the refusal tests
// start here and break one thing.
func minimalDocJSON(t testing.TB, formatVersion int64) []byte {
	t.Helper()
	doc := Document{
		FormatVersion:        formatVersion,
		Users:                []User{},
		Funds:                []Fund{},
		Accounts:             []Account{},
		Purposes:             []Purpose{},
		DuesTiers:            []DuesTier{},
		DuesRates:            []DuesRate{},
		Members:              []Member{},
		Transfers:            []Transfer{},
		Reimbursements:       []Reimbursement{},
		Transactions:         []Transaction{},
		Receipts:             []Receipt{},
		Reconciliations:      []Reconciliation{},
		ReconciliationLines:  []ReconciliationLine{},
		Incidentals:          []Incidental{},
		IncidentalRecipients: []IncidentalRecipient{},
		Totals:               Totals{Funds: []FundTotal{}, Accounts: []AccountTotal{}, Purposes: []PurposeTotal{}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshaling minimal doc: %v", err)
	}
	return b
}

// zipOf builds a zip in memory holding jsonBytes under uruni.json plus one
// entry per key in receipts, the shape BuildZip produces, built by hand so a
// refusal test can hand it what BuildZip never writes (an unknown field, a
// bad image, a mismatched format_version, junk entries).
// parseUploadBytes stands in for the staging file stageAndInspect
// (internal/http/restore.go) writes: tests build zips in memory, but
// ParseUpload reads only from disk (#344), so each call writes the zip to a
// temp file first.
func parseUploadBytes(t *testing.T, data []byte) (ParsedUpload, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upload.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing zip fixture to disk: %v", err)
	}
	return ParseUpload(path)
}

func zipOf(t testing.TB, jsonBytes []byte, receipts map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(jsonFilename)
	if err != nil {
		t.Fatalf("creating %s: %v", jsonFilename, err)
	}
	if _, err := w.Write(jsonBytes); err != nil {
		t.Fatalf("writing %s: %v", jsonFilename, err)
	}
	for name, data := range receipts {
		rw, err := zw.Create(receiptsDir + name)
		if err != nil {
			t.Fatalf("creating receipts/%s: %v", name, err)
		}
		if _, err := rw.Write(data); err != nil {
			t.Fatalf("writing receipts/%s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func TestParseUploadRefusesUnknownFields(t *testing.T) {
	t.Parallel()
	var raw map[string]any
	if err := json.Unmarshal(minimalDocJSON(t, FormatVersion), &raw); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	raw["something_this_version_has_never_heard_of"] = true
	tampered, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal tampered doc: %v", err)
	}

	_, err = parseUploadBytes(t, zipOf(t, tampered, nil))
	if !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("ParseUpload() error = %v, want ErrMalformedBackup", err)
	}
}

func TestParseUploadRefusesNewerFormatVersionDistinctly(t *testing.T) {
	t.Parallel()
	_, err := parseUploadBytes(t, zipOf(t, minimalDocJSON(t, FormatVersion+1), nil))
	if !errors.Is(err, ErrFormatVersionNewer) {
		t.Errorf("ParseUpload() error = %v, want ErrFormatVersionNewer", err)
	}
	if errors.Is(err, ErrFormatVersionOlder) {
		t.Errorf("ParseUpload() error also matches ErrFormatVersionOlder, want the two kept distinct")
	}
}

// "Older" is older than the oldest format an upgrade step covers; the previous
// release's format is upgraded, not refused (upgrade_test.go).
func TestParseUploadRefusesOlderFormatVersionDistinctly(t *testing.T) {
	t.Parallel()
	_, err := parseUploadBytes(t, zipOf(t, minimalDocJSON(t, minUpgradableFormat-1), nil))
	if !errors.Is(err, ErrFormatVersionOlder) {
		t.Errorf("ParseUpload() error = %v, want ErrFormatVersionOlder", err)
	}
	if errors.Is(err, ErrFormatVersionNewer) {
		t.Errorf("ParseUpload() error also matches ErrFormatVersionNewer, want the two kept distinct")
	}
}

// Serial on purpose, no t.Parallel: it shrinks a package-level upload cap
// that every parallel ParseUpload reads.
func TestParseUploadRefusesTooManyEntries(t *testing.T) {
	orig := MaxZipEntries
	MaxZipEntries = 2
	t.Cleanup(func() { MaxZipEntries = orig })

	z := zipOf(t, minimalDocJSON(t, FormatVersion), map[string][]byte{"a.jpg": {1}, "b.jpg": {2}})
	_, err := parseUploadBytes(t, z)
	if !errors.Is(err, ErrTooManyEntries) {
		t.Errorf("ParseUpload() error = %v, want ErrTooManyEntries", err)
	}
}

// Serial on purpose, no t.Parallel: it shrinks a package-level upload cap
// that every parallel ParseUpload reads.
func TestParseUploadRefusesUnzippedTooLarge(t *testing.T) {
	orig := MaxUnzippedBytes
	MaxUnzippedBytes = 4 // smaller than uruni.json's own minimal bytes
	t.Cleanup(func() { MaxUnzippedBytes = orig })

	_, err := parseUploadBytes(t, zipOf(t, minimalDocJSON(t, FormatVersion), nil))
	if !errors.Is(err, ErrUnzippedTooLarge) {
		t.Errorf("ParseUpload() error = %v, want ErrUnzippedTooLarge", err)
	}
}

func TestParseUploadRefusesBadReceiptImage(t *testing.T) {
	t.Parallel()
	doc := Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Funds: []Fund{}, Accounts: []Account{}, Purposes: []Purpose{},
		DuesTiers: []DuesTier{}, DuesRates: []DuesRate{}, Members: []Member{},
		Transfers: []Transfer{}, Reimbursements: []Reimbursement{}, Transactions: []Transaction{},
		Receipts:             []Receipt{{ID: 1, FundID: 1, Path: "bad.jpg", UploadedAt: 1}},
		Reconciliations:      []Reconciliation{},
		ReconciliationLines:  []ReconciliationLine{},
		Incidentals:          []Incidental{},
		IncidentalRecipients: []IncidentalRecipient{},
		Totals:               Totals{Funds: []FundTotal{}, Accounts: []AccountTotal{}, Purposes: []PurposeTotal{}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	z := zipOf(t, b, map[string][]byte{"bad.jpg": []byte("not actually an image")})
	_, err = parseUploadBytes(t, z)
	if !errors.Is(err, ErrBadReceiptImage) {
		t.Errorf("ParseUpload() error = %v, want ErrBadReceiptImage", err)
	}
}

func TestParseUploadRefusesReceiptPathThatIsNotABareFilename(t *testing.T) {
	t.Parallel()
	doc := Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Funds: []Fund{}, Accounts: []Account{}, Purposes: []Purpose{},
		DuesTiers: []DuesTier{}, DuesRates: []DuesRate{}, Members: []Member{},
		Transfers: []Transfer{}, Reimbursements: []Reimbursement{}, Transactions: []Transaction{},
		Receipts:             []Receipt{{ID: 1, FundID: 1, Path: "../../etc/passwd", UploadedAt: 1}},
		Reconciliations:      []Reconciliation{},
		ReconciliationLines:  []ReconciliationLine{},
		Incidentals:          []Incidental{},
		IncidentalRecipients: []IncidentalRecipient{},
		Totals:               Totals{Funds: []FundTotal{}, Accounts: []AccountTotal{}, Purposes: []PurposeTotal{}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, err = parseUploadBytes(t, zipOf(t, b, nil))
	if !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("ParseUpload() error = %v, want ErrMalformedBackup", err)
	}
}

func TestParseUploadToleratesAMissingReceiptImage(t *testing.T) {
	t.Parallel()
	doc := Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Funds: []Fund{}, Accounts: []Account{}, Purposes: []Purpose{},
		DuesTiers: []DuesTier{}, DuesRates: []DuesRate{}, Members: []Member{},
		Transfers: []Transfer{}, Reimbursements: []Reimbursement{}, Transactions: []Transaction{},
		Receipts:             []Receipt{{ID: 1, FundID: 1, Path: "missing.jpg", UploadedAt: 1}},
		Reconciliations:      []Reconciliation{},
		ReconciliationLines:  []ReconciliationLine{},
		Incidentals:          []Incidental{},
		IncidentalRecipients: []IncidentalRecipient{},
		Totals:               Totals{Funds: []FundTotal{}, Accounts: []AccountTotal{}, Purposes: []PurposeTotal{}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// No receipts/ entry at all for missing.jpg.
	parsed, err := parseUploadBytes(t, zipOf(t, b, nil))
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error (a missing image never blocks a restore)", err)
	}
	if len(parsed.Document.Receipts) != 1 {
		t.Fatalf("parsed.Document.Receipts = %v, want the one row preserved", parsed.Document.Receipts)
	}
}

// TestRestoreTotalsMismatchRollsBack hand-builds a ParsedUpload whose totals
// disagree with its rows (bypassing ParseUpload) and checks Restore refuses
// to commit and leaves the destination exactly as it was.
func TestRestoreTotalsMismatchRollsBack(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	seedOneUser(t, destDB)

	doc := Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Accounts: []Account{}, Purposes: []Purpose{}, DuesTiers: []DuesTier{},
		DuesRates: []DuesRate{}, Members: []Member{}, Transfers: []Transfer{}, Reimbursements: []Reimbursement{},
		Transactions: []Transaction{}, Receipts: []Receipt{}, Reconciliations: []Reconciliation{},
		ReconciliationLines: []ReconciliationLine{}, Incidentals: []Incidental{}, IncidentalRecipients: []IncidentalRecipient{},
		Funds: []Fund{{ID: 1, Name: "Dana", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "0000000000000000000slug01", CreatedAt: 1}},
		Totals: Totals{
			Funds:    []FundTotal{{FundID: 1, Balance: 999}}, // the fund carries zero transactions, so its real balance is 0
			Accounts: []AccountTotal{}, Purposes: []PurposeTotal{},
		},
	}

	err := Restore(context.Background(), destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: doc}, time.Unix(1800000000, 0))
	if !errors.Is(err, ErrTotalsMismatch) {
		t.Fatalf("Restore() error = %v, want ErrTotalsMismatch", err)
	}

	funds, err := store.New(destDB).ListFunds(context.Background())
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 0 {
		t.Errorf("ListFunds() = %v, want none - a mismatch must roll back with nothing changed", funds)
	}
}

// TestRestoreFailureRemovesOnlyTheReceiptsItCreated forces a totals mismatch
// after receipt extraction: the image this restore wrote must be gone again,
// so "nothing was changed" holds, but an image already on disk under the same
// name (ADR-011's random names) must survive.
func TestRestoreFailureRemovesOnlyTheReceiptsItCreated(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	realReceipt := realJPEGBytes(t)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realReceipt, 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	zipBytes, _ := exportFixture(t, srcDB, srcUploads)

	for _, alreadyThere := range []bool{false, true} {
		name := "receipt not on disk before"
		if alreadyThere {
			name = "receipt already on disk"
		}
		t.Run(name, func(t *testing.T) {
			parsed, err := parseUploadBytes(t, zipBytes)
			if err != nil {
				t.Fatalf("ParseUpload() = %v, want no error", err)
			}
			parsed.Document.Totals.Funds[0].Balance++

			destDB := newTestDB(t)
			destUploads := t.TempDir()
			dest := filepath.Join(destUploads, receiptFilename)
			if alreadyThere {
				if err := os.WriteFile(dest, realReceipt, 0o600); err != nil {
					t.Fatalf("pre-seeding the receipt: %v", err)
				}
			}

			err = Restore(context.Background(), destDB, destUploads, t.TempDir(), parsed, time.Unix(1800000000, 0))
			if !errors.Is(err, ErrTotalsMismatch) {
				t.Fatalf("Restore() error = %v, want ErrTotalsMismatch", err)
			}

			_, statErr := os.Stat(dest)
			if alreadyThere && statErr != nil {
				t.Errorf("a receipt that was on disk before the restore is gone: %v", statErr)
			}
			if !alreadyThere && !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("a receipt this failed restore wrote is still on disk (stat err = %v)", statErr)
			}
		})
	}
}

// TestRestoreSafetyNetDumpFailureAborts makes WritePreRestoreDump fail (an
// unwritable backup directory) and checks Restore aborts before touching the
// database.
func TestRestoreSafetyNetDumpFailureAborts(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	seedOneUser(t, destDB)

	// A file, not a directory: os.CreateTemp inside it fails immediately.
	notADir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing stand-in file: %v", err)
	}

	doc := minimalDocWithOneFund(t)
	err := Restore(context.Background(), destDB, t.TempDir(), notADir, ParsedUpload{Document: doc}, time.Unix(1800000000, 0))
	if err == nil {
		t.Fatal("Restore() = nil, want an error when the safety-net dump cannot be written")
	}

	funds, err := store.New(destDB).ListFunds(context.Background())
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 0 {
		t.Errorf("ListFunds() = %v, want none - a failed safety-net dump must abort with nothing changed", funds)
	}
}

// TestRestoreUserRowSurvives is the login ruling (ADR-012, #325): the live
// user row is untouched by a restore, though the file's Users are decoded
// and differ.
func TestRestoreUserRowSurvives(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	liveUser := seedOneUser(t, destDB)

	doc := minimalDocWithOneFund(t)
	doc.Users = []User{{ID: 999, Email: "someone-else@example.org", PasswordHash: "not-the-live-hash", CreatedAt: 1}}

	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: doc})

	users, err := store.New(destDB).ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers() = %v, want no error", err)
	}
	if len(users) != 1 || users[0].ID != liveUser.ID || users[0].Email != liveUser.Email || users[0].PasswordHash != liveUser.PasswordHash {
		t.Errorf("ListUsers() after restore = %+v, want the untouched live user %+v", users, liveUser)
	}
}

// TestRestoreClearsSessions is ADR-012's "a restore logs everyone out."
func TestRestoreClearsSessions(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	seedOneUser(t, destDB)
	q := store.New(destDB)
	if _, err := q.CreateSession(context.Background(), store.CreateSessionParams{
		Token: "some-session-token", Data: []byte("data"), ExpiresAt: 9999999999,
	}); err != nil {
		t.Fatalf("CreateSession() = %v, want no error", err)
	}

	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: minimalDocWithOneFund(t)})

	_, err := q.GetSession(context.Background(), store.GetSessionParams{Token: "some-session-token", ExpiresAt: 0})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession() after restore = %v, want sql.ErrNoRows (every session cleared)", err)
	}
}

// TestRestoreClearsIdempotencyKeys is ADR-043's "restore clears it": a key from
// before the restore would replay a success whose rows are gone.
func TestRestoreClearsIdempotencyKeys(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	seedOneUser(t, destDB)
	q := store.New(destDB)
	if err := q.CreateIdempotencyKey(context.Background(), store.CreateIdempotencyKeyParams{
		Key: "a-key-from-before-the-restore", Fingerprint: strings.Repeat("f", 64), Result: []byte("{}"), CreatedAt: 100, ExpiresAt: 9999999999,
	}); err != nil {
		t.Fatalf("CreateIdempotencyKey() = %v, want no error", err)
	}

	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: minimalDocWithOneFund(t)})

	if _, err := q.GetIdempotencyKey(context.Background(), store.GetIdempotencyKeyParams{Key: "a-key-from-before-the-restore", Now: 0}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetIdempotencyKey() after restore = %v, want sql.ErrNoRows (every key cleared)", err)
	}
}

// TestRestoreRecreatesImmutableTriggers is the drift guard immutableTriggers
// promises: after a restore, each of the eight recreated triggers still
// refuses the same update/delete with the same message.
func TestRestoreRecreatesImmutableTriggers(t *testing.T) {
	t.Parallel()
	// The fixture, not minimalDocWithOneFund: a BEFORE trigger never fires on a
	// statement matching zero rows, so this needs a restored database with a row
	// with id 1 in all four protected tables, which buildFixture provides.
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

	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	cases := []struct {
		name string
		stmt string
		want string
	}{
		{"transaction update", `UPDATE "transaction" SET note = 'x' WHERE id = 1`, "transaction rows are immutable - post an adjusting entry"},
		{"transaction delete", `DELETE FROM "transaction" WHERE id = 1`, "transaction rows are immutable - post an adjusting entry"},
		{"transfer update", `UPDATE transfer SET kind = 'reclass_purpose' WHERE id = 1`, "transfer rows are immutable - post an adjusting entry"},
		{"transfer delete", `DELETE FROM transfer WHERE id = 1`, "transfer rows are immutable - post an adjusting entry"},
		{"reconciliation update", `UPDATE reconciliation SET note = 'x' WHERE id = 1`, "reconciliation rows are immutable - take a new snapshot"},
		{"reconciliation delete", `DELETE FROM reconciliation WHERE id = 1`, "reconciliation rows are immutable - take a new snapshot"},
		{"reconciliation_line update", `UPDATE reconciliation_line SET resolution = 'matched' WHERE id = 1`, "reconciliation_line rows are immutable - take a new snapshot"},
		{"reconciliation_line delete", `DELETE FROM reconciliation_line WHERE id = 1`, "reconciliation_line rows are immutable - take a new snapshot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := destDB.ExecContext(context.Background(), tc.stmt)
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Errorf("%s = %v, want an error containing %q", tc.stmt, err, tc.want)
			}
		})
	}
}

// seedOneUser writes the row every Restore test on a non-fixture database
// needs first: Restore clears every fund-scoped table but never "user", and
// a live instance always has one.
func seedOneUser(t *testing.T, sqlDB *sql.DB) store.User {
	t.Helper()
	//nolint:gosec // not a credential - a fixed test fixture value
	u, err := store.New(sqlDB).CreateUser(context.Background(), store.CreateUserParams{
		Email: "treasurer@example.org", PasswordHash: "argon2id$fake$hash", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateUser() = %v, want no error", err)
	}
	return u
}

// minimalDocWithOneFund is the smallest Document Restore can commit: one
// fund, no accounts/purposes/transactions, and a zeroed totals block.
func minimalDocWithOneFund(t *testing.T) Document {
	t.Helper()
	return Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Accounts: []Account{}, Purposes: []Purpose{}, DuesTiers: []DuesTier{},
		DuesRates: []DuesRate{}, Members: []Member{}, Transfers: []Transfer{}, Reimbursements: []Reimbursement{},
		Transactions: []Transaction{}, Receipts: []Receipt{}, Reconciliations: []Reconciliation{},
		ReconciliationLines: []ReconciliationLine{}, Incidentals: []Incidental{}, IncidentalRecipients: []IncidentalRecipient{},
		Funds: []Fund{{ID: 1, Name: "Dana", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "0000000000000000000slug01", CreatedAt: 1}},
		Totals: Totals{
			Funds: []FundTotal{{FundID: 1, Balance: 0}}, Accounts: []AccountTotal{}, Purposes: []PurposeTotal{},
		},
	}
}

// newestMomentEpoch/newestMomentDate are one fixed instant and its
// Asia/Jakarta calendar date, to check Preview.Date reads created_at rather
// than occurred_on.
const (
	newestMomentEpoch = 1767225600 // 2026-01-01T00:00:00Z = 2026-01-01 07:00 WIB
	newestMomentDate  = "2026-01-01"
)

// TestBuildPreviewReportsKeptRemovedAndAddedFunds covers the per-fund confirm
// line: a fund on both sides is "kept" and names how many live transactions
// sit past the file's cutoff, a live-only fund is "removed", a file-only fund
// is "added".
func TestBuildPreviewReportsKeptRemovedAndAddedFunds(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	ctx := context.Background()
	q := store.New(destDB)

	// Fund 1 lives on both sides: the file's newest transaction for it is id 1
	// (2026-01-01); two more were posted live afterward and would be lost.
	kept, err := q.CreateFund(ctx, store.CreateFundParams{Name: "Kept Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "0000000000000000000keptfund", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateFund(kept): %v", err)
	}
	cash, err := q.CreateAccount(ctx, store.CreateAccountParams{FundID: kept.ID, Kind: "cash", Name: "Tunai", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	main, err := q.CreatePurpose(ctx, store.CreatePurposeParams{FundID: kept.ID, Kind: "main", Name: "Kas Utama", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreatePurpose: %v", err)
	}
	fileTxn, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: kept.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "in", Amount: 1000,
		OccurredOn: "2026-01-01", Kind: "opening", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(file's own): %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: kept.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "in", Amount: 500,
			OccurredOn: "2026-02-01", Kind: "normal", CreatedAt: 2,
		}); err != nil {
			t.Fatalf("CreateTransaction(live-only): %v", err)
		}
	}

	// Fund 2 lives only live: removed by the restore.
	removed, err := q.CreateFund(ctx, store.CreateFundParams{Name: "Removed Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "0000000000000000000removedf", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateFund(removed): %v", err)
	}

	doc := Document{
		FormatVersion: FormatVersion,
		Funds: []Fund{
			{ID: kept.ID, Name: "Kept Fund (restored name)", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: kept.ReportSlug, CreatedAt: 1},
			{ID: 999, Name: "Added Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "0000000000000000000addedfund", CreatedAt: 1},
		},
		Transactions: []Transaction{
			// CreatedAt, not OccurredOn, is what Preview.Date reads (the newest
			// created_at stands in for "when was this backup taken").
			{ID: fileTxn.ID, FundID: kept.ID, AccountID: cash.ID, PurposeID: main.ID, Direction: "in", Amount: 1000, OccurredOn: "2026-01-01", Kind: "opening", CreatedAt: newestMomentEpoch},
		},
		Totals: Totals{
			Funds: []FundTotal{{FundID: kept.ID, Balance: 1000}, {FundID: 999, Balance: 0}},
		},
	}

	preview, err := BuildPreview(ctx, q, doc)
	if err != nil {
		t.Fatalf("BuildPreview() = %v, want no error", err)
	}

	if preview.Total != 1000 {
		t.Errorf("preview.Total = %d, want 1000", preview.Total)
	}
	if preview.Date != newestMomentDate {
		t.Errorf("preview.Date = %q, want %q (the file's own newest timestamp)", preview.Date, newestMomentDate)
	}

	byID := make(map[int64]FundPreview, len(preview.Funds))
	for _, f := range preview.Funds {
		byID[f.FundID] = f
	}

	if got := byID[kept.ID]; got.Status != FundKept || got.TransactionsLost != 2 || got.CutoffDate != "2026-01-01" || got.Name != "Kept Fund (restored name)" {
		t.Errorf("kept fund preview = %+v, want Status=kept TransactionsLost=2 CutoffDate=2026-01-01 Name from the file", got)
	}
	if got := byID[removed.ID]; got.Status != FundRemoved || got.Name != "Removed Fund" {
		t.Errorf("removed fund preview = %+v, want Status=removed Name=Removed Fund", got)
	}
	if got := byID[999]; got.Status != FundAdded || got.Name != "Added Fund" {
		t.Errorf("added fund preview = %+v, want Status=added Name=Added Fund", got)
	}
}

// TestNewestMomentPicksTheLatestRowOfAnyTable: the confirm dialog's "Cadangan
// dari <date>" is the newest created_at (or a receipt's uploaded_at) across
// every table, as a calendar date in the file's own zone (ADR-041), and no date
// for a document with no rows.
func TestNewestMomentPicksTheLatestRowOfAnyTable(t *testing.T) {
	t.Parallel()
	if got, err := newestMoment(Document{}); err != nil || got != "" {
		t.Errorf("newestMoment(empty) = (%q, %v), want empty", got, err)
	}

	// 2026-09-30 23:30 UTC is already 1 October in Jakarta (UTC+7).
	latest := time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC).Unix()
	earlier := latest - 86400*10
	tables := map[string]func(*Document, int64){
		"funds":          func(d *Document, ts int64) { d.Funds = append(d.Funds, Fund{CreatedAt: ts, TimeZone: tz.Jakarta}) },
		"accounts":       func(d *Document, ts int64) { d.Accounts = append(d.Accounts, Account{CreatedAt: ts}) },
		"purposes":       func(d *Document, ts int64) { d.Purposes = append(d.Purposes, Purpose{CreatedAt: ts}) },
		"dues tiers":     func(d *Document, ts int64) { d.DuesTiers = append(d.DuesTiers, DuesTier{CreatedAt: ts}) },
		"dues rates":     func(d *Document, ts int64) { d.DuesRates = append(d.DuesRates, DuesRate{CreatedAt: ts}) },
		"members":        func(d *Document, ts int64) { d.Members = append(d.Members, Member{CreatedAt: ts}) },
		"transfers":      func(d *Document, ts int64) { d.Transfers = append(d.Transfers, Transfer{CreatedAt: ts}) },
		"reimbursements": func(d *Document, ts int64) { d.Reimbursements = append(d.Reimbursements, Reimbursement{CreatedAt: ts}) },
		"transactions":   func(d *Document, ts int64) { d.Transactions = append(d.Transactions, Transaction{CreatedAt: ts}) },
		"receipts":       func(d *Document, ts int64) { d.Receipts = append(d.Receipts, Receipt{UploadedAt: ts}) },
		"reconciliations": func(d *Document, ts int64) {
			d.Reconciliations = append(d.Reconciliations, Reconciliation{CreatedAt: ts})
		},
		"incidentals": func(d *Document, ts int64) { d.Incidentals = append(d.Incidentals, Incidental{CreatedAt: ts}) },
	}
	for newest, addNewest := range tables {
		t.Run(newest, func(t *testing.T) {
			var doc Document
			for name, add := range tables {
				if name != newest {
					add(&doc, earlier)
				}
			}
			addNewest(&doc, latest)
			// Every table's newest row is read in the WIB the document's fund is in
			// (a document with no fund is WIB by default): 23:30 UTC is already 1 October.
			if doc.Funds == nil {
				doc.Funds = []Fund{{ID: 1, TimeZone: tz.Jakarta}}
			}
			if got, err := newestMoment(doc); err != nil || got != "2026-10-01" {
				t.Errorf("newestMoment() = (%q, %v), want %q", got, err, "2026-10-01")
			}
		})
	}
}

// The preview's date is the file's own zone's calendar day (ADR-041): the same
// instant is 30 September for a WIB file, and 1 October for a WIT one, which
// has already turned the day over by 00:30. A zone tz.Load refuses cannot date
// the file, so the preview errors rather than falling back.
func TestNewestMomentReadsTheFilesOwnZone(t *testing.T) {
	t.Parallel()
	instant := time.Date(2026, 9, 30, 22, 30, 0, 0, jakarta).Unix() // 22:30 WIB = 23:30 WITA = 00:30 WIT
	for zone, want := range map[string]string{tz.Jakarta: "2026-09-30", tz.Makassar: "2026-09-30", tz.Jayapura: "2026-10-01"} {
		doc := Document{Funds: []Fund{{ID: 1, TimeZone: zone, CreatedAt: instant}}}
		if got, err := newestMoment(doc); err != nil || got != want {
			t.Errorf("%s: newestMoment() = (%q, %v), want %q", zone, got, err, want)
		}
		if got, err := NewestDate(doc); err != nil || got != want {
			t.Errorf("%s: NewestDate() = (%q, %v), want %q", zone, got, err, want)
		}
	}
	for _, bad := range []string{"", "UTC", "Asia/Tokyo"} {
		doc := Document{Funds: []Fund{{ID: 1, TimeZone: bad, CreatedAt: instant}}}
		if got, err := newestMoment(doc); err == nil {
			t.Errorf("zone %q: newestMoment() = %q, want an error", bad, got)
		}
	}
}

// TestRestoreRoundTripCarriesTransferReason pins ADR-036's two directions on
// the golden fixture (one allocation, one roll, one pair with no reason): an
// export names each pair's reason, and a restore writes exactly those back.
func TestRestoreRoundTripCarriesTransferReason(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	zipBytes, wantDoc := exportFixture(t, srcDB, srcUploads)

	exported := map[int64]string{} // transfer id -> reason, "" for null
	for _, tr := range wantDoc.Transfers {
		if tr.Reason != nil {
			exported[tr.ID] = *tr.Reason
		} else {
			exported[tr.ID] = ""
		}
	}
	if want := (map[int64]string{1: "", 2: "allocation", 3: "roll"}); !reflect.DeepEqual(exported, want) {
		t.Fatalf("exported transfer reasons = %v, want %v", exported, want)
	}

	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	restored, err := store.New(destDB).ListTransfersByFund(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	got := map[int64]string{}
	for _, tr := range restored {
		if tr.Reason != nil {
			got[tr.ID] = *tr.Reason
		} else {
			got[tr.ID] = ""
		}
	}
	if !reflect.DeepEqual(got, exported) {
		t.Errorf("restored transfer reasons = %v, want %v", got, exported)
	}
}

// TestRestoreOfAFileWithoutReasonReadsNull is ADR-036's no-bump promise: a
// backup made before the column has no "reason" key on any transfer, the
// importer reads that as empty rather than refusing, and every restored
// transfer is NULL, which for a reclass pair that corrects nothing is a roll.
// Balances still match: reason moves no money.
func TestRestoreOfAFileWithoutReasonReadsNull(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	_, wantDoc := exportFixture(t, srcDB, srcUploads)

	// Re-encode the document as an older server wrote it: transfers without the
	// key.
	raw, err := json.Marshal(wantDoc)
	if err != nil {
		t.Fatalf("marshaling document: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshaling document: %v", err)
	}
	var transfers []map[string]json.RawMessage
	if err := json.Unmarshal(generic["transfers"], &transfers); err != nil {
		t.Fatalf("unmarshaling transfers: %v", err)
	}
	if len(transfers) != 3 {
		t.Fatalf("fixture has %d transfers, want 3", len(transfers))
	}
	for _, tr := range transfers {
		if _, ok := tr["reason"]; !ok {
			t.Fatalf("export has no reason key to strip: %v", tr)
		}
		delete(tr, "reason")
	}
	if generic["transfers"], err = json.Marshal(transfers); err != nil {
		t.Fatalf("marshaling transfers: %v", err)
	}
	old, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("marshaling old-shaped document: %v", err)
	}

	parsed, err := parseUploadBytes(t, zipOf(t, old, map[string][]byte{receiptFilename: realJPEGBytes(t)}))
	if err != nil {
		t.Fatalf("ParseUpload(file without reason) = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	assertBalancesMatch(t, destDB, wantDoc)
	restored, err := store.New(destDB).ListTransfersByFund(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	if len(restored) != 3 {
		t.Fatalf("restored %d transfers, want 3", len(restored))
	}
	for _, tr := range restored {
		if tr.Reason != nil {
			t.Errorf("transfer %d restored with reason %q, want NULL", tr.ID, *tr.Reason)
		}
	}
}

// TestRestoreOfANullJoinedOnReadsTheAdoptionDate (#471): member.joined_on
// became NOT NULL, but a format_version 1 file written earlier can carry
// null. Restore reads it as the day the fund's history starts (its earliest
// opening-balance row), so no member's dues change.
func TestRestoreOfANullJoinedOnReadsTheAdoptionDate(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	_, doc := exportFixture(t, srcDB, srcUploads)

	// The earliest of the two openings is the adoption date, not the first in
	// file order.
	var openings int
	for i := range doc.Transactions {
		if doc.Transactions[i].Kind == "opening" {
			openings++
			if openings == 2 {
				doc.Transactions[i].OccurredOn = "2025-12-20"
			}
		}
	}
	if openings != 2 {
		t.Fatalf("fixture has %d openings, want 2", openings)
	}
	if len(doc.Members) == 0 {
		t.Fatal("fixture has no members")
	}
	oldDoc := v1Of(doc)
	for i := range oldDoc.Members {
		oldDoc.Members[i].JoinedOn = nil
	}
	old, err := json.Marshal(oldDoc)
	if err != nil {
		t.Fatalf("marshaling old-shaped document: %v", err)
	}

	parsed, err := parseUploadBytes(t, zipOf(t, old, map[string][]byte{receiptFilename: realJPEGBytes(t)}))
	if err != nil {
		t.Fatalf("ParseUpload(file with null joined_on) = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	restored, err := store.New(destDB).ListMembersByFund(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v, want no error", err)
	}
	if len(restored) != len(doc.Members) {
		t.Fatalf("restored %d members, want %d", len(restored), len(doc.Members))
	}
	for _, m := range restored {
		if m.JoinedOn != "2025-12-20" {
			t.Errorf("member %d restored with joined_on %q, want the adoption date %q", m.ID, m.JoinedOn, "2025-12-20")
		}
	}
	// The backfilled tier row reaches back to that adoption month, so the dues the
	// null date meant are priced from it too.
	tiers, err := store.New(destDB).ListMemberTiersForExport(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListMemberTiersForExport() = %v, want no error", err)
	}
	if len(tiers) != 1 || tiers[0].EffectiveFrom != "2025-12" || tiers[0].Correction != 0 {
		t.Errorf("restored member_tier rows = %+v, want Andi's one row from 2025-12 at correction 0", tiers)
	}
}

// A fund that never posted an opening balance starts its history the day it
// was created, read in the fund's own zone (ADR-041): 17:00Z is already the
// next day in WIB, and 15:00Z already in WIT.
func TestAdoptionDatesFallsBackToTheFundsCreationDayInItsOwnZone(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC).Unix()
	got, err := adoptionDates(Document{Funds: []Fund{
		{ID: 7, CreatedAt: created, TimeZone: tz.Jakarta},
		{ID: 8, CreatedAt: created - 2*3600, TimeZone: tz.Jayapura},
		{ID: 9, CreatedAt: created - 3600, TimeZone: tz.Makassar},
		{ID: 10, CreatedAt: created - 1, TimeZone: tz.Jakarta},
	}})
	if err != nil {
		t.Fatalf("adoptionDates() = %v, want no error", err)
	}
	for id, want := range map[int64]string{7: "2026-09-01", 8: "2026-09-01", 9: "2026-09-01", 10: "2026-08-31"} {
		if got[id] != want {
			t.Errorf("adoptionDates()[%d] = %q, want %q", id, got[id], want)
		}
	}
	if _, err := adoptionDates(Document{Funds: []Fund{{ID: 1, TimeZone: ""}}}); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("adoptionDates(empty zone) = %v, want ErrMalformedBackup", err)
	}
}

// TestRestoreCarriesTheRateTrailAndLeavesTheUpdateTriggersAlone is ADR-040's
// claim about restore: an UPDATE-only trigger never fires on restore's deletes
// and inserts, so dues_rate_immutable_update (like member_tier's) needs no place
// in immutableTriggers. The trail survives the round trip, the standing view
// reads it as before, and both triggers still refuse after a restore that
// replaced an existing trail.
func TestRestoreCarriesTheRateTrailAndLeavesTheUpdateTriggersAlone(t *testing.T) {
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

	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed) // over an existing trail

	ctx := context.Background()
	q := store.New(destDB)
	all, err := q.ListDuesRatesByFund(ctx, 1)
	if err != nil {
		t.Fatalf("ListDuesRatesByFund() = %v, want no error", err)
	}
	type trail struct {
		from       string
		amount     int64
		correction int64
		deleted    int64
	}
	var got []trail
	for _, r := range all {
		got = append(got, trail{r.EffectiveFrom, r.Amount, r.Correction, r.Deleted})
	}
	want := []trail{{"2026-01", 50000, 0, 0}, {"2026-01", 55000, 1, 0}, {"2026-06", 70000, 0, 0}, {"2026-06", 70000, 1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("restored dues_rate rows = %+v, want %+v: the superseded and deleting rows are the trail", got, want)
	}

	standing, err := q.ListDuesRatesByTier(ctx, all[0].TierID)
	if err != nil || len(standing) != 1 || standing[0].Amount != 55000 || standing[0].EffectiveFrom != "2026-01" {
		t.Errorf("standing rates after restore = %+v, %v, want January's corrected 55000 only (June was deleted)", standing, err)
	}

	for _, stmt := range []string{
		`UPDATE dues_rate SET amount = 1 WHERE id = 1`,
		`UPDATE member_tier SET tier_id = NULL WHERE id = 1`,
	} {
		if _, err := destDB.ExecContext(ctx, stmt); err == nil || !strings.Contains(err.Error(), "are immutable") {
			t.Errorf("%s after a restore = %v, want the immutability trigger to refuse", stmt, err)
		}
	}
}
