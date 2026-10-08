package http

// Restore from an uploaded backup (M6.39, #325, ADR-012), or from one of the
// server's own stored dumps (M6.40, #326): three routes over internal/backup's
// ParseUpload/BuildPreview/Restore trio. POST /api/restore/inspect decodes an
// upload; POST /api/restore/inspect-stored/{name} reads a dump already
// sitting in backupDir instead - both stream their input onto disk and
// share one tail (stageAndInspect) that validates it, stages it, and builds
// the same preview - date, funds, total, a per-fund kept/removed/added
// line - well before the treasurer has typed anything. POST /api/restore/confirm takes her
// password back and the token whichever inspect step handed her, and is the
// only route that actually changes anything - it does not care which
// inspect route staged what it is about to confirm.
//
// Two steps rather than one upload-and-restore call, and a server-side
// stash rather than asking the client to hold the parsed upload and send
// it twice: a treasurer on a phone should not have to re-upload a backup
// that can be up to 256 MB just because she is now typing her password -
// and the already-decoded Document, not the raw zip, is what the confirm
// step needs most of, so re-sending the zip a second time would mean
// parsing it twice for no benefit. restoreStage (below) is that stash: a
// fresh random token naming one slot - this is a single-treasurer app with
// one session that could ever be mid-restore at a time (ADR-030), so there
// is nothing to gain from a map of many pending uploads.
//
// The stash itself holds only the small, decoded Document plus a path
// (issue #344): the zip's own bytes, and every receipt a restore might
// extract from it (up to 1 GB decompressed), are staged on disk, under its
// own uniquely-named file (backup.CreateRestoreStagingTemp), that
// dumps.go's own ListDumps/ValidDumpName/ApplyRetention/ChangeHash can
// never mistake for a dump, since it never matches dumpNamePattern.
//
// Every inspect gets its own file, never a shared fixed name (issue #344's
// own follow-up): two concurrent inspects racing to rename onto one shared
// slot is exactly how a token ends up naming the wrong bytes. restoreStage
// itself is what owns a file once an inspect successfully stages it -
// replacing the previous one, if any, under its own mutex - so "one slot"
// is a property of restoreStage's bookkeeping, never of a filesystem path
// two requests could otherwise collide on. A restart drops the in-memory
// half of the stash (and the file it named, along with it, is swept by
// RemoveStaleTemps at the next boot, same as any other interrupted write) -
// which just means the upload step runs again, not a fact this package
// tries to hide from the treasurer with a retry loop.
import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
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
// explains - "one slot" meaning one *stage* at a time, enforced entirely by
// this type's own mutex, never by two requests sharing a filesystem path.
// Safe for concurrent use, though in practice exactly one session is ever
// gated to reach these routes at all (ADR-030) - the mutex still matters,
// because that one treasurer can still open the confirm dialog in two tabs.
type restoreStage struct {
	mu      sync.Mutex
	token   string
	parsed  backup.ParsedUpload
	expires time.Time
}

func newRestoreStage() *restoreStage {
	return &restoreStage{}
}

// stage installs parsed as the current stage, under the mutex - and, still
// under that same mutex, removes whatever file the *previous* stage (if
// any) owned. Doing both atomically, in one critical section, is what
// makes "one slot" true without a shared filesystem name to race over: two
// concurrent inspects each hand stage their own, uniquely-named file
// (backup.CreateRestoreStagingTemp); whichever call takes the lock second
// is the one that both wins the token and deletes the loser's file, in the
// same breath, so there is never a moment where the current token names
// one file while a different file sits at "the" staging path.
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
	s.expires = time.Now().Add(restoreStageTTL)
	return token, nil
}

// take consumes the staged upload if token matches and it has not expired -
// one-shot, like a nonce: a second confirm with the same token (a retried
// request, a double-tap) finds nothing staged rather than restoring twice.
// On a match, it clears the stage and hands the caller the path along with
// the parsed Document - the caller now owns that file outright; nothing
// restoreStage does afterward (a later stage(), a later take()) can ever
// reference or remove it again, since s.parsed is already zeroed before
// this function returns. confirmRestore removes it only once backup.Restore
// has read it, success or failure alike. An *expired* stage is different:
// a treasurer who let the confirm dialog sit past restoreStageTTL is never
// coming back to it, so this access is what finally notices, and also what
// cleans the staging file up itself.
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
	return parsed, true
}

// restoreInspectResponse is POST /api/restore/inspect's whole body: the
// token the confirm step must send back, and the preview to show while it
// does.
type restoreInspectResponse struct {
	Token   string         `json:"token"`
	Preview backup.Preview `json:"preview"`
}

// inspectRestoreUpload is POST /api/restore/inspect: a streamed multipart
// upload, capped and validated exactly as issue #325's acceptance criteria
// list - 256 MB of compressed body via http.MaxBytesReader, then
// backup.ParseUpload's own unzipped-bytes and entry-count caps, format
// version check and per-image decode check. Nothing is written anywhere
// but the one staging slot stageAndInspect commits to only once ParseUpload
// has already proved the file a valid backup; a failed inspect leaves no
// trace (stageAndInspect's own doc comment).
//
// r.MultipartReader() (streamed), not r.ParseMultipartForm (issue #344):
// that call spills anything over its own maxMemory to a temp file under
// os.TempDir, which the distroless production image is not guaranteed to
// have, and it would hold the whole upload in memory below that spill
// point regardless - exactly the bug this issue is about. Streaming the
// "file" part straight into the staging temp file (stageAndInspect) means
// this handler itself never holds more than one io.Copy buffer's worth of
// the upload at a time.
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
//
// The dump is streamed into its own staging file rather than read into
// memory (issue #344) - this also fixes a correctness gap, not only a
// memory one: WritePreRestoreDump's own ApplyRetention call, inside
// Restore, prunes older pre-restore dumps and could otherwise prune the
// very dump this request is restoring, if confirm read from the original
// path. Since confirm only ever reads the staged copy (stageAndInspect
// stages it before this handler returns), the original file at full can be
// pruned, or simply deleted by an operator, at any point after this
// request answers, with no effect on the restore already in flight.
func (a *api) inspectStoredBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	full, ok := a.resolveStoredBackupPath(name)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "That is not a valid backup name.")
		return
	}

	//nolint:gosec // full is validated by resolveStoredBackupPath exactly as downloadStoredBackup's own read is
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
			return
		}
		a.logger.Error("opening stored backup for restore", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	defer func() { _ = f.Close() }()

	a.stageAndInspect(w, r, f)
}

// stageAndInspect is inspectRestoreUpload's and inspectStoredBackup's
// shared tail (issue #344): stream src into a fresh, uniquely-named
// staging file, validate it with backup.ParseUpload, then - only on
// success - build the preview against the live database and hand the
// parsed result, staging path included, to restoreStage.stage. src is
// never read into a []byte of its own by this function; it moves through
// io.Copy's own bounded buffer, once.
//
// There is no shared name to rename onto and nothing to "commit": every
// call gets its own file from backup.CreateRestoreStagingTemp, so a failed
// validation simply removes that one file and returns, and a successful
// one hands ownership of it straight to restoreStage.stage, which is the
// only place a previous stage's file is ever removed (under its own
// mutex - restoreStage's own doc comment explains why that is what makes
// "one slot" safe between two concurrent inspects).
func (a *api) stageAndInspect(w http.ResponseWriter, r *http.Request, src io.Reader) {
	tmp, err := backup.CreateRestoreStagingTemp(a.backupDir)
	if err != nil {
		a.logger.Error("creating restore staging temp file", "error", err)
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
		a.logger.Error("closing restore staging temp file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	parsed, err := backup.ParseUpload(tmpPath)
	if err != nil {
		writeRestoreParseError(w, a.logger, err)
		return
	}

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
	staged = true

	writeJSON(w, http.StatusOK, restoreInspectResponse{Token: token, Preview: preview})
}

// writeRestoreStreamError maps a streaming read failure - either finding
// the upload's "file" part or copying it - onto the same two codes
// inspectRestoreUpload always has: restore_file_too_large when
// http.MaxBytesReader is what stopped the read, invalid_argument
// otherwise. Kept as one function so both call sites answer identically.
func writeRestoreStreamError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		// restore_file_too_large, not receipts.go's payload_too_large: that
		// code's own copy ("Ukuran foto lebih dari 10 MB") names a photo and
		// a 10 MB limit - wrong on both counts here, and copy.common.errors
		// is one shared map keyed by code across the whole app, so a backup
		// upload needs its own.
		writeAPIError(w, http.StatusRequestEntityTooLarge, "restore_file_too_large", "The backup file is larger than the 256 MB limit.")
		return
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The backup file could not be read.")
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

	ip := "restore-ip:" + a.clientIP(r)
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

	// take() already cleared the stage's own bookkeeping and handed this
	// handler sole ownership of parsed.StagingPath - nothing else will ever
	// remove it, so this handler must, once backup.Restore has read it:
	// on the success path below and on this one alike, since either way
	// there is nothing left anyone could still confirm with this token
	// (issue #344: "a successful confirm, and a failed one, removes it").
	restoreErr := backup.Restore(r.Context(), a.sqlDB, a.queries, a.ledger, a.uploadsDir, a.backupDir, parsed, time.Now())
	if err := os.Remove(parsed.StagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.logger.Error("removing restore staging file after confirm", "error", err)
		// Not itself a reason to report the restore as having failed -
		// same reasoning as the session-destroy failure below.
	}
	if restoreErr != nil {
		writeRestoreCommitError(w, a.logger, restoreErr)
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
