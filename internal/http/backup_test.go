package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), testLogger(), auth.New(sqlDB), "", t.TempDir())

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
