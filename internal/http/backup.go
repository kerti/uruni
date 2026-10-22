package http

// GET /api/backup (ADR-012): the whole fund as one zip, uruni.json plus every
// receipt image under its stored name. The work lives in internal/backup; this
// file is the thin route that streams what it builds.
//
// GET /api/backups and GET /api/backups/{name} (ADR-012/013) list and
// download the server-side dumps internal/backup's scheduler and boot check
// write to URUNI_BACKUP_DIR. Neither builds anything, they only read the
// directory, which is why they are separate from the singular /api/backup.

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/tz"
)

// resolveStoredBackupPath validates name against backup.ValidDumpName and
// resolves it to a path inside a.backupDir: the one place a client-supplied
// backup name becomes a filesystem path, shared by downloadStoredBackup and
// restore.go's inspectStoredBackup (#326). Both address a dump by this
// identifier, never a client path.
//
// Two checks, both on purpose: ValidDumpName alone refuses every
// path-traversal shape (no "/" or ".." in dumpNamePattern), but resolving the
// joined path and requiring it stay inside a.backupDir matches the
// belt-and-suspenders internal/http applies wherever a path is built from
// request input, and costs nothing.
func (a *api) resolveStoredBackupPath(name string) (string, bool) {
	if !backup.ValidDumpName(name) {
		return "", false
	}
	full := filepath.Join(a.backupDir, name)
	if rel, err := filepath.Rel(a.backupDir, full); err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return full, true
}

// backupListItem is one dump's wire shape (GET /api/backups): enough for the
// Cadangan card to render a row (date, kind, whether it can be restored) and for
// the download route to be given the name to ask for. All
// of it is read off DumpInfo, parsed from the file's name
// (internal/backup/dumps.go); nothing is derived a second way.
type backupListItem struct {
	Name            string `json:"name"`
	Date            string `json:"date"` // YYYY-MM-DD, in the zone the dump's name is stamped in
	Kind            string `json:"kind"` // "daily" | "pre-restore"
	FormatVersion   int64  `json:"format_version"`
	IsCurrentFormat bool   `json:"is_current_format"` // written by this build's format; false for an older one
	// CanRestore is true for any format the importer can read or upgrade
	// (ADR-012, ADR-040): the restore button follows this, not is_current_format.
	CanRestore bool  `json:"can_restore"`
	SizeBytes  int64 `json:"size_bytes"`
}

// listBackups is GET /api/backups: every dump backup.ListDumps recognises in
// URUNI_BACKUP_DIR, newest first: the Cadangan card's list, with no dialog or
// filter ("one thing to browse, not to edit", see Backup.tsx). Unscoped by
// fund like /api/backup: a dump is a whole-instance file (ADR-012's restore
// scope).
func (a *api) listBackups(w http.ResponseWriter, r *http.Request) {
	dumps, err := backup.ListDumps(a.backupDir)
	if err != nil {
		a.log(r).Error("listing backups", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	// Always an array, never "null", for an instance with no dumps (as
	// getBalances and BuildDocument's slices).
	items := make([]backupListItem, 0, len(dumps))
	for _, d := range dumps {
		info, err := os.Stat(filepath.Join(a.backupDir, d.Name))
		if err != nil {
			a.log(r).Error("statting backup", "name", d.Name, "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
			return
		}
		items = append(items, backupListItem{
			Name:            d.Name,
			Date:            d.When.Format("2006-01-02"), // the name's own date, in the zone it names
			Kind:            string(d.Kind),
			FormatVersion:   d.FormatVersion,
			IsCurrentFormat: d.FormatVersion == backup.FormatVersion,
			CanRestore:      backup.CanUpgradeFormat(d.FormatVersion),
			SizeBytes:       info.Size(),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// downloadStoredBackup is GET /api/backups/{name}: streams one written dump
// back byte for byte, from the file to the response through one small buffer
// (#533), never read whole. name must be a filename backup.ListDumps returned,
// never trusted as a bare path segment however chi routed it;
// resolveStoredBackupPath validates it, and the open error below
// (fs.ErrNotExist) turns a valid but unknown or since-pruned name into a 404
// instead of a 500.
func (a *api) downloadStoredBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	full, ok := a.resolveStoredBackupPath(name)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "That is not a valid backup name.")
		return
	}

	//nolint:gosec // full is a.backupDir joined with name, and name has just
	// been proven to match dumpNamePattern in full (ValidDumpName) - the
	// same trust boundary internal/backup/export.go's own addReceiptToZip
	// already relies on for a server-generated filename.
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
			return
		}
		a.log(r).Error("opening stored backup", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	defer func() { _ = f.Close() }()

	a.securityEvent(r, slog.LevelInfo, "backup_downloaded", "kind", "stored", "name", name)
	a.serveZip(w, r, f, name)
}

// downloadBackup answers with a fresh zip, Content-Disposition set so the
// browser saves it under backup.ZipFilename's dated name rather than opening
// it. Session-gated like every route in this group (api.go's second
// r.Group); Pengaturan's Cadangan card tells the treasurer the file carries
// the login, so this route has nothing to warn about.
//
// Unscoped by fund on purpose: ADR-012's restore scope is the whole instance,
// so the download is too; backup.Export walks every fund ListFunds returns,
// not the one resolveFund would answer with.
//
// The zip is streamed into a temp file in backupDir, then from the file to the
// response (#533): memory holds one buffer, never the archive. Building it
// first is what keeps a failure an honest 500 - a zip streamed straight to the
// response would already have answered 200 when a receipt read failed - and it
// lets the response carry its length. The cost is disk: one more backup's size
// in backupDir for as long as the download runs (SELF-HOSTING.md).
func (a *api) downloadBackup(w http.ResponseWriter, r *http.Request) {
	// The name's date is the fund's day (ADR-041); the download is instance-wide,
	// so the zone is the first fund's, or tz.Default with none.
	zone, err := backup.InstanceZone(r.Context(), a.queries)
	if err != nil {
		a.log(r).Error("reading the fund's time zone", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	loc, err := tz.Load(zone)
	if err != nil {
		a.log(r).Error("fund has an unusable time zone", "time_zone", zone, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	tmp, err := backup.CreateDownloadTemp(a.backupDir)
	if err != nil {
		a.log(r).Error("creating the download's temp file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	missing, err := backup.Export(r.Context(), a.sqlDB, a.uploadsDir, tmp)
	if err != nil {
		a.log(r).Error("building backup zip", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		a.log(r).Error("rewinding the download's temp file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	if len(missing) > 0 {
		// Skipped, not failed (backup.Export): the operator learns a photo is gone
		// from the uploads volume; the treasurer still gets her backup.
		a.log(r).Warn("backup: receipt images missing from uploads dir", "count", len(missing), "paths", missing)
	}

	a.securityEvent(r, slog.LevelInfo, "backup_downloaded", "kind", "fresh")
	a.serveZip(w, r, tmp, backup.ZipFilename(a.now().In(loc)))
}

// serveZip writes a finished zip file to the response as an attachment named
// name, with its length, copying from f rather than reading it whole.
func (a *api) serveZip(w http.ResponseWriter, r *http.Request, f *os.File, name string) {
	info, err := f.Stat()
	if err != nil {
		a.log(r).Error("reading the backup's size", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		// The status is already sent; a cut-off copy is the client's to notice,
		// and usually the client is why it was cut off.
		a.log(r).Info("backup download ended early", "name", name, "error", err)
	}
}
