package http

// Restore from an uploaded backup (ADR-012) or one of the server's stored
// dumps: three routes over internal/backup's ParseUpload/BuildPreview/Restore
// trio. POST /api/restore/inspect decodes an upload; POST
// /api/restore/inspect-stored/{name} reads a dump already in backupDir. Both
// stream their input to disk and share one tail (stageAndInspect) that
// validates it, stages it, and builds the preview (date, funds, total, a
// per-fund kept/removed/added line) before the treasurer types anything. POST
// /api/restore/confirm takes her password and the token back, and is the only
// route that changes anything.
//
// Two steps, with a server-side stash, so a treasurer on a phone does not
// re-upload a backup of up to 256 MB just to type her password.
// restoreStage is that stash: a random token naming one slot (a
// single-treasurer app has one session that could be mid-restore, ADR-030,
// so a map of pending uploads gains nothing).
//
// The stash holds only the small decoded Document plus a path: the zip and
// every receipt a restore might extract (up to 1 GB decompressed) stay on
// disk in a uniquely-named file (backup.CreateRestoreStagingTemp) that
// dumps.go can never mistake for a dump, since it never matches
// dumpNamePattern.
//
// Every inspect gets its own file, never a shared name: two concurrent
// inspects renaming onto one slot could leave a token naming the wrong bytes.
// restoreStage owns a file once an inspect stages it, replacing the previous
// one under its mutex, so "one slot" is restoreStage's bookkeeping, not a
// filesystem path two requests could collide on. A restart drops the
// in-memory half (the file is swept by RemoveStaleTemps at boot), which just
// means the upload step runs again. An abandoned stage - inspected, never
// confirmed, never replaced - is removed by its own timer when it expires
// (#531), so up to 256 MB does not sit on disk until the next inspect.
import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
)

// restoreStageTTL is how long an inspected upload stays valid for
// confirmation: long enough to read the dialog and type a password, short
// enough that an abandoned upload does not hold its disk for long.
const restoreStageTTL = 10 * time.Minute

// restoreStage is the one-slot stash described at the top of this file: one
// stage at a time, enforced by this type's mutex, never by a shared path.
// Safe for concurrent use; in practice one session reaches these routes
// (ADR-030), but that treasurer can open the confirm dialog in two tabs.
type restoreStage struct {
	mu      sync.Mutex
	token   string
	parsed  backup.ParsedUpload
	expires time.Time
	// sweep removes the current stage's file at expiry; replaced with the stage.
	sweep *time.Timer
	ttl   time.Duration
}

func newRestoreStage() *restoreStage {
	return &restoreStage{ttl: restoreStageTTL}
}

// stage installs parsed as the current stage under the mutex and, in the same
// critical section, removes the file the previous stage owned. That is what
// makes "one slot" true without a shared filesystem name: of two concurrent
// inspects, the one taking the lock second wins the token and deletes the
// loser's file, so the current token never names one file while a different
// file sits at "the" staging path.
func (s *restoreStage) stage(parsed backup.ParsedUpload) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		_ = os.Remove(s.parsed.StagingPath)
	}
	s.token = token
	s.parsed = parsed
	s.expires = time.Now().Add(s.ttl)
	if s.sweep != nil {
		s.sweep.Stop()
	}
	s.sweep = time.AfterFunc(s.ttl, func() { s.expire(token) })
	return token, nil
}

// expire is the sweep timer's callback: it removes token's stage and its file,
// unless that stage was already taken or replaced, which the token check
// settles under the mutex whatever order the timer and a request arrive in.
func (s *restoreStage) expire(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != token {
		return
	}
	_ = os.Remove(s.parsed.StagingPath)
	s.token, s.parsed = "", backup.ParsedUpload{}
}

// take consumes the staged upload if token matches and it has not expired.
// One-shot, like a nonce: a second confirm with the same token (a retry, a
// double-tap) finds nothing rather than restoring twice. On a match it clears
// the stage and hands the caller the path and the parsed Document; the caller
// now owns that file, and s.parsed is zeroed so nothing restoreStage does
// later can reference or remove it. confirmRestore removes it once
// backup.Restore has read it, success or failure. An expired stage (the
// dialog sat past restoreStageTTL) is noticed here, and this access also
// removes its staging file.
func (s *restoreStage) take(token string) (backup.ParsedUpload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if token == "" || s.token == "" {
		return backup.ParsedUpload{}, false
	}
	if time.Now().After(s.expires) {
		_ = os.Remove(s.parsed.StagingPath)
		s.token, s.parsed = "", backup.ParsedUpload{}
		return backup.ParsedUpload{}, false
	}
	if s.token != token {
		return backup.ParsedUpload{}, false
	}
	parsed := s.parsed
	s.token, s.parsed = "", backup.ParsedUpload{}
	s.sweep.Stop()
	return parsed, true
}

// restoreInspectResponse is POST /api/restore/inspect's body: the token the
// confirm step must send back, and the preview to show meanwhile.
type restoreInspectResponse struct {
	Token   string         `json:"token"`
	Preview backup.Preview `json:"preview"`
}

// inspectRestoreUpload is POST /api/restore/inspect: a streamed multipart
// upload, capped at 256 MB of compressed body via http.MaxBytesReader, then
// validated by backup.ParseUpload (unzipped-bytes and entry-count caps,
// format version, per-image decode). Nothing is written but the staging file
// stageAndInspect commits to after ParseUpload proves a valid backup; a
// failed inspect leaves no trace.
//
// r.MultipartReader() (streamed), not r.ParseMultipartForm: that spills past
// maxMemory to os.TempDir, which the distroless image does not guarantee,
// and holds the whole upload in memory below that point. Streaming the
// "file" part into the staging file means this handler never holds more than
// one io.Copy buffer.
func (a *api) inspectRestoreUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxUploadBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request is not a valid file upload.")
		return
	}

	var part *multipart.Part
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeRestoreStreamError(w, err)
			return
		}
		if p.FormName() == "file" {
			part = p
			break
		}
		_ = p.Close()
	}
	if part == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "A backup file is required.")
		return
	}
	defer func() { _ = part.Close() }()

	a.stageAndInspect(w, r, part)
}

// inspectStoredBackup is POST /api/restore/inspect-stored/{name}: the same
// inspect step as inspectRestoreUpload, with the zip coming from a dump in
// backupDir instead of an upload. name goes through resolveStoredBackupPath
// (backup.go), the validation GET /api/backups/{name} uses: the client names
// a dump by the string listBackups gave it, never a path, and a valid name
// not on disk (unknown, or pruned since the list was fetched) is a 404.
//
// Past that point it is backup.ParseUpload and the same errors and
// writeRestoreParseError mapping. A dump is judged exactly as an uploaded zip of
// the same format is, so ParseUpload is the one place judging format version.
//
// The dump is streamed into a staging file rather than read into memory. This
// also fixes a correctness gap: WritePreRestoreDump's ApplyRetention call,
// inside Restore, could prune the very dump being restored if confirm read
// the original path. Confirm reads only the staged copy, so the original can
// be pruned or deleted after this request answers.
func (a *api) inspectStoredBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	full, ok := a.resolveStoredBackupPath(name)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "That is not a valid backup name.")
		return
	}

	//nolint:gosec // full is validated by resolveStoredBackupPath as downloadStoredBackup's read is
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
			return
		}
		a.log(r).Error("opening stored backup for restore", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	defer func() { _ = f.Close() }()

	a.stageAndInspect(w, r, f)
}

// stageAndInspect is the shared tail of inspectRestoreUpload and
// inspectStoredBackup: stream src into a fresh, uniquely-named staging file,
// validate it with backup.ParseUpload, then, only on success, build the
// preview against the live database and hand the parsed result (staging path
// included) to restoreStage.stage. src is never read into a []byte; it moves
// through io.Copy's bounded buffer once.
//
// Every call gets its own file from backup.CreateRestoreStagingTemp, so a
// failed validation just removes that file, and success hands ownership to
// restoreStage.stage, the only place a previous stage's file is removed.
func (a *api) stageAndInspect(w http.ResponseWriter, r *http.Request, src io.Reader) {
	tmp, err := backup.CreateRestoreStagingTemp(a.backupDir)
	if err != nil {
		a.log(r).Error("creating restore staging temp file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	tmpPath := tmp.Name()
	staged := false
	defer func() {
		if !staged {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		writeRestoreStreamError(w, err)
		return
	}
	if err := tmp.Close(); err != nil {
		a.log(r).Error("closing restore staging temp file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	parsed, err := backup.ParseUpload(tmpPath)
	if err != nil {
		writeRestoreParseError(w, a.log(r), err)
		return
	}

	preview, err := backup.BuildPreview(r.Context(), a.queries, parsed.Document)
	if err != nil {
		a.log(r).Error("building restore preview", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	token, err := a.restoreStage.stage(parsed)
	if err != nil {
		a.log(r).Error("staging restore upload", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	staged = true

	a.securityEvent(r, slog.LevelInfo, "restore_inspected")
	writeJSON(w, http.StatusOK, restoreInspectResponse{Token: token, Preview: preview})
}

// writeRestoreStreamError maps a streaming read failure (finding the "file"
// part, or copying it) onto the two codes inspectRestoreUpload always had:
// restore_file_too_large when http.MaxBytesReader stopped the read,
// invalid_argument otherwise.
func writeRestoreStreamError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		// restore_file_too_large, not receipts.go's payload_too_large: that code's
		// copy ("Ukuran foto lebih dari 10 MB") names a photo and a 10 MB limit, and
		// copy.common.errors is one shared map keyed by code, so a backup upload
		// needs its own.
		writeAPIError(w, http.StatusRequestEntityTooLarge, "restore_file_too_large", "The backup file is larger than the 256 MB limit.")
		return
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The backup file could not be read.")
}

// writeRestoreParseError maps ParseUpload's sentinel errors onto distinct
// wire codes; a newer or older format_version is refused with distinct copy.
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
	case errors.Is(err, backup.ErrDocumentTooLarge):
		// Its own sentinel and message (#531), the same code: to her it is the
		// same answer, a file larger than any backup Uruni makes.
		writeAPIError(w, http.StatusRequestEntityTooLarge, "restore_file_too_large",
			"This backup's data file is larger than 64 MB, more than a Uruni backup ever holds.")
	case errors.Is(err, backup.ErrTooManyEntries), errors.Is(err, backup.ErrUnzippedTooLarge), errors.Is(err, backup.ErrUploadTooLarge):
		writeAPIError(w, http.StatusRequestEntityTooLarge, "restore_file_too_large",
			"This file is larger than a Uruni backup ever should be.")
	case errors.Is(err, backup.ErrBadReceiptImage):
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_bad_image",
			"One of the backup's receipt photos could not be read.")
	case errors.Is(err, backup.ErrTotalsMismatch):
		// The file disagrees with itself (#496): its totals block does not match its
		// rows. A separate code from restore_totals_mismatch, which is the
		// rolled-back-restore message.
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_file_inconsistent",
			"The backup's totals do not match its own entries, so it cannot be restored.")
	case errors.Is(err, backup.ErrMalformedBackup):
		writeAPIError(w, http.StatusUnprocessableEntity, "restore_invalid_file",
			"That file is not a valid Uruni backup.")
	default:
		logger.Error("parsing restore upload", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}
}

// restoreConfirmRequest is POST /api/restore/confirm's body: the token
// inspect handed back, and the treasurer's current password (ADR-012: this
// checks the CURRENT password, never anything decoded out of the file).
type restoreConfirmRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// confirmRestore is POST /api/restore/confirm: verify the current password,
// take the staged upload, and restore. It is the one route that can discard
// the live database's history in favour of a file's.
//
// The password check is rate-limited through the loginLimiter POST /api/login
// uses, under its own key prefix, so this route cannot be used to brute-force
// the password just because it sits behind a different path.
func (a *api) confirmRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	users, err := a.queries.ListUsers(r.Context())
	if err != nil {
		a.log(r).Error("listing the live user for restore confirm", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	if len(users) != 1 {
		// ADR-030 decision 2: exactly one account exists for the life of a v1
		// instance. Zero here would mean confirming a restore before register/setup
		// ran, which sessionRequired makes unreachable.
		a.log(r).Error("restore confirm: unexpected user count", "count", len(users))
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	// Reserved before the password is verified, as in login.go (#495): the
	// attempt counts while in flight. Kept for a wrong password, handed back for
	// anything that was not a guess.
	ip := "restore-ip:" + a.clientNetwork(r)
	reservation, ok := a.loginLimiter.reserve(ip)
	if !ok {
		a.securityEvent(r, slog.LevelWarn, "restore_confirm_refused", "reason", "locked_out")
		writeAPIError(w, http.StatusTooManyRequests, "too_many_requests", "Too many attempts. Try again later.")
		return
	}
	if _, err := a.auth.Authenticate(r.Context(), users[0].Email, req.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			a.securityEvent(r, slog.LevelWarn, "restore_confirm_refused", "reason", "wrong_password")
		} else {
			reservation.release()
		}
		mapAuthError(w, a.log(r), err)
		return
	}
	a.loginLimiter.reset(ip)

	parsed, ok := a.restoreStage.take(req.Token)
	if !ok {
		writeAPIError(w, http.StatusGone, "restore_upload_expired", "This upload has expired. Upload the backup again.")
		return
	}

	// take() handed this handler sole ownership of parsed.StagingPath, so it
	// must remove it once backup.Restore has read it, on success and failure
	// alike: nothing could still confirm with this token.
	restoreErr := backup.Restore(r.Context(), a.sqlDB, a.uploadsDir, a.backupDir, parsed, time.Now())
	if err := os.Remove(parsed.StagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.log(r).Error("removing restore staging file after confirm", "error", err)
		// Not a reason to report the restore as failed, like the session-destroy
		// failure below.
	}
	if restoreErr != nil {
		a.securityEvent(r, slog.LevelWarn, "restore_confirm_failed")
		writeRestoreCommitError(w, a.log(r), restoreErr)
		return
	}

	// backup.Restore's DeleteAllSessions (inside its transaction) clears every
	// session row, including this request's. The store no longer re-creates a
	// deleted row on commit (sessionStore.CommitCtx), but scs's LoadAndSave
	// would still hand this browser a refreshed cookie unless the session is
	// Destroyed (see logout.go): this clears it, so the route that logs everyone
	// out leaves no stale cookie behind.
	if err := a.sessionManager.Destroy(r.Context()); err != nil {
		a.log(r).Error("destroying the confirming session after restore", "error", err)
		// The restore already committed; this is a logout-hygiene failure, not a
		// reason to report the restore as failed.
	}

	a.securityEvent(r, slog.LevelWarn, "restore_confirmed")
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
