package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/backup"
)

// The refusals below are what the treasurer actually reads when a restore
// cannot go ahead: each wire code maps to its own sentence in
// copy.common.errors, so a code drifting (or collapsing into
// internal_error) would show her the wrong reason. internal/backup's tests
// cover the sentinel errors themselves; these cover the mapping, end to
// end through the router.

func decodeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding error envelope: %v (body: %s)", err, rec.Body.String())
	}
	return envelope.Error.Code
}

func assertAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, status, rec.Body.String())
	}
	if got := decodeErrorCode(t, rec); got != code {
		t.Errorf("error code = %q, want %q", got, code)
	}
}

// rewriteZip copies src entry by entry, letting edit replace any entry's
// bytes - how a test turns a real, valid backup into one that fails exactly
// one check.
func rewriteZip(t *testing.T, src []byte, edit func(name string, data []byte) []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", f.Name, err)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("creating %s: %v", f.Name, err)
		}
		if _, err := w.Write(edit(f.Name, data)); err != nil {
			t.Fatalf("writing %s: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func TestRestoreInspectRefusesANewerFormatBackup(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := postRestoreInspect(t, r, minimalDumpZip(t, backup.FormatVersion+1))
	assertAPIError(t, rec, http.StatusUnprocessableEntity, "restore_format_too_new")
}

func TestRestoreInspectRefusesTooManyEntries(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i <= backup.MaxZipEntries; i++ {
		if _, err := zw.Create("receipts/" + strconv.Itoa(i) + ".jpg"); err != nil {
			t.Fatalf("creating entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}

	rec := postRestoreInspect(t, r, buf.Bytes())
	assertAPIError(t, rec, http.StatusRequestEntityTooLarge, "restore_file_too_large")
}

// A real backup whose one receipt image has been replaced with bytes that
// are not an image at all.
func TestRestoreInspectRefusesABadReceiptImage(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)
	photo := encodeTestJPEG(t, solidBlockImage(40, 30, 10, 10, color.RGBA{255, 255, 255, 255}, color.RGBA{200, 0, 0, 255}))
	if rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", photo); rec.Code != http.StatusCreated {
		t.Fatalf("uploading receipt = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	tampered := rewriteZip(t, downloadRealBackupZip(t, r), func(name string, data []byte) []byte {
		if strings.HasPrefix(name, "receipts/") {
			return []byte("not an image")
		}
		return data
	})

	rec := postRestoreInspect(t, r, tampered)
	assertAPIError(t, rec, http.StatusUnprocessableEntity, "restore_bad_image")
}

func TestRestoreInspectRefusesANonMultipartRequest(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/restore/inspect", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusBadRequest, "invalid_argument")
}

// A multipart body carrying some other field but no "file" part: the other
// part is skipped, the reader runs out, and the answer names the missing
// file rather than a parse failure.
func TestRestoreInspectRequiresAFilePart(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField("note", "no file here"); err != nil {
		t.Fatalf("writing field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/restore/inspect", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusBadRequest, "invalid_argument")
	assertRestoreStagingFileExists(t, backupDir, false)
}

// A multipart body cut off mid-stream (no closing boundary): the read
// fails partway, which is a bad request, not a server error.
func TestRestoreInspectRefusesATruncatedUpload(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	body := "--b\r\nContent-Disposition: form-data; name=\"file\"; filename=\"backup.zip\"\r\n\r\nPK\x03\x04 truncated"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/restore/inspect", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	r.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusBadRequest, "invalid_argument")
}

// A backup whose totals block disagrees with its own rows passes inspect
// (ParseUpload does not recompute totals) and is refused at confirm, after
// the transaction has been rolled back - the one failure the treasurer can
// meet after typing her password.
func TestRestoreConfirmRefusesATotalsMismatch(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	tampered := rewriteZip(t, downloadRealBackupZip(t, r), func(name string, data []byte) []byte {
		if name != "uruni.json" {
			return data
		}
		var doc map[string]any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("decoding uruni.json: %v", err)
		}
		fund := doc["totals"].(map[string]any)["funds"].([]any)[0].(map[string]any)
		balance, err := fund["balance"].(json.Number).Int64()
		if err != nil {
			t.Fatalf("reading fund balance: %v", err)
		}
		fund["balance"] = balance + 1
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("encoding uruni.json: %v", err)
		}
		return out
	})

	token := decodeInspectToken(t, postRestoreInspect(t, r, tampered))
	rec := postRestoreConfirm(t, r, token, fixturePassword)
	assertAPIError(t, rec, http.StatusUnprocessableEntity, "restore_totals_mismatch")

	// Nothing changed: the session that confirmed is still valid.
	fundRec := httptest.NewRecorder()
	r.ServeHTTP(fundRec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if fundRec.Code != http.StatusOK {
		t.Errorf("GET /api/fund after a refused restore = %d, want %d", fundRec.Code, http.StatusOK)
	}
	assertRestoreStagingFileExists(t, backupDir, false)
}

// Confirm re-checks the password, so it shares login's limiter (its own
// key): enough wrong guesses lock it before the password is even looked at.
func TestRestoreConfirmIsRateLimited(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)
	token := decodeInspectToken(t, postRestoreInspect(t, r, downloadRealBackupZip(t, r)))

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := postRestoreConfirm(t, r, token, "wrong-guess"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}
	rec := postRestoreConfirm(t, r, token, fixturePassword)
	assertAPIError(t, rec, http.StatusTooManyRequests, "too_many_requests")
}

// An expired stage is noticed on its next access: take refuses it and
// removes the staged file itself, since nobody will confirm it now.
func TestRestoreStageExpiryRemovesTheStagedFile(t *testing.T) {
	dir := t.TempDir()
	path := createRestoreStagingTempOrFatal(t, dir)

	s := newRestoreStage()
	token, err := s.stage(backup.ParsedUpload{StagingPath: path})
	if err != nil {
		t.Fatalf("stage() = %v, want no error", err)
	}
	s.mu.Lock()
	s.expires = time.Now().Add(-time.Second)
	s.mu.Unlock()

	if _, ok := s.take(token); ok {
		t.Fatal("take() on an expired stage = ok, want refused")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired stage's file still on disk (stat err = %v)", err)
	}
}
