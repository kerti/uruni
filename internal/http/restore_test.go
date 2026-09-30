package http

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/backup"
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
