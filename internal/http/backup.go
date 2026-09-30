package http

// GET /api/backup (M6.37, #323, ADR-012): the whole fund, downloaded as one
// zip - uruni.json plus every receipt image under its stored name. All of
// the actual work lives in internal/backup, so #325's importer can sit
// beside it in the same package; this file is the thin route that streams
// what that package builds.
//
// GET /api/backups and GET /api/backups/{name} (M6.38, #324, ADR-012/013)
// are this file's other addition: listing the server-side dumps
// internal/backup's own scheduler and boot check already write to
// URUNI_BACKUP_DIR, and downloading one of them by name. Neither builds
// anything - they only ever read the directory - which is the whole reason
// they are separate routes from the singular /api/backup above.

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/backup"
)

// backupListItem is one dump's own wire shape (GET /api/backups): enough
// for the Cadangan card to render a row - date, kind, whether it can still
// be restored - and enough for the download route below to be handed back
// exactly the name it must ask for. Everything here is read straight off
// DumpInfo, parsed from the file's own name (internal/backup/dumps.go) -
// nothing is derived a second way.
type backupListItem struct {
	Name            string `json:"name"`
	Date            string `json:"date"` // YYYY-MM-DD, Asia/Jakarta
	Kind            string `json:"kind"` // "daily" | "pre-restore"
	FormatVersion   int64  `json:"format_version"`
	IsCurrentFormat bool   `json:"is_current_format"`
	SizeBytes       int64  `json:"size_bytes"`
}

// listBackups is GET /api/backups: every dump backup.ListDumps recognises
// in URUNI_BACKUP_DIR, newest first - the Cadangan card's own list, no
// dialog, no filter, matching the whole section's "one thing to browse, not
// to edit" shape (Backup.tsx's own comment). Unscoped by fund for the same
// reason /api/backup already is: a dump is a whole-instance file, not a
// per-fund one (ADR-012's restore scope).
func (a *api) listBackups(w http.ResponseWriter, _ *http.Request) {
	dumps, err := backup.ListDumps(a.backupDir)
	if err != nil {
		a.logger.Error("listing backups", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	// Always an array, never "null" for an instance with no dumps yet
	// (the same "always an array" contract getBalances and BuildDocument's
	// own slices keep for their own wire shapes).
	items := make([]backupListItem, 0, len(dumps))
	for _, d := range dumps {
		info, err := os.Stat(filepath.Join(a.backupDir, d.Name))
		if err != nil {
			a.logger.Error("statting backup", "name", d.Name, "error", err)
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

// downloadStoredBackup is GET /api/backups/{name}: streams one already-
// written dump back out, byte for byte. name must be exactly a filename
// backup.ListDumps just returned to this same caller - never trusted as a
// bare path segment, however chi routed it here.
//
// Two checks, deliberately both: ValidDumpName alone would already refuse
// every path-traversal shape (dumpNamePattern's character classes contain
// no "/" and no ".."), but resolving the joined path and requiring it stay
// inside a.backupDir is the same belt-and-suspenders internal/http already
// applies wherever a path is built from request input elsewhere in this
// package - cheap, and it costs nothing to keep the two packages' habits
// matched. The listing check (does a dump by exactly this name exist right
// now) is what turns "a syntactically valid but unknown name" into a plain
// 404 instead of an attempted read of a file that was never written, or
// was since pruned by retention.
func (a *api) downloadStoredBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !backup.ValidDumpName(name) {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "That is not a valid backup name.")
		return
	}

	// Belt-and-suspenders on top of ValidDumpName's own pattern match
	// (which already contains no "/" or ".." in its character classes, so
	// this can never actually fire against a name that passed it): resolve
	// the joined path and require it still sit inside a.backupDir before
	// ever calling os.ReadFile on it.
	full := filepath.Join(a.backupDir, name)
	if rel, err := filepath.Rel(a.backupDir, full); err != nil || strings.HasPrefix(rel, "..") {
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
		a.logger.Error("reading stored backup", "name", name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

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

// downloadBackup answers with the finished zip, Content-Disposition set so
// the browser saves it under backup.ZipFilename's dated name rather than opening it
// inline. Session-gated like every route in this group (api.go's second
// r.Group) - the copy on Pengaturan's own Cadangan card is what tells the
// treasurer this file carries the login, so there is nothing left for this
// route itself to warn about.
//
// Unscoped by fund on purpose: ADR-012's restore scope is the whole
// instance, so the download is too - backup.Export itself walks every fund
// ListFunds returns, not just the one resolveFund would answer with.
func (a *api) downloadBackup(w http.ResponseWriter, r *http.Request) {
	zipBytes, missing, err := backup.Export(r.Context(), a.queries, a.ledger, a.uploadsDir)
	if err != nil {
		a.logger.Error("building backup zip", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	if len(missing) > 0 {
		// Skipped, not failed (backup.Export): the operator learns a photo is
		// gone from the uploads volume; the treasurer still gets her backup.
		a.logger.Warn("backup: receipt images missing from uploads dir", "count", len(missing), "paths", missing)
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+backup.ZipFilename(time.Now())+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(zipBytes)
}
