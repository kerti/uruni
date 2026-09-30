package http

// GET /api/backup (M6.37, #323, ADR-012): the whole fund, downloaded as one
// zip - uruni.json plus every receipt image under its stored name. All of
// the actual work lives in internal/backup, so #325's importer can sit
// beside it in the same package; this file is the thin route that streams
// what that package builds.

import (
	"net/http"
	"time"

	"github.com/kerti/uruni/internal/backup"
)

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
