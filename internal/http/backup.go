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
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
// Cadangan card to render a row (date, kind, whether it can still be
// restored) and for the download route to be given the name to ask for. All
// of it is read off DumpInfo, parsed from the file's name
// (internal/backup/dumps.go); nothing is derived a second way.
type backupListItem struct {
	Name            string `json:"name"`
	Date            string `json:"date"` // YYYY-MM-DD, Asia/Jakarta
	Kind            string `json:"kind"` // "daily" | "pre-restore"
	FormatVersion   int64  `json:"format_version"`
	IsCurrentFormat bool   `json:"is_current_format"`
	SizeBytes       int64  `json:"size_bytes"`
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
			Date:            d.When.Format("2006-01-02"),
			Kind:            string(d.Kind),
			FormatVersion:   d.FormatVersion,
			IsCurrentFormat: d.FormatVersion == backup.FormatVersion,
			SizeBytes:       info.Size(),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// downloadStoredBackup is GET /api/backups/{name}: streams one written dump
// back byte for byte. name must be a filename backup.ListDumps returned, never
// trusted as a bare path segment however chi routed it;
// resolveStoredBackupPath validates it, and the os.ReadFile error below
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
	data, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
			return
		}
		a.log(r).Error("reading stored backup", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	a.securityEvent(r, slog.LevelInfo, "backup_downloaded", "kind", "stored", "name", name)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: data is the raw bytes of a zip file read from
	// full, which was just validated (ValidDumpName's full match against
	// dumpNamePattern, then the filepath.Rel containment check above) - not
	// attacker-controlled content reaching a template or a header, just a
	// byte stream written under Content-Type: application/zip, the same
	// shape downloadBackup's own w.Write(zipBytes) below is never flagged
	// for because gosec's taint tracking has nothing to trace there.
	_, _ = w.Write(data)
}

// downloadBackup answers with the finished zip, Content-Disposition set so the
// browser saves it under backup.ZipFilename's dated name rather than opening
// it. Session-gated like every route in this group (api.go's second
// r.Group); Pengaturan's Cadangan card tells the treasurer the file carries
// the login, so this route has nothing to warn about.
//
// Unscoped by fund on purpose: ADR-012's restore scope is the whole instance,
// so the download is too; backup.Export walks every fund ListFunds returns,
// not the one resolveFund would answer with.
func (a *api) downloadBackup(w http.ResponseWriter, r *http.Request) {
	zipBytes, missing, err := backup.Export(r.Context(), a.sqlDB, a.uploadsDir)
	if err != nil {
		a.log(r).Error("building backup zip", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	if len(missing) > 0 {
		// Skipped, not failed (backup.Export): the operator learns a photo is gone
		// from the uploads volume; the treasurer still gets her backup.
		a.log(r).Warn("backup: receipt images missing from uploads dir", "count", len(missing), "paths", missing)
	}

	a.securityEvent(r, slog.LevelInfo, "backup_downloaded", "kind", "fresh")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+backup.ZipFilename(a.now().In(tz.Jakarta))+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(zipBytes)
}
