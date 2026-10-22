package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// restoreStagingTempPrefix mirrors backup.CreateRestoreStagingTemp's
// os.CreateTemp pattern (dumps.go's tempPrefix+"restore-*"); tempPrefix is
// unexported, so this test package spells it out. There is no fixed staging
// name (a shared name let two concurrent inspects race), so staging tests
// find their files by this prefix.
const restoreStagingTempPrefix = ".uruni-backup-tmp-restore-"

// restoreStagingFilesIn lists every file currently staged in backupDir, by
// name: never a real dump (ValidDumpName's pattern shares nothing with this
// prefix) and never a RemoveStaleTemps leftover from a dump write (those
// carry no "restore-").
func restoreStagingFilesIn(t *testing.T, backupDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) = %v, want no error", backupDir, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), restoreStagingTempPrefix) {
			names = append(names, e.Name())
		}
	}
	return names
}

// assertRestoreStagingFileExists checks only whether some staging file is
// present; tests that care which, or how many, use restoreStagingFilesIn.
func assertRestoreStagingFileExists(t *testing.T, backupDir string, want bool) {
	t.Helper()
	got := len(restoreStagingFilesIn(t, backupDir)) > 0
	if got != want {
		t.Errorf("a restore staging file is present in %s = %v, want %v", backupDir, got, want)
	}
}

// fixturePassword is testRouter's registered password (authedRouterFor,
// router_test.go), which the confirm step checks against.
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
	//nolint:gosec // not a credential leak - this is the request body POST /api/restore/confirm's contract requires
	b, err := json.Marshal(restoreConfirmRequest{Token: token, Password: password})
	if err != nil {
		t.Fatalf("marshaling restore confirm request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/restore/confirm", bytes.NewReader(b)))
	return rec
}

// downloadRealBackupZip is every restore test's fixture: GET /api/backup
// against a router that already has a fund, so ParseUpload gets a real zip.
func downloadRealBackupZip(t *testing.T, r http.Handler) []byte {
	t.Helper()
	rec := getBackup(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backup = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// TestRestoreInspectThenConfirmHappyPath is the whole two-step flow:
// inspect answers a preview naming the live fund "kept" with nothing lost
// (the file is this database's backup), and confirm with the right password
// restores it and clears every session, so the same cookie is
// unauthenticated on the next request.
func TestRestoreInspectThenConfirmHappyPath(t *testing.T) {
	t.Parallel()
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

	// The same session cookie now names no session row: every session is gone
	// after a restore.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund after restore = %d, want %d (every session must be cleared)", rec.Code, http.StatusUnauthorized)
	}
}

// TestRestoreConfirmRefusesWrongPassword checks the current password, not
// anything decoded from the file, and leaves the session and the staged
// upload alone.
func TestRestoreConfirmRefusesWrongPassword(t *testing.T) {
	t.Parallel()
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

	// The session survives a refused confirm: a gated route still answers.
	fundRec := httptest.NewRecorder()
	r.ServeHTTP(fundRec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if fundRec.Code != http.StatusOK {
		t.Errorf("GET /api/fund after a refused confirm = %d, want %d (session must survive)", fundRec.Code, http.StatusOK)
	}
}

// TestRestoreConfirmWithUnknownTokenIsGone covers confirming with no prior
// inspect: the token names nothing staged.
func TestRestoreConfirmWithUnknownTokenIsGone(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := postRestoreConfirm(t, r, "not-a-real-token", fixturePassword)
	if rec.Code != http.StatusGone {
		t.Fatalf("POST /api/restore/confirm (unknown token) = %d, want %d (body: %s)", rec.Code, http.StatusGone, rec.Body.String())
	}
}

// TestRestoreInspectRefusesAMalformedFile proves ParseUpload's sentinel
// errors reach the wire as distinct codes; internal/backup's tests cover
// ParseUpload itself.
func TestRestoreInspectRefusesAMalformedFile(t *testing.T) {
	t.Parallel()
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
// under uruni.json: enough for checkFormatVersion's peek to fire before any
// strict decode, which is all TestRestoreInspectStoredRefusesOlderFormatBackup
// needs to prove the stored route runs the same check as an upload.
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

// TestRestoreInspectStoredHappyPath (#326): the inspect step as for an
// upload, reading a name from backupDir. The dump is a real GET /api/backup
// zip of the router's live fund, written under a BuildDumpName name, so
// ParseUpload sees what a scheduled dump would hand it.
func TestRestoreInspectStoredHappyPath(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setup := setUpFund(t, r)

	zipBytes := downloadRealBackupZip(t, r)
	name := backup.BuildDumpName(time.Now(), tz.Jakarta, backup.KindDaily, backup.FormatVersion, "eeeeeeeeeeeeeeee")
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

	// The token behaves like one from the upload route: confirm restores it and
	// clears every session, this one included; same tail, no second restore path.
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

// TestRestoreInspectStoredRejectsUnknownName: a valid name never written (or
// pruned) is a 404, as downloadStoredBackup; both share
// resolveStoredBackupPath.
func TestRestoreInspectStoredRejectsUnknownName(t *testing.T) {
	t.Parallel()
	r, _ := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	name := backup.BuildDumpName(time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), tz.Jakarta, backup.KindDaily, backup.FormatVersion, "ffffffffffffffff")

	rec := postRestoreInspectStored(t, r, name)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", name, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestRestoreInspectStoredRefusesPathTraversal mirrors
// TestDownloadStoredBackupRefusesPathTraversal (backup_test.go): the name
// segment is never trusted as a bare path, whatever chi lets through.
func TestRestoreInspectStoredRefusesPathTraversal(t *testing.T) {
	t.Parallel()
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

// TestRestoreInspectStoredAcceptsAnOlderFormatItCanUpgrade: a format-1 dump is
// listed with its restore button (can_restore) and inspects like any other,
// because backup.ParseUpload upgrades it (ADR-012, ADR-040). The route has no
// format check of its own: ParseUpload is the one place judging a version.
func TestRestoreInspectStoredAcceptsAnOlderFormatItCanUpgrade(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	name := backup.BuildDumpName(time.Now(), tz.Jakarta, backup.KindDaily, 1, "0123456789ab")
	writeTestDumpFile(t, backupDir, name, minimalDumpZip(t, 1))

	rec := postRestoreInspectStored(t, r, name)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", name, rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestRestoreInspectStoredRefusesAFormatNoStepCovers: a dump older than the
// oldest format the importer can upgrade is still listed and downloadable but
// never offered for restore, and this route refuses it through the same
// backup.ParseUpload check as an upload (ErrFormatVersionOlder).
func TestRestoreInspectStoredRefusesAFormatNoStepCovers(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	name := backup.BuildDumpName(time.Now(), tz.Jakarta, backup.KindDaily, 0, "0123456789ab")
	writeTestDumpFile(t, backupDir, name, minimalDumpZip(t, 0))

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
// against a fresh unauthenticated router (testRouter and
// authedRouterWithBackupDir both log a treasurer in).
func TestInspectStoredBackupRequiresASession(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil)

	rec := postRestoreInspectStored(t, r, "uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/restore/inspect-stored/... (no session) = %d, want %d (body: %s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestRestoreFromStoredSafetyNetBackupEndToEnd (#326): "restoring a
// safety-net backup works, so a restore can itself be undone." It runs the
// upload-restore path once (writing a pre-restore safety-net dump, per
// ADR-012), logs back in since that cleared every session, then restores
// again from that safety-net dump through the stored route.
func TestRestoreFromStoredSafetyNetBackupEndToEnd(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	backupDir := t.TempDir()
	raw := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), backupDir, nil)

	const email = "treasurer@example.org"
	reg := postRegister(t, raw, email, fixturePassword)
	if reg.Code != http.StatusCreated {
		t.Fatalf("fixture POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	r1 := withSessionCookie{Handler: raw, token: sessionCookie(reg)}
	setUpFund(t, r1)

	// First restore: an upload of the router's current state; the point here is
	// the pre-restore safety-net dump ADR-012 requires.
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

	// Every session (r1's included) is gone: log back in for the second restore,
	// of the safety-net dump.
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

	// The second restore's session is gone too; restoring a safety net is an
	// ordinary restore.
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund after the second restore = %d, want %d (every session must be cleared)", rec.Code, http.StatusUnauthorized)
	}
}

// decodeInspectToken pulls the token out of a successful inspect response.
func decodeInspectToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var inspected restoreInspectResponse
	if err := json.NewDecoder(rec.Body).Decode(&inspected); err != nil {
		t.Fatalf("decoding inspect response: %v", err)
	}
	if inspected.Token == "" {
		t.Fatal("inspect response carries no token")
	}
	return inspected.Token
}

// TestRestoreInspectStagesOnDiskAndConfirmRemovesIt (#344): the upload lands
// on disk under backupDir's restore-staging slot the moment inspect answers,
// and a successful confirm leaves nothing behind.
func TestRestoreInspectStagesOnDiskAndConfirmRemovesIt(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)
	zipBytes := downloadRealBackupZip(t, r)

	assertRestoreStagingFileExists(t, backupDir, false)

	inspectRec := postRestoreInspect(t, r, zipBytes)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect = %d, want %d (body: %s)", inspectRec.Code, http.StatusOK, inspectRec.Body.String())
	}
	token := decodeInspectToken(t, inspectRec)
	assertRestoreStagingFileExists(t, backupDir, true)

	confirmRec := postRestoreConfirm(t, r, token, fixturePassword)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm = %d, want %d (body: %s)", confirmRec.Code, http.StatusOK, confirmRec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, false)
}

// TestRestoreConfirmWrongPasswordKeepsStagedFile: a wrong password never
// reaches restoreStage.take() (confirmRestore checks the password first), so
// the stage and its file survive, and the treasurer can retype without
// re-uploading.
func TestRestoreConfirmWrongPasswordKeepsStagedFile(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)
	zipBytes := downloadRealBackupZip(t, r)

	inspectRec := postRestoreInspect(t, r, zipBytes)
	token := decodeInspectToken(t, inspectRec)
	assertRestoreStagingFileExists(t, backupDir, true)

	wrongRec := postRestoreConfirm(t, r, token, "definitely-the-wrong-password")
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/restore/confirm (wrong password) = %d, want %d (body: %s)", wrongRec.Code, http.StatusUnauthorized, wrongRec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, true)

	// The treasurer can retry with the same token, proving the stage survived
	// the wrong guess.
	confirmRec := postRestoreConfirm(t, r, token, fixturePassword)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm (retry) = %d, want %d (body: %s)", confirmRec.Code, http.StatusOK, confirmRec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, false)
}

// TestRestoreFailedInspectLeavesNoStagingFile: a malformed upload never gets
// past ParseUpload, so nothing is left staged.
func TestRestoreFailedInspectLeavesNoStagingFile(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)

	rec := postRestoreInspect(t, r, []byte("not a zip file at all"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/restore/inspect (garbage body) = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, false)
}

// TestRestoreSecondInspectReplacesStagedFile: one slot, so a second
// successful inspect wins. The first token names nothing (410 on confirm),
// and the file behind the second token is what restores.
func TestRestoreSecondInspectReplacesStagedFile(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)
	zipBytes := downloadRealBackupZip(t, r)

	firstRec := postRestoreInspect(t, r, zipBytes)
	firstToken := decodeInspectToken(t, firstRec)

	secondRec := postRestoreInspect(t, r, zipBytes)
	secondToken := decodeInspectToken(t, secondRec)

	if firstToken == secondToken {
		t.Fatal("two separate inspects produced the same token")
	}
	assertRestoreStagingFileExists(t, backupDir, true)

	staleConfirm := postRestoreConfirm(t, r, firstToken, fixturePassword)
	if staleConfirm.Code != http.StatusGone {
		t.Fatalf("POST /api/restore/confirm (superseded token) = %d, want %d (body: %s)", staleConfirm.Code, http.StatusGone, staleConfirm.Body.String())
	}
	// The second inspect's stage must be intact; a stale confirm must not have
	// disturbed it.
	assertRestoreStagingFileExists(t, backupDir, true)

	confirmRec := postRestoreConfirm(t, r, secondToken, fixturePassword)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm (current token) = %d, want %d (body: %s)", confirmRec.Code, http.StatusOK, confirmRec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, false)
}

// TestRestoreStagingFileNeverAppearsInBackupsList: a staging file's name
// never matches dumpNamePattern, so GET /api/backups (ListDumps only) never
// surfaces it, staged or not.
func TestRestoreStagingFileNeverAppearsInBackupsList(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setUpFund(t, r)
	zipBytes := downloadRealBackupZip(t, r)

	inspectRec := postRestoreInspect(t, r, zipBytes)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect = %d, want %d (body: %s)", inspectRec.Code, http.StatusOK, inspectRec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, true)

	rec := getBackups(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/backups = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var items []backupListItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("GET /api/backups = %v, want [] - the staged upload is not a dump", items)
	}
}

// TestRestoreInspectStoredThenOriginalDeletedStillConfirms (#344):
// inspecting a stored dump copies it into the staging slot, so confirm, which
// reads only that copy, succeeds even if the original is deleted (by an
// operator, or retention pruning) between inspect and confirm.
func TestRestoreInspectStoredThenOriginalDeletedStillConfirms(t *testing.T) {
	t.Parallel()
	r, backupDir := authedRouterWithBackupDir(t)
	setup := setUpFund(t, r)
	zipBytes := downloadRealBackupZip(t, r)

	name := backup.BuildDumpName(time.Now(), tz.Jakarta, backup.KindDaily, backup.FormatVersion, "0123456789ab")
	dumpPath := filepath.Join(backupDir, name)
	writeTestDumpFile(t, backupDir, name, zipBytes)

	inspectRec := postRestoreInspectStored(t, r, name)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/inspect-stored/%s = %d, want %d (body: %s)", name, inspectRec.Code, http.StatusOK, inspectRec.Body.String())
	}
	var inspected restoreInspectResponse
	if err := json.NewDecoder(inspectRec.Body).Decode(&inspected); err != nil {
		t.Fatalf("decoding inspect response: %v", err)
	}
	if len(inspected.Preview.Funds) != 1 || inspected.Preview.Funds[0].FundID != setup.Fund.ID {
		t.Fatalf("preview.Funds = %v, want exactly the one live fund %d", inspected.Preview.Funds, setup.Fund.ID)
	}
	assertRestoreStagingFileExists(t, backupDir, true)

	if err := os.Remove(dumpPath); err != nil {
		t.Fatalf("removing the original stored dump: %v", err)
	}

	confirmRec := postRestoreConfirm(t, r, inspected.Token, fixturePassword)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore/confirm (original dump deleted) = %d, want %d (body: %s)", confirmRec.Code, http.StatusOK, confirmRec.Body.String())
	}
	assertRestoreStagingFileExists(t, backupDir, false)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/fund after restore = %d, want %d (every session must be cleared)", rec.Code, http.StatusUnauthorized)
	}
}

// TestRestoreStageReplacingEarlierStageDeletesEarlierFile is a white-box
// proof of restoreStage.stage's concurrency fix (#344): staging a second
// upload removes exactly the file the first owned, under the mutex that
// installs the second, never a shared name either could race over.
func TestRestoreStageReplacingEarlierStageDeletesEarlierFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newRestoreStage()

	path1 := createRestoreStagingTempOrFatal(t, dir)
	if _, err := s.stage(backup.ParsedUpload{StagingPath: path1}); err != nil {
		t.Fatalf("stage() (first) = %v, want no error", err)
	}

	path2 := createRestoreStagingTempOrFatal(t, dir)
	if _, err := s.stage(backup.ParsedUpload{StagingPath: path2}); err != nil {
		t.Fatalf("stage() (second) = %v, want no error", err)
	}

	if _, err := os.Stat(path1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat(first staged file) = %v, want os.ErrNotExist - stage() must remove the file it superseded", err)
	}
	if _, err := os.Stat(path2); err != nil {
		t.Errorf("stat(second staged file) = %v, want it to survive", err)
	}
}

// TestRestoreConfirmRemovalLeavesALaterInspectsFileAlone: once take() hands a
// file to a (simulated) confirm, restoreStage no longer references it, so a
// later inspect's stage(), landing before that confirm removes its file,
// neither touches it nor is affected. No goroutines: take then stage proves
// take() clears its bookkeeping before stage() runs.
func TestRestoreConfirmRemovalLeavesALaterInspectsFileAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newRestoreStage()

	path1 := createRestoreStagingTempOrFatal(t, dir)
	token1, err := s.stage(backup.ParsedUpload{StagingPath: path1})
	if err != nil {
		t.Fatalf("stage() (first) = %v, want no error", err)
	}

	taken, ok := s.take(token1)
	if !ok || taken.StagingPath != path1 {
		t.Fatalf("take(%q) = (%+v, %v), want the first stage's own file", token1, taken, ok)
	}

	// A second inspect lands next, before this test's stand-in for confirmRestore
	// removes taken.StagingPath.
	path2 := createRestoreStagingTempOrFatal(t, dir)
	if _, err := s.stage(backup.ParsedUpload{StagingPath: path2}); err != nil {
		t.Fatalf("stage() (second, after take) = %v, want no error", err)
	}

	if _, err := os.Stat(path1); err != nil {
		t.Errorf("stat(first file) after a later stage() = %v, want it to survive - confirm, not stage, owns it now", err)
	}
	if _, err := os.Stat(path2); err != nil {
		t.Errorf("stat(second staged file) = %v, want it present", err)
	}

	// confirmRestore's cleanup, stood in for directly: removing the file take()
	// handed it must not reach for anything restoreStage holds.
	if err := os.Remove(taken.StagingPath); err != nil {
		t.Fatalf("removing the taken file: %v", err)
	}
	if _, err := os.Stat(path2); err != nil {
		t.Errorf("stat(second staged file) after confirm's own cleanup = %v, want it to survive", err)
	}
}

// createRestoreStagingTempOrFatal makes a real file at a unique staging path
// via backup.CreateRestoreStagingTemp, closed immediately: these white-box
// tests only need the path's presence or absence.
func createRestoreStagingTempOrFatal(t *testing.T, dir string) string {
	t.Helper()
	f, err := backup.CreateRestoreStagingTemp(dir)
	if err != nil {
		t.Fatalf("CreateRestoreStagingTemp() = %v, want no error", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		t.Fatalf("closing staging file: %v", err)
	}
	return path
}
