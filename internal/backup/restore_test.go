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
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// realJPEGBytes is a genuine, decodable 1x1 JPEG - buildFixture's own
// receiptFileBytes is deliberately *not* a real image (its own comment:
// "Export never decodes it, only copies it"), which restore's own ADR-011
// decode check now does care about. Tests that round-trip through
// ParseUpload replace the fixture's stub bytes with this, on disk only, in
// their own uploadsDir - never touching fixture_test.go's shared helper or
// golden_test.go's separate database.
func realJPEGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encoding fixture JPEG: %v", err)
	}
	return buf.Bytes()
}

// exportFixture is the golden test's own recipe (golden_test.go), reused
// here: a real, deterministic fund built through store.Queries directly,
// exported through BuildDocument/BuildZip, ready to feed back into
// ParseUpload/Restore as if it had just been uploaded.
func exportFixture(t *testing.T, sqlDB *sql.DB, uploadsDir string) (zipBytes []byte, doc Document) {
	t.Helper()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)
	doc, receipts, err := BuildDocument(ctx, q, l)
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
// destDB actually holds after a restore - the round trip's own proof,
// independent of Restore's own internal verifyTotals check (which runs
// inside the transaction, before this test ever gets to look).
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

// assertDocumentsEqual compares two exports byte-for-byte via their own
// canonical JSON encoding - the same shape TestGoldenFixtureMatchesExport
// already trusts for "these two documents are the same data" - except
// Users, which is cleared on both sides first: the login ruling (ADR-012
// amendment, #325) makes the live user row and the file's own Users
// independent on purpose, so a restored instance's Users is never expected
// to match the file it was restored from.
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
	q := store.New(destDB)
	l := ledger.New(destDB)
	if err := Restore(context.Background(), destDB, q, l, uploadsDir, backupDir, parsed, time.Unix(1800000000, 0)); err != nil {
		t.Fatalf("Restore() = %v, want no error", err)
	}
}

// TestRestoreRoundTripGoldenFixture is ADR-012's own requirement: export ->
// fresh database -> import -> identical balances - on the fixture that
// already carries a reversal, a transfer, a named contribution, a settled
// reimbursement with its receipt, and a reconciliation.
func TestRestoreRoundTripGoldenFixture(t *testing.T) {
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
	gotDoc, _, err := BuildDocument(ctx, store.New(destDB), ledger.New(destDB))
	if err != nil {
		t.Fatalf("BuildDocument(dest) = %v, want no error", err)
	}
	assertDocumentsEqual(t, gotDoc, wantDoc)

	//nolint:gosec // destUploads and receiptFilename are both this test's own fixture values, never request input
	gotReceipt, err := os.ReadFile(filepath.Join(destUploads, receiptFilename))
	if err != nil {
		t.Fatalf("reading restored receipt file: %v", err)
	}
	if string(gotReceipt) != string(realReceipt) {
		t.Errorf("restored receipt bytes do not match the original")
	}
}

// TestRestoreRoundTripWithClosedEnvelope adds the one shape buildFixture's
// own envelope leaves out: a closed incidental (ADR-034's closed_on).
func TestRestoreRoundTripWithClosedEnvelope(t *testing.T) {
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}

	q := store.New(srcDB)
	// buildFixture's own envelope purpose is the sole incidental it wrote -
	// find it by IncidentalClosedOnForPurpose-shaped lookup: list the
	// fund's incidentals and close the one there is.
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
	gotDoc, _, err := BuildDocument(context.Background(), store.New(destDB), ledger.New(destDB))
	if err != nil {
		t.Fatalf("BuildDocument(dest) = %v, want no error", err)
	}
	assertDocumentsEqual(t, gotDoc, wantDoc)
}

func strPtr(s string) *string { return &s }

// minimalDocJSON is the smallest valid uruni.json ParseUpload will accept
// on its own (every "always an array" field present, formatVersion
// correct) - the refusal-case tests below start from this and mutate
// exactly the one thing each test means to break.
func minimalDocJSON(t *testing.T, formatVersion int64) []byte {
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
// entry per key in receipts - the same two-entry-kind shape BuildZip
// produces, built by hand so a refusal-case test can hand it something
// BuildZip itself would never write (an unknown field, a bad image, a
// mismatched format_version, extra junk entries).
// parseUploadBytes is every test's own stand-in for the staging file
// stageAndInspect (internal/http/restore.go) writes before ever calling
// ParseUpload - this package's own tests build a zip in memory (zipOf,
// exportFixture) for convenience, but ParseUpload itself only ever reads
// from disk (issue #344), so every call site writes that zip to a fresh
// temp file first.
func parseUploadBytes(t *testing.T, data []byte) (ParsedUpload, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upload.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing zip fixture to disk: %v", err)
	}
	return ParseUpload(path)
}

func zipOf(t *testing.T, jsonBytes []byte, receipts map[string][]byte) []byte {
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
	_, err := parseUploadBytes(t, zipOf(t, minimalDocJSON(t, FormatVersion+1), nil))
	if !errors.Is(err, ErrFormatVersionNewer) {
		t.Errorf("ParseUpload() error = %v, want ErrFormatVersionNewer", err)
	}
	if errors.Is(err, ErrFormatVersionOlder) {
		t.Errorf("ParseUpload() error also matches ErrFormatVersionOlder, want the two kept distinct")
	}
}

func TestParseUploadRefusesOlderFormatVersionDistinctly(t *testing.T) {
	_, err := parseUploadBytes(t, zipOf(t, minimalDocJSON(t, FormatVersion-1), nil))
	if !errors.Is(err, ErrFormatVersionOlder) {
		t.Errorf("ParseUpload() error = %v, want ErrFormatVersionOlder", err)
	}
	if errors.Is(err, ErrFormatVersionNewer) {
		t.Errorf("ParseUpload() error also matches ErrFormatVersionNewer, want the two kept distinct")
	}
}

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

	// No receipts/ entry at all for missing.jpg - ruling 5.
	parsed, err := parseUploadBytes(t, zipOf(t, b, nil))
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error (a missing image never blocks a restore)", err)
	}
	if len(parsed.Document.Receipts) != 1 {
		t.Fatalf("parsed.Document.Receipts = %v, want the one row preserved", parsed.Document.Receipts)
	}
}

// TestRestoreTotalsMismatchRollsBack hand-builds a ParsedUpload whose
// totals block disagrees with the rows it carries (bypassing ParseUpload,
// which never itself computes a mismatched total) and checks Restore
// refuses to commit and leaves the destination database exactly as it was.
func TestRestoreTotalsMismatchRollsBack(t *testing.T) {
	destDB := newTestDB(t)
	seedOneUser(t, destDB)

	doc := Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Accounts: []Account{}, Purposes: []Purpose{}, DuesTiers: []DuesTier{},
		DuesRates: []DuesRate{}, Members: []Member{}, Transfers: []Transfer{}, Reimbursements: []Reimbursement{},
		Transactions: []Transaction{}, Receipts: []Receipt{}, Reconciliations: []Reconciliation{},
		ReconciliationLines: []ReconciliationLine{}, Incidentals: []Incidental{}, IncidentalRecipients: []IncidentalRecipient{},
		Funds: []Fund{{ID: 1, Name: "Dana", Currency: "IDR", ReportSlug: "0000000000000000000slug01", CreatedAt: 1}},
		Totals: Totals{
			Funds:    []FundTotal{{FundID: 1, Balance: 999}}, // the fund carries zero transactions, so its real balance is 0
			Accounts: []AccountTotal{}, Purposes: []PurposeTotal{},
		},
	}

	err := Restore(context.Background(), destDB, store.New(destDB), ledger.New(destDB), t.TempDir(), t.TempDir(), ParsedUpload{Document: doc}, time.Unix(1800000000, 0))
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

// TestRestoreFailureRemovesOnlyTheReceiptsItCreated forces a totals
// mismatch after receipt extraction has run: the image this restore wrote
// must be gone again, so "nothing was changed" stays true - but an image
// that was already on disk under the same name (the same file, ADR-011's
// random names) must survive, since this restore never created it.
func TestRestoreFailureRemovesOnlyTheReceiptsItCreated(t *testing.T) {
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

			err = Restore(context.Background(), destDB, store.New(destDB), ledger.New(destDB), destUploads, t.TempDir(), parsed, time.Unix(1800000000, 0))
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
// unwritable backup directory) and checks Restore aborts before touching
// the database at all.
func TestRestoreSafetyNetDumpFailureAborts(t *testing.T) {
	destDB := newTestDB(t)
	seedOneUser(t, destDB)

	// A file, not a directory - os.CreateTemp inside it fails immediately.
	notADir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing stand-in file: %v", err)
	}

	doc := minimalDocWithOneFund(t)
	err := Restore(context.Background(), destDB, store.New(destDB), ledger.New(destDB), t.TempDir(), notADir, ParsedUpload{Document: doc}, time.Unix(1800000000, 0))
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

// TestRestoreUserRowSurvives is the login ruling (ADR-012 amendment,
// #325): the live user row is untouched by a restore, even though the
// file's own Users are decoded and even though this test's file carries a
// different user entirely.
func TestRestoreUserRowSurvives(t *testing.T) {
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

// TestRestoreRecreatesImmutableTriggers is the drift guard restore.go's own
// comment on immutableTriggers promises: after a restore, every one of the
// eight dropped-then-recreated triggers still refuses exactly the
// update/delete it always has, with the same message.
func TestRestoreRecreatesImmutableTriggers(t *testing.T) {
	// The fixture, not minimalDocWithOneFund: a BEFORE trigger never fires
	// against a statement that matches zero rows, so this test needs a
	// restored database that actually has a row with id 1 in all four
	// protected tables - which buildFixture's own transaction, transfer,
	// reconciliation and reconciliation_line each are.
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

// seedOneUser writes the one row every test that calls Restore against a
// non-fixture database needs first - Restore's own transaction clears every
// fund-scoped table but never "user", and a database with no user row at
// all is not a shape a live instance is ever actually in.
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

// minimalDocWithOneFund is the smallest Document Restore can actually
// commit: one fund, no accounts/purposes/transactions at all, and a
// correctly-zeroed totals block (an empty fund's own balance is always 0).
func minimalDocWithOneFund(t *testing.T) Document {
	t.Helper()
	return Document{
		FormatVersion: FormatVersion,
		Users:         []User{}, Accounts: []Account{}, Purposes: []Purpose{}, DuesTiers: []DuesTier{},
		DuesRates: []DuesRate{}, Members: []Member{}, Transfers: []Transfer{}, Reimbursements: []Reimbursement{},
		Transactions: []Transaction{}, Receipts: []Receipt{}, Reconciliations: []Reconciliation{},
		ReconciliationLines: []ReconciliationLine{}, Incidentals: []Incidental{}, IncidentalRecipients: []IncidentalRecipient{},
		Funds: []Fund{{ID: 1, Name: "Dana", Currency: "IDR", ReportSlug: "0000000000000000000slug01", CreatedAt: 1}},
		Totals: Totals{
			Funds: []FundTotal{{FundID: 1, Balance: 0}}, Accounts: []AccountTotal{}, Purposes: []PurposeTotal{},
		},
	}
}

// newestMomentEpoch/newestMomentDate are one fixed instant and its own
// Asia/Jakarta calendar date, used only to check that Preview.Date reads
// created_at rather than occurred_on or anything else.
const (
	newestMomentEpoch = 1767225600 // 2026-01-01T00:00:00Z = 2026-01-01 07:00 WIB
	newestMomentDate  = "2026-01-01"
)

// TestBuildPreviewReportsKeptRemovedAndAddedFunds is issue #325's own
// per-fund confirm-step line: a fund in both sides is "kept" and names how
// many of its live transactions sit past the file's own cutoff for it, a
// live-only fund is "removed" outright, and a file-only fund is "added".
func TestBuildPreviewReportsKeptRemovedAndAddedFunds(t *testing.T) {
	destDB := newTestDB(t)
	ctx := context.Background()
	q := store.New(destDB)

	// Fund 1 lives on both sides: the file's own newest transaction for it
	// is id 1 (2026-01-01); two more were posted live afterward and would
	// be lost.
	kept, err := q.CreateFund(ctx, store.CreateFundParams{Name: "Kept Fund", Currency: "IDR", ReportSlug: "0000000000000000000keptfund", CreatedAt: 1})
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

	// Fund 2 lives only live - removed by the restore.
	removed, err := q.CreateFund(ctx, store.CreateFundParams{Name: "Removed Fund", Currency: "IDR", ReportSlug: "0000000000000000000removedf", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateFund(removed): %v", err)
	}

	doc := Document{
		FormatVersion: FormatVersion,
		Funds: []Fund{
			{ID: kept.ID, Name: "Kept Fund (restored name)", Currency: "IDR", ReportSlug: kept.ReportSlug, CreatedAt: 1},
			{ID: 999, Name: "Added Fund", Currency: "IDR", ReportSlug: "0000000000000000000addedfund", CreatedAt: 1},
		},
		Transactions: []Transaction{
			// CreatedAt, not OccurredOn, is what Preview.Date reads (its
			// own comment: the newest created_at stands in for "when was
			// this backup taken").
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
