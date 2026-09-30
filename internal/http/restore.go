package http

// Restore from an uploaded backup (M6.39, #325, ADR-012), or from one of the
// server's own stored dumps (M6.40, #326): three routes over internal/backup's
// ParseUpload/BuildPreview/Restore trio. POST /api/restore/inspect decodes an
// upload; POST /api/restore/inspect-stored/{name} reads a dump already
// sitting in backupDir instead - both validate their input, then share one
// tail (stageAndRespondWithPreview) that builds the same preview - date,
// funds, total, a per-fund kept/removed/added line - well before the
// treasurer has typed anything. POST /api/restore/confirm takes her
// password back and the token whichever inspect step handed her, and is the
// only route that actually changes anything - it does not care which
// inspect route staged what it is about to confirm.
//
// Two steps rather than one upload-and-restore call, and a server-side
// stash rather than asking the client to hold the parsed upload and send
// it twice: a treasurer on a phone should not have to re-upload a backup
// that can be up to 256 MB just because she is now typing her password -
// and the already-decoded Document, not the raw zip, is what the confirm
// step needs, so re-sending the zip a second time would mean parsing it
// twice for no benefit. restoreStage (below) is that stash: in-process
// memory, keyed by a fresh random token, one slot - this is a single-
// treasurer app with one session that could ever be mid-restore at a time
// (ADR-030), so there is nothing to gain from a map of many pending
// uploads, and every candidate that would (a database row, a temp file on
// disk) is a second thing to expire and clean up for state that only ever
// needs to survive one dialog staying open. A restart drops the stash,
// which just means the upload step runs again - not a fact this package
// tries to hide from the treasurer with a retry loop.
import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/backup"
)

// restoreStageTTL is how long an inspected upload stays valid for
// confirmation - long enough to read the confirm dialog and type a
// password once, short enough that an abandoned upload does not sit in
// memory for the life of the process.
const restoreStageTTL = 10 * time.Minute

// restoreStage is the one-slot stash restore.go's own doc comment
// explains. Safe for concurrent use, though in practice exactly one
// session is ever gated to reach these routes at all (ADR-030).
type restoreStage struct {
	mu      sync.Mutex
	token   string
	parsed  backup.ParsedUpload
	expires time.Time
}

func newRestoreStage() *restoreStage {
	return &restoreStage{}
}

// stage replaces whatever was staged before (a second inspect always wins -
// there is only one confirm dialog on screen at a time) and returns the
// fresh token that names it.
func (s *restoreStage) stage(parsed backup.ParsedUpload) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
	s.parsed = parsed
	s.expires = time.Now().Add(restoreStageTTL)
	return token, nil
}

// take consumes the staged upload if token matches and it has not expired -
// one-shot, like a nonce: a second confirm with the same token (a retried
// request, a double-tap) finds nothing staged rather than restoring twice.
func (s *restoreStage) take(token string) (backup.ParsedUpload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if token == "" || s.token == "" || s.token != token || time.Now().After(s.expires) {
		return backup.ParsedUpload{}, false
	}
	parsed := s.parsed
	s.token, s.parsed = "", backup.ParsedUpload{}
	return parsed, true
}

// restoreInspectResponse is POST /api/restore/inspect's whole body: the
// token the confirm step must send back, and the preview to show while it
// does.
type restoreInspectResponse struct {
	Token   string         `json:"token"`
	Preview backup.Preview `json:"preview"`
}

// inspectRestoreUpload is POST /api/restore/inspect: multipart upload,
// capped and validated exactly as issue #325's acceptance criteria list -
// 256 MB of compressed body via http.MaxBytesReader, then
// backup.ParseUpload's own unzipped-bytes and entry-count caps, format
// version check and per-image decode check. Nothing is written anywhere;
// a failed inspect leaves no trace.
func (a *api) inspectRestoreUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxUploadBytes)

	if err := r.ParseMultipartForm(backup.MaxUploadBytes); err != nil { //nolint:gosec // not unbounded - r.Body is already wrapped in http.MaxBytesReader above
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// restore_file_too_large, not receipts.go's payload_too_large:
			// that code's own copy ("Ukuran foto lebih dari 10 MB") names a
			// photo and a 10 MB limit - wrong on both counts here, and
			// copy.common.errors is one shared map keyed by code across the
			// whole app, so a backup upload needs its own.
			writeAPIError(w, http.StatusRequestEntityTooLarge, "restore_file_too_large", "The backup file is larger than the 256 MB limit.")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request is not a valid file upload.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "A backup file is required.")
		return
	}
	defer func() { _ = file.Close() }()

	zipBytes, err := io.ReadAll(file)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The backup file could not be read.")
		return
	}

	parsed, err := backup.ParseUpload(zipBytes)
	if err != nil {
		writeRestoreParseError(w, a.logger, err)
		return
	}

	a.stageAndRespondWithPreview(w, r, parsed)
}

// inspectStoredBackup is POST /api/restore/inspect-stored/{name}: the same
// inspect step as inspectRestoreUpload above, except the zip comes from a
// dump already sitting in backupDir rather than a multipart upload - the
// "run #325's restore path end to end... with the file read from the
// backup directory instead of uploaded" issue #326 asks for. name is
// resolved through resolveStoredBackupPath (backup.go), the exact same
// server-side-identifier validation GET /api/backups/{name} already uses:
// the client names a dump by the string listBackups gave it, never a path,
// and a name that is syntactically valid but not actually on disk (unknown,
// or pruned by retention since the list was fetched) is a plain 404.
//
// Everything past that point is backup.ParseUpload itself - the same
// function, the same sentinel errors, the same
// writeRestoreParseError mapping inspectRestoreUpload's own malformed-upload
// case uses. An older-format dump refuses here exactly as a hand-uploaded
// older-format zip would (ErrFormatVersionOlder), which is what keeps "only
// current-format backups offer restore" true without this handler having to
// duplicate that check against backupListItem's own IsCurrentFormat flag -
// there is exactly one place a format version is ever judged, and this
// route goes through it like every other path into ParseUpload.
func (a *api) inspectStoredBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	full, ok := a.resolveStoredBackupPath(name)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "That is not a valid backup name.")
		return
	}

	zipBytes, err := os.ReadFile(full) //nolint:gosec // full is validated by resolveStoredBackupPath exactly as downloadStoredBackup's own read is
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
			return
		}
		a.logger.Error("reading stored backup for restore", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	parsed, err := backup.ParseUpload(zipBytes)
	if err != nil {
		writeRestoreParseError(w, a.logger, err)
		return
	}

	a.stageAndRespondWithPreview(w, r, parsed)
}

// stageAndRespondWithPreview is inspectRestoreUpload's and
// inspectStoredBackup's shared tail: build the preview against the live
// database, stash the parsed document under a fresh token, answer with
// both. Neither caller writes anything before this point, and this
// function itself writes nothing to the database either - only
// restoreStage's in-process map, the same one-slot stash either inspect
// route replaces wholesale (a second inspect, from either route, always
// wins - there is only one confirm dialog on screen at a time).
func (a *api) stageAndRespondWithPreview(w http.ResponseWriter, r *http.Request, parsed backup.ParsedUpload) {
	preview, err := backup.BuildPreview(r.Context(), a.queries, parsed.Document)
	if err != nil {
		a.logger.Error("building restore preview", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	token, err := a.restoreStage.stage(parsed)
	if err != nil {
		a.logger.Error("staging restore upload", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	writeJSON(w, http.StatusOK, restoreInspectResponse{Token: token, Preview: preview})
}

// writeRestoreParseError maps ParseUpload's own sentinel errors onto
// distinct wire codes - issue #325's own acceptance criteria: "newer or
// older format_version refused with distinct copy."
func writeRestoreParseError(w http.ResponseWriter, logger interface {
	Error(msg string, args ...any)
}, err error) {
	switch {
	case errors.Is(err, backup.ErrFormatVersionNewer):
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_format_too_new",
			"This backup was made by a newer version of Uruni. Update the server, then try again.")
	case errors.Is(err, backup.ErrFormatVersionOlder):
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_format_too_old",
			"This backup was made by an older version of Uruni that this server can no longer read.")
	case errors.Is(err, backup.ErrTooManyEntries), errors.Is(err, backup.ErrUnzippedTooLarge):
		writeAPIError(w, http.StatusRequestEntityTooLarge, "restore_file_too_large",
			"This file is larger than a Uruni backup ever should be.")
	case errors.Is(err, backup.ErrBadReceiptImage):
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_bad_image",
			"One of the backup's receipt photos could not be read.")
	case errors.Is(err, backup.ErrMalformedBackup):
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_invalid_file",
			"That file is not a valid Uruni backup.")
	default:
		logger.Error("parsing restore upload", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}
}

// restoreConfirmRequest is POST /api/restore/confirm's body: the token
// inspect handed back, and the treasurer's current password (ADR-012's
// login ruling: this checks the CURRENT password, never anything decoded
// out of the file).
type restoreConfirmRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// confirmRestore is POST /api/restore/confirm: verify the current password,
// take the staged upload, and restore - the one route in this whole
// surface that can discard the live database's own history in favour of a
// file's.
//
// The password check is rate-limited through the same loginLimiter
// POST /api/login already uses, under its own key prefix, so this route
// cannot be used to brute-force the treasurer's password just because it
// sits behind a different path than login.
func (a *api) confirmRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ip := "restore-ip:" + clientIP(r)
	if a.loginLimiter.blocked(ip) {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_requests", "Too many attempts. Try again later.")
		return
	}

	users, err := a.queries.ListUsers(r.Context())
	if err != nil {
		a.logger.Error("listing the live user for restore confirm", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	if len(users) != 1 {
		// ADR-030 decision 2: exactly one account exists for the life of a
		// v1 instance. A count of zero here would mean confirming a
		// restore before the one-shot register/setup flow ever ran, which
		// sessionRequired already makes unreachable.
		a.logger.Error("restore confirm: unexpected user count", "count", len(users))
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	if _, err := a.auth.Authenticate(r.Context(), users[0].Email, req.Password); err != nil {
		a.loginLimiter.recordFailure(ip)
		mapAuthError(w, a.logger, err)
		return
	}
	a.loginLimiter.reset(ip)

	parsed, ok := a.restoreStage.take(req.Token)
	if !ok {
		writeAPIError(w, http.StatusGone, "restore_upload_expired", "This upload has expired. Upload the backup again.")
		return
	}

	if err := backup.Restore(r.Context(), a.sqlDB, a.queries, a.ledger, a.uploadsDir, a.backupDir, parsed, time.Now()); err != nil {
		writeRestoreCommitError(w, a.logger, err)
		return
	}

	// backup.Restore's own DeleteAllSessions (inside its transaction)
	// clears every session row in the database, including this request's
	// own - but scs's LoadAndSave middleware (api.go) still holds this
	// request's session in memory and writes it straight back to the store
	// after this handler returns, unless it is told the session is
	// Destroyed first (logout.go's own comment explains the same
	// mechanism). Without this call, the one session that just confirmed
	// the restore would resurrect itself the instant this handler returns,
	// which is exactly backwards for the one route that is supposed to log
	// everyone out.
	if err := a.sessionManager.Destroy(r.Context()); err != nil {
		a.logger.Error("destroying the confirming session after restore", "error", err)
		// The restore itself already committed - this is a logout-hygiene
		// failure, not a reason to report the restore as having failed.
	}

	writeJSON(w, http.StatusOK, struct{}{})
}

func writeRestoreCommitError(w http.ResponseWriter, logger interface {
	Error(msg string, args ...any)
}, err error) {
	if errors.Is(err, backup.ErrTotalsMismatch) {
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_totals_mismatch",
			"The restored totals did not match the file, so nothing was changed.")
		return
	}
	logger.Error("restoring backup", "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "The restore could not be completed - nothing was changed.")
}
