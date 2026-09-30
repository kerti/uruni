package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// fixturePassword is testRouter's own registered password
// (authedRouterFor, router_test.go) - the confirm step checks against
// exactly this.
const fixturePassword = "correct-horse-battery"

func postRestoreInspect(t *testing.T, r http.Handler, zipBytes []byte) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", "backup.zip")
	if err != nil {
		t.Fatalf("creating multipart file field: %v", err)
	}
	if _, err := part.Write(zipBytes); err != nil {
		t.Fatalf("writing multipart zip data: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/restore/inspect", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(rec, req)
	return rec
}

func postRestoreInspectStored(t *testing.T, r http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/restore/inspect-stored/"+name, nil))
	return rec
}

func postRestoreConfirm(t *testing.T, r http.Handler, token, password string) *httptest.ResponseRecorder {
	t.Helper()
	//nolint:gosec // not a credential leak - this is the request body POST /api/restore/confirm's own contract requires
	b, err := json.Marshal(restoreConfirmRequest{Token: token, Password: password})
	if err != nil {
		t.Fatalf("marshaling restore confirm request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/restore/confirm", bytes.NewReader(b)))
	return rec
}

// downloadRealBackupZip is every restore test's own fixture: GET /api/backup
// against a router that already has a fund, so ParseUpload is handed a
// real, valid zip rather than one hand-built in this package.
func downloadRealBackupZip(t *testing.T, r http.Handler) []byte {
	t.Helper()
	rec := getBackup(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backup = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// TestRestoreInspectThenConfirmHappyPath is the whole two-step flow, end to
// end: inspect answers a preview naming the one live fund as "kept" with
// nothing lost (the file is this same database's own backup), confirm with
// the right password restores it and clears every session - the same
// cookie that just confirmed is unauthenticated on the very next request.
func TestRestoreInspectThenConfirmHappyPath(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	zipBytes := downloadRealBackupZip(t, r)

	inspectRec := postRestoreInspect(t, r, zipBytes)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect = %d, want %d (body: %s)", inspectRec.Code, http.StatusOK, inspectRec.Body.String())
	}
	var inspected restoreInspectResponse
	if err := json.NewDecoder(inspectRec.Body).Decode(&inspected); err != nil {
		t.Fatalf("decoding inspect response: %v", err)
	}
	if inspected.Token == "" {
		t.Fatal("inspect response carries no token")
	}
	if len(inspected.Preview.Funds) != 1 {
		t.Fatalf("preview.Funds = %v, want exactly the one live fund", inspected.Preview.Funds)
	}
	fp := inspected.Preview.Funds[0]
	if fp.FundID != setup.Fund.ID || fp.Status != backup.FundKept || fp.TransactionsLost != 0 {
		t.Errorf("preview fund = %+v, want FundID=%d Status=kept TransactionsLost=0", fp, setup.Fund.ID)
	}

	confirmRec := postRestoreConfirm(t, r, inspected.Token, fixturePassword)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm = %d, want %d (body: %s)", confirmRec.Code, http.StatusOK, confirmRec.Body.String())
	}

	// The very same (fixed, auto-attached) session cookie now names no
	// session row at all - every session is gone after a restore.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund after restore = %d, want %d (every session must be cleared)", rec.Code, http.StatusUnauthorized)
	}
}

// TestRestoreConfirmRefusesWrongPassword checks the current password, not
// anything decoded out of the file - and leaves the session (and the
// staged upload) alone on a wrong guess.
func TestRestoreConfirmRefusesWrongPassword(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)
	zipBytes := downloadRealBackupZip(t, r)

	inspectRec := postRestoreInspect(t, r, zipBytes)
	var inspected restoreInspectResponse
	if err := json.NewDecoder(inspectRec.Body).Decode(&inspected); err != nil {
		t.Fatalf("decoding inspect response: %v", err)
	}

	rec := postRestoreConfirm(t, r, inspected.Token, "definitely-the-wrong-password")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/restore/confirm (wrong password) = %d, want %d (body: %s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	// The session survives a refused confirm - a gated route still answers.
	fundRec := httptest.NewRecorder()
	r.ServeHTTP(fundRec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if fundRec.Code != http.StatusOK {
		t.Errorf("GET /api/fund after a refused confirm = %d, want %d (session must survive)", fundRec.Code, http.StatusOK)
	}
}

// TestRestoreConfirmWithUnknownTokenIsGone covers confirming with no prior
// inspect at all - the token names nothing staged.
func TestRestoreConfirmWithUnknownTokenIsGone(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := postRestoreConfirm(t, r, "not-a-real-token", fixturePassword)
	if rec.Code != http.StatusGone {
		t.Fatalf("POST /api/restore/confirm (unknown token) = %d, want %d (body: %s)", rec.Code, http.StatusGone, rec.Body.String())
	}
}

// TestRestoreInspectRefusesAMalformedFile is the HTTP wiring's own proof
// that ParseUpload's sentinel errors reach the wire as a distinct code -
// restore_test.go (internal/backup) already covers ParseUpload itself in
// depth; this is only the mapping.
func TestRestoreInspectRefusesAMalformedFile(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := postRestoreInspect(t, r, []byte("not a zip file at all"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/restore/inspect (garbage body) = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if envelope.Error.Code != "restore_invalid_file" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "restore_invalid_file")
	}
}

// minimalDumpZip builds a zip holding only {"format_version": formatVersion}
// under uruni.json - just enough for backup.ParseUpload's own
// checkFormatVersion peek to fire before any strict decode, which is all
// TestRestoreInspectStoredRefusesOlderFormatBackup below needs to prove the
// stored-backup inspect route goes through the exact same check an uploaded
// zip would.
func minimalDumpZip(t *testing.T, formatVersion int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("uruni.json")
	if err != nil {
		t.Fatalf("creating uruni.json entry: %v", err)
	}
	if _, err := fmt.Fprintf(w, `{"format_version":%d}`, formatVersion); err != nil {
		t.Fatalf("writing uruni.json entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// TestRestoreInspectStoredHappyPath is #326's whole point: the same inspect
// step as an upload, but reading a name straight out of backupDir. The dump
// used here is a real GET /api/backup zip of the router's own live fund,
// written under a name the production dump-writer's own BuildDumpName
// shape would produce, so ParseUpload sees exactly what a real scheduled
// dump would hand it.
func TestRestoreInspectStoredHappyPath(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)
	setup := setUpFund(t, r)

	zipBytes := downloadRealBackupZip(t, r)
	name := backup.BuildDumpName(time.Now(), backup.KindDaily, backup.FormatVersion, "eeeeeeeeeeeeeeee")
	writeTestDumpFile(t, backupDir, name, zipBytes)

	inspectRec := postRestoreInspectStored(t, r, name)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", name, inspectRec.Code, http.StatusOK, inspectRec.Body.String())
	}
	var inspected restoreInspectResponse
	if err := json.NewDecoder(inspectRec.Body).Decode(&inspected); err != nil {
		t.Fatalf("decoding inspect response: %v", err)
	}
	if inspected.Token == "" {
		t.Fatal("inspect response carries no token")
	}
	if len(inspected.Preview.Funds) != 1 || inspected.Preview.Funds[0].FundID != setup.Fund.ID {
		t.Fatalf("preview.Funds = %v, want exactly the one live fund %d", inspected.Preview.Funds, setup.Fund.ID)
	}

	// The token behaves exactly like one from the upload route: confirm
	// restores it and clears every session, this one included - no second
	// restore implementation, same tail.
	confirmRec := postRestoreConfirm(t, r, inspected.Token, fixturePassword)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm = %d, want %d (body: %s)", confirmRec.Code, http.StatusOK, confirmRec.Body.String())
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund after restore = %d, want %d (every session must be cleared)", rec.Code, http.StatusUnauthorized)
	}
}

// TestRestoreInspectStoredRejectsUnknownName: a syntactically valid name
// that was never written (or already pruned) is a plain 404, matching
// downloadStoredBackup's own behaviour for the same case - the shared
// resolveStoredBackupPath validation is what the two routes have in common.
func TestRestoreInspectStoredRejectsUnknownName(t *testing.T) {
	r, _ := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	name := backup.BuildDumpName(time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), backup.KindDaily, backup.FormatVersion, "ffffffffffffffff")

	rec := postRestoreInspectStored(t, r, name)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", name, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestRestoreInspectStoredRefusesPathTraversal mirrors
// TestDownloadStoredBackupRefusesPathTraversal (backup_test.go) - the name
// segment is never trusted as a bare path, whatever chi's own route
// matching lets through as {name}.
func TestRestoreInspectStoredRefusesPathTraversal(t *testing.T) {
	r, _ := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	cases := []string{
		"..%2Fsecret.txt",
		"....%2F%2F....%2F%2Fsecret.txt",
		"uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip%00.txt",
		"not-a-dump-at-all.zip",
	}
	for _, name := range cases {
		rec := postRestoreInspectStored(t, r, name)
		if rec.Code == http.StatusOK {
			t.Errorf("POST /api/restore/inspect-stored/%s = 200, want it refused (body: %s)", name, rec.Body.String())
		}
	}
}

// TestRestoreInspectStoredRefusesOlderFormatBackup: an older-format dump is
// still listed and downloadable (Backup.tsx's own "Dibuat versi lama"
// label), but never offered for restore. This route enforces that itself,
// through the exact same backup.ParseUpload check an uploaded older-format
// zip would hit (ErrFormatVersionOlder) - not a second, duplicated format
// check against backupListItem's IsCurrentFormat flag.
func TestRestoreInspectStoredRefusesOlderFormatBackup(t *testing.T) {
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	name := backup.BuildDumpName(time.Now(), backup.KindDaily, backup.FormatVersion-1, "0123456789ab")
	writeTestDumpFile(t, backupDir, name, minimalDumpZip(t, backup.FormatVersion-1))

	rec := postRestoreInspectStored(t, r, name)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", name, rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if envelope.Error.Code != "restore_format_too_old" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "restore_format_too_old")
	}
}

// TestInspectStoredBackupRequiresASession is the auth-gate half of the DoD,
// matching TestDownloadBackupRequiresASession's own shape for this new
// route - built against a fresh, unauthenticated router rather than
// testRouter/authedRouterWithBackupDir, both of which register and log a
// treasurer in before returning.
func TestInspectStoredBackupRequiresASession(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir())

	rec := postRestoreInspectStored(t, r, "uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/restore/inspect-stored/... (no session) = %d, want %d (body: %s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestRestoreFromStoredSafetyNetBackupEndToEnd is #326's own named
// acceptance criterion: "restoring a safety-net backup works, so a restore
// can itself be undone." It runs the ordinary upload-restore path once
// (which writes a pre-restore safety-net dump to backupDir as a side
// effect, per ADR-012), logs back in since that restore cleared every
// session, then restores again - this time from that very safety-net dump,
// through the new stored-backup route end to end.
func TestRestoreFromStoredSafetyNetBackupEndToEnd(t *testing.T) {
	sqlDB := testStoreDB(t)
	backupDir := t.TempDir()
	raw := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, testLogger(), auth.New(sqlDB), "", t.TempDir(), backupDir)

	const email = "treasurer@example.org"
	reg := postRegister(t, raw, email, fixturePassword)
	if reg.Code != http.StatusCreated {
		t.Fatalf("fixture POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	r1 := withSessionCookie{Handler: raw, token: sessionCookie(reg)}
	setUpFund(t, r1)

	// First restore: an ordinary upload of the router's own current state.
	// Its only interesting side effect here is the pre-restore safety-net
	// dump ADR-012 requires Restore to write before it changes anything.
	zipBytes := downloadRealBackupZip(t, r1)
	firstInspect := postRestoreInspect(t, r1, zipBytes)
	if firstInspect.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect (first) = %d, want %d (body: %s)", firstInspect.Code, http.StatusOK, firstInspect.Body.String())
	}
	var firstInspected restoreInspectResponse
	if err := json.NewDecoder(firstInspect.Body).Decode(&firstInspected); err != nil {
		t.Fatalf("decoding first inspect response: %v", err)
	}
	firstConfirm := postRestoreConfirm(t, r1, firstInspected.Token, fixturePassword)
	if firstConfirm.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm (first) = %d, want %d (body: %s)", firstConfirm.Code, http.StatusOK, firstConfirm.Body.String())
	}

	dumps, err := backup.ListDumps(backupDir)
	if err != nil {
		t.Fatalf("backup.ListDumps() = %v, want no error", err)
	}
	var safetyNet backup.DumpInfo
	found := false
	for _, d := range dumps {
		if d.Kind == backup.KindPreRestore {
			safetyNet, found = d, true
			break
		}
	}
	if !found {
		t.Fatalf("no pre-restore dump found among %v, want the safety net Restore writes", dumps)
	}
	if safetyNet.FormatVersion != backup.FormatVersion {
		t.Fatalf("safety-net dump format_version = %d, want the current %d - it must itself be restorable", safetyNet.FormatVersion, backup.FormatVersion)
	}

	// Every session (including r1's) is gone now - log back in for the
	// second restore, this time of the safety-net dump itself.
	login := postLogin(t, raw, email, fixturePassword)
	if login.Code != http.StatusOK {
		t.Fatalf("POST /api/login (after first restore) = %d, want %d (body: %s)", login.Code, http.StatusOK, login.Body.String())
	}
	r2 := withSessionCookie{Handler: raw, token: sessionCookie(login)}

	secondInspect := postRestoreInspectStored(t, r2, safetyNet.Name)
	if secondInspect.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", safetyNet.Name, secondInspect.Code, http.StatusOK, secondInspect.Body.String())
	}
	var secondInspected restoreInspectResponse
	if err := json.NewDecoder(secondInspect.Body).Decode(&secondInspected); err != nil {
		t.Fatalf("decoding second inspect response: %v", err)
	}

	secondConfirm := postRestoreConfirm(t, r2, secondInspected.Token, fixturePassword)
	if secondConfirm.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm (second, from safety net) = %d, want %d (body: %s)", secondConfirm.Code, http.StatusOK, secondConfirm.Body.String())
	}

	// The second restore's own session is gone too - restoring a safety net
	// is an ordinary restore in every respect, including this one.
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund after the second restore = %d, want %d (every session must be cleared)", rec.Code, http.StatusUnauthorized)
	}
}
