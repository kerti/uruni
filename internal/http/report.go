package http

// The public report's request edge (ADR-035, #373): GET /report/{slug} and
// /report/{slug}/pdf. No session, no resolveFund: the slug names its fund
// (ADR-030's carve-out) and is a read capability, never an authorization. The
// page and the file are built and drawn by internal/report; this file routes,
// looks the slug up, and owns the headers, the CSP nonce and the status codes.

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/report"
	"github.com/kerti/uruni/internal/store"
)

// reportHandler serves GET /report/{slug}. The running month is the ledger
// clock's, read in the fund's zone.
func reportHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

		logger := loggerFrom(r.Context(), logger)
		fund, ok := reportFundOr404(w, r, q, logger)
		if !ok {
			return
		}

		page, err := report.Build(r.Context(), l, q, fund.ID, r.URL.Query())
		if err != nil {
			logger.Error("report: assembling the month", "fund_id", fund.ID, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		// The link is to the month the page shows, the ledger's validated "YYYY-MM",
		// never the raw query value.
		page.PDFHref = "/report/" + url.PathEscape(fund.ReportSlug) + "/pdf?month=" + url.QueryEscape(page.Month)
		writeReportHTML(w, logger, http.StatusOK, "report", func(w io.Writer, nonce string) error {
			return report.RenderHTML(w, page, nonce)
		})
	}
}

// reportPDFHandler serves GET /report/{slug}/pdf with the page's edge: the
// same headers, the same 404 for an unknown slug, a missing or malformed month
// is the current one.
func reportPDFHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

		logger := loggerFrom(r.Context(), logger)
		fund, ok := reportFundOr404(w, r, q, logger)
		if !ok {
			return
		}

		body, month, err := report.PDF(r.Context(), l, fund.ID, r.URL.Query().Get("month"))
		if err != nil {
			logger.Error("report: rendering the pdf", "fund_id", fund.ID, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		h := w.Header()
		h.Set("Content-Type", "application/pdf")
		// month is the ledger's validated "YYYY-MM", never the raw query value.
		h.Set("Content-Disposition", `attachment; filename="`+report.Text.PDFFilename(month)+`"`)
		h.Set("Content-Length", strconv.Itoa(len(body)))
		//nolint:gosec // G705 is an HTML-injection rule; this body is application/pdf bytes drawn by fpdf, served as an attachment, never parsed as markup
		_, _ = w.Write(body)
	}
}

// reportFundOr404 is the lookup both report routes open with: the fund the
// slug names, or a short 404 naming no fund and ok == false.
func reportFundOr404(w http.ResponseWriter, r *http.Request, q store.Querier, logger *slog.Logger) (store.Fund, bool) {
	fund, err := q.GetFundByReportSlug(r.Context(), chi.URLParam(r, "slug"))
	switch {
	case err == nil:
		return fund, true
	case errors.Is(err, sql.ErrNoRows):
		writeReportHTML(w, logger, http.StatusNotFound, "notfound", func(w io.Writer, nonce string) error {
			return report.RenderNotFound(w, nonce)
		})
	default:
		logger.Error("report: looking up the slug", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
	return store.Fund{}, false
}

// setReportHeaders is ADR-035's request edge: not indexed, not followed, no
// referrer leaking the slug onward, not cached in between, and no cookie
// (this route never touches the session manager, mounted on /api alone).
func setReportHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// reportCSP is the report's Content-Security-Policy, replacing the app's
// (securityHeaders). The page is one self-contained response: its only script
// and style are inline and carry this response's nonce, so nothing else (an
// injected tag, an attribute handler, a file from anywhere) runs or applies.
// img-src covers the browser's favicon request; form-action keeps the filter
// forms on this origin.
func reportCSP(nonce string) string {
	return "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'nonce-" + nonce + "'; " +
		"img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
}

// writeReportHTML renders into a buffer first, so a template error is a clean
// 500 rather than half a page under a 200 already sent. Each render draws a
// fresh nonce: the page is no-store, so no copy outlives the header naming
// its nonce.
func writeReportHTML(w http.ResponseWriter, logger *slog.Logger, status int, name string, render func(w io.Writer, nonce string) error) {
	nonce := rand.Text()
	var buf bytes.Buffer
	if err := render(&buf, nonce); err != nil {
		logger.Error("report: rendering", "template", name, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", reportCSP(nonce))
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
