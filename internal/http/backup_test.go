package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func getBackup(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backup", nil))
	return rec
}

// TestDownloadBackupRequiresASession is the auth-gate half of the DoD -
// session_gate_test.go's table-driven sweep already covers "401 with no
// cookie at all" for every gated route including this one; this test is the
// same fact told directly, against a router this file builds itself so it
// can also cover the success path below without a second router.
func TestDownloadBackupRequiresASession(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir())

	rec := getBackup(t, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/backup (no session) = %d, want %d (body: %s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestDownloadBackupReturnsAZipWithJSONAndReceipt is the route's own
// end-to-end shape: a session-gated GET answers with a zip carrying
// uruni.json (decodable, format_version set) and, once a receipt has been
// uploaded through the ordinary upload route, a receipts/ entry for it.
func TestDownloadBackupReturnsAZipWithJSONAndReceipt(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	fixture := encodeTestJPEG(t, solidBlockImage(64, 64, 16, 16, fixtureBG, fixtureBlock))
	uploadRec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", uploadRec.Code, http.StatusCreated, uploadRec.Body.String())
	}

	rec := getBackup(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backup = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/zip")
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="uruni-`) || !strings.HasSuffix(cd, `.zip"`) {
		t.Errorf("Content-Disposition = %q, want a dated uruni-YYYY-MM-DD.zip attachment", cd)
	}

	zipBytes := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader() = %v, want no error", err)
	}

	jsonFile, err := zr.Open("uruni.json")
	if err != nil {
		t.Fatalf("zip has no uruni.json: %v", err)
	}
	var doc backup.Document
	if err := json.NewDecoder(jsonFile).Decode(&doc); err != nil {
		t.Fatalf("decoding uruni.json: %v", err)
	}
	_ = jsonFile.Close()

	if doc.FormatVersion != backup.FormatVersion {
		t.Errorf("format_version = %d, want %d", doc.FormatVersion, backup.FormatVersion)
	}
	if len(doc.Transactions) == 0 {
		t.Error("uruni.json has no transactions, want at least the ones setup and the fixture posted")
	}
	if len(doc.Receipts) != 1 {
		t.Fatalf("len(uruni.json receipts) = %d, want 1", len(doc.Receipts))
	}

	wantEntry := "receipts/" + doc.Receipts[0].Path
	found := false
	for _, f := range zr.File {
		if f.Name == wantEntry {
			found = true
		}
	}
	if !found {
		t.Errorf("zip has no %s entry for the uploaded receipt", wantEntry)
	}
}

// authedRouterWithBackupDir is authedRouterFor's own recipe (router_test.go),
// but built directly here rather than reused, so this file can hold onto
// the backupDir it passed to New - the tests below need to seed dump files
// into it directly, since these routes only ever read URUNI_BACKUP_DIR and
// nothing in this package writes to it through the HTTP surface itself.
func authedRouterWithBackupDir(t *testing.T) (http.Handler, string) {
	t.Helper()
	sqlDB := testStoreDB(t)
	backupDir := t.TempDir()
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), backupDir)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("fixture POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	token := sessionCookie(reg)
	if token == "" {
		t.Fatal("fixture register set no session cookie")
	}
	return withSessionCookie{Handler: r, token: token}, backupDir
}

func getBackups(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backups", nil))
	return rec
}

func getStoredBackup(t *testing.T, r http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backups/"+name, nil))
	return rec
}

func writeTestDumpFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("writing fixture dump %s: %v", name, err)
	}
}

// TestListBackupsIsEmptyWithNoDumpsYet: a fresh instance with no scheduled
// dump yet answers with an empty array, never null and never an error.
func TestListBackupsIsEmptyWithNoDumpsYet(t *testing.T) {
	r, _ := authedRouterWithBackupDir(t)

	rec := getBackups(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backups = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("GET /api/backups body = %q, want []", body)
	}
}

// TestListBackupsReturnsEveryDumpNewestFirstWithFormatFlag lists a mix of
// current- and older-format dumps and checks every field the Cadangan card
// reads: date, kind, format_version, is_current_format and size, newest
// dump first.
func TestListBackupsReturnsEveryDumpNewestFirstWithFormatFlag(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)

	oldName := backup.BuildDumpName(time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC), backup.KindDaily, backup.FormatVersion-1, "aaaaaaaaaaaaaaaa")
	newName := backup.BuildDumpName(time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), backup.KindPreRestore, backup.FormatVersion, "bbbbbbbbbbbbbbbb")
	writeTestDumpFile(t, backupDir, oldName, []byte("older format stub"))
	writeTestDumpFile(t, backupDir, newName, []byte("current format stub, a bit longer"))

	rec := getBackups(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backups = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []backupListItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	newest := items[0]
	if newest.Name != newName {
		t.Errorf("items[0].Name = %q, want the newest dump %q", newest.Name, newName)
	}
	if newest.Kind != string(backup.KindPreRestore) {
		t.Errorf("items[0].Kind = %q, want %q", newest.Kind, backup.KindPreRestore)
	}
	if newest.Date != "2026-09-30" {
		t.Errorf("items[0].Date = %q, want 2026-09-30", newest.Date)
	}
	if !newest.IsCurrentFormat {
		t.Error("items[0].IsCurrentFormat = false, want true")
	}
	if newest.SizeBytes != int64(len("current format stub, a bit longer")) {
		t.Errorf("items[0].SizeBytes = %d, want the file's real size", newest.SizeBytes)
	}

	oldest := items[1]
	if oldest.Name != oldName {
		t.Errorf("items[1].Name = %q, want the older dump %q", oldest.Name, oldName)
	}
	if oldest.IsCurrentFormat {
		t.Error("items[1].IsCurrentFormat = true, want false - it was written by an older FormatVersion")
	}
	if oldest.Kind != string(backup.KindDaily) {
		t.Errorf("items[1].Kind = %q, want %q", oldest.Kind, backup.KindDaily)
	}
}

// TestDownloadStoredBackupServesExactBytesUnderItsOwnName: a listed dump
// downloads byte for byte, with a Content-Disposition naming it exactly.
func TestDownloadStoredBackupServesExactBytesUnderItsOwnName(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)
	name := backup.BuildDumpName(time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), backup.KindDaily, backup.FormatVersion, "cccccccccccccccc")
	want := []byte("a fixture zip's stub bytes")
	writeTestDumpFile(t, backupDir, name, want)

	rec := getStoredBackup(t, r, name)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backups/%s = %d, want %d (body: %s)", name, rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="`+name+`"`) {
		t.Errorf("Content-Disposition = %q, want it to name %q", cd, name)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Errorf("body = %q, want %q", rec.Body.Bytes(), want)
	}
}

// TestDownloadStoredBackupRejectsUnknownName: a syntactically valid name
// that simply does not exist (never written, or already pruned by
// retention) is a plain 404, not a 500 from a failed file read.
func TestDownloadStoredBackupRejectsUnknownName(t *testing.T) {
	r, _ := authedRouterWithBackupDir(t)
	name := backup.BuildDumpName(time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), backup.KindDaily, backup.FormatVersion, "dddddddddddddddd")

	rec := getStoredBackup(t, r, name)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/backups/%s = %d, want %d (body: %s)", name, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestDownloadStoredBackupRefusesPathTraversal is the download route's own
// security property: nothing that is not exactly a dump name reaches
// os.ReadFile, whatever chi's own route matching lets through as the
// {name} segment.
func TestDownloadStoredBackupRefusesPathTraversal(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)

	// A real secret sitting next to the backups this instance actually
	// wrote - proof that a traversal attempt cannot reach it.
	secretPath := filepath.Join(filepath.Dir(backupDir), "secret.txt")
	if err := os.WriteFile(secretPath, []byte("should never be servable"), 0o600); err != nil {
		t.Fatalf("writing sibling secret file: %v", err)
	}

	cases := []string{
		"..%2Fsecret.txt",
		"....%2F%2F....%2F%2Fsecret.txt",
		"uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip%00.txt",
		"not-a-dump-at-all.zip",
	}
	for _, name := range cases {
		rec := getStoredBackup(t, r, name)
		if rec.Code == http.StatusOK {
			t.Errorf("GET /api/backups/%s = 200, want it refused (body: %s)", name, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "should never be servable") {
			t.Errorf("GET /api/backups/%s leaked the sibling file's contents", name)
		}
	}
}
