package http

// The public report (ADR-035, #373): GET /report/{slug}, server-rendered with
// html/template from ledger.MonthlyReport. No session, no resolveFund - the
// slug names its fund (ADR-030's carve-out), and is a read capability, never an
// authorization. This slice renders the header and the month selector; the
// transactions, dues and envelopes sections fill in beneath it in later
// slices, from the same Report.

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

//go:embed report.html
var reportTemplateSource string

var reportTemplates = template.Must(template.New("report").Parse(reportTemplateSource))

// reportPage is everything the "report" template reads. Every string is
// already display text - formatted rupiah, Indonesian dates - so the template
// only places it, and nothing is computed there (ADR-035).
type reportPage struct {
	Title string
	Style template.CSS
	Text  reportCopy

	FundName string
	AsOf     string
	Balance  string
	Check    *reportCheck
	Purposes []reportPurpose

	PrevMonth    string
	NextMonth    string
	MonthOptions []reportMonthOption

	// Empty is a fund with nothing recorded at all - not merely a quiet
	// month - which gets the warm line instead of a bare Rp 0.
	Empty bool
}

// reportCheck is the latest cek kas, as the home banner reads it: matched,
// a discrepancy in terracotta, or never counted (neutral, never green).
type reportCheck struct {
	Class   string // "matched", "discrepancy" or "never"
	Message string
	When    string
}

type reportPurpose struct {
	Name     string
	Balance  string
	Negative bool
}

type reportMonthOption struct {
	Value    string
	Label    string
	Selected bool
}

// reportHandler serves GET /report/{slug}. now is injectable so a test can
// pin the Jakarta month; production passes time.Now.
func reportHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

		fund, err := q.GetFundByReportSlug(r.Context(), chi.URLParam(r, "slug"))
		if errors.Is(err, sql.ErrNoRows) {
			renderReport(w, logger, http.StatusNotFound, "notfound", reportPage{
				Title: reportText.NotFoundTitle,
				Style: reportStyle(),
				Text:  reportText,
			})
			return
		}
		if err != nil {
			logger.Error("report: looking up the slug", "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		report, err := monthlyReportOrCurrent(r.Context(), l, fund.ID, r.URL.Query().Get("month"), now())
		if err != nil {
			logger.Error("report: assembling the month", "fund_id", fund.ID, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		_, err = q.FirstTransactionDateByFund(r.Context(), fund.ID)
		empty := errors.Is(err, sql.ErrNoRows)
		if err != nil && !empty {
			logger.Error("report: checking for any transaction", "fund_id", fund.ID, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		renderReport(w, logger, http.StatusOK, "report", buildReportPage(report, empty))
	}
}

// monthlyReportOrCurrent reads ?month= as ADR-035 says to: a missing,
// malformed or out-of-range month is the current one, never an error page.
// A malformed month is the ledger's ErrInvalidArgument; an out-of-range one
// assembles fine but names a month the selector does not offer, so it is
// re-read as the current month too.
func monthlyReportOrCurrent(ctx context.Context, l *ledger.Ledger, fundID int64, month string, now time.Time) (ledger.Report, error) {
	report, err := l.MonthlyReport(ctx, ledger.ReportParams{FundID: fundID, Month: month, Now: now})
	if err == nil && slices.Contains(report.Months, report.Month) {
		return report, nil
	}
	if err != nil && !errors.Is(err, ledger.ErrInvalidArgument) {
		return ledger.Report{}, err
	}
	return l.MonthlyReport(ctx, ledger.ReportParams{FundID: fundID, Now: now})
}

func buildReportPage(r ledger.Report, empty bool) reportPage {
	page := reportPage{
		Title:    reportText.Title(r.FundName),
		Style:    reportStyle(),
		Text:     reportText,
		FundName: r.FundName,
		AsOf:     reportText.AsOf(reportText.longDate(r.AsOf)),
		Balance:  money.FormatIDR(r.Balance),
		Empty:    empty,
	}

	switch rec := r.Reconciliation; {
	case rec == nil:
		page.Check = &reportCheck{Class: "never", Message: reportText.NeverChecked}
	case rec.Matched:
		page.Check = &reportCheck{Class: "matched", Message: reportText.Matched, When: reportText.LastChecked(reportText.longDate(rec.Date))}
	default:
		page.Check = &reportCheck{
			Class:   "discrepancy",
			Message: reportText.Discrepancy(money.FormatIDR(rec.Difference)),
			When:    reportText.LastChecked(reportText.longDate(rec.Date)),
		}
	}

	for _, p := range r.PurposeBalances {
		page.Purposes = append(page.Purposes, reportPurpose{
			Name:     p.Name,
			Balance:  money.FormatIDR(p.Balance),
			Negative: p.Balance < 0,
		})
	}

	i := slices.Index(r.Months, r.Month)
	if i > 0 {
		page.PrevMonth = r.Months[i-1]
	}
	if i >= 0 && i < len(r.Months)-1 {
		page.NextMonth = r.Months[i+1]
	}
	// Newest first: the month a visitor most likely wants is at the top.
	for j := len(r.Months) - 1; j >= 0; j-- {
		m := r.Months[j]
		page.MonthOptions = append(page.MonthOptions, reportMonthOption{
			Value:    m,
			Label:    reportText.monthName(m),
			Selected: m == r.Month,
		})
	}

	return page
}

// setReportHeaders is ADR-035's request edge: not indexed, not followed, no
// referrer leaking the slug onward, not cached by anything in between, and no
// cookie - this route never touches the session manager, which is mounted on
// /api alone.
func setReportHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// renderReport executes into a buffer first, so a template error is a clean
// 500 rather than half a page under a 200 that has already gone out.
func renderReport(w http.ResponseWriter, logger *slog.Logger, status int, name string, page reportPage) {
	var buf bytes.Buffer
	if err := reportTemplates.ExecuteTemplate(&buf, name, page); err != nil {
		logger.Error("report: rendering", "template", name, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
