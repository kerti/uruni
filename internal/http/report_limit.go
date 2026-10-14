package http

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

// The public report's per-client budgets (#520). The link is shared in group
// chats, so a burst of neighbours opening it at once is normal; what these
// bound is one client hammering a route that recomputes a month (and, for the
// PDF, re-draws and re-compresses it) on the single database connection the
// treasurer's writes also use (ADR-004).
//
// A fixed window per client network, the same limiter login uses (without an
// escalation ladder: a full window just stays closed until it ends, so the
// worst a burst costs is the rest of one minute). HTML is 120 a minute, two a
// second, more than any person or chat link-preview fan-out reaches. The PDF
// is 20 a minute: it is the heavier route (a render costs about as much as the
// page plus the drawing and compression) and nobody downloads it twenty times
// a minute.
//
// The PDF counts against its own bucket only, not the page's as well: the PDF
// link sits on the page, so a visitor who has used her page budget must not be
// unable to download the file, and each route is bounded by its own figure.
const (
	reportHTMLLimit   = 120
	reportPDFLimit    = 20
	reportLimitWindow = time.Minute
)

// reportLimiter holds both buckets in one rateLimiter, told apart by key
// prefix, each with its own rule.
type reportLimiter struct {
	rl  *rateLimiter
	api *api // the router's api, for clientNetwork and securityEvent only
}

func newReportLimiter(trusted *api, now func() time.Time) *reportLimiter {
	rl := newRateLimiter(reportHTMLLimit, reportLimitWindow)
	rl.now = now
	return &reportLimiter{rl: rl, api: trusted}
}

// middleware limits one bucket: kind is "html" or "pdf" and limit is
// that bucket's budget per window. A request over it is answered 429 with the
// report's own page and Retry-After, and logs one security event (the client's
// address and the kind, never the slug).
func (rlm *reportLimiter) middleware(kind string, limit int, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := kind + "|" + rlm.api.clientNetwork(r)
			if _, ok := rlm.rl.reserveRules(keyRule{key: key, rule: limitRule{limit: limit, window: reportLimitWindow}}); !ok {
				rlm.api.securityEvent(r, slog.LevelWarn, "report_rate_limited", "kind", kind)
				setReportHeaders(w)
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(rlm.rl.lockedFor(key).Seconds())))))
				renderReport(w, loggerFrom(r.Context(), logger), http.StatusTooManyRequests, "ratelimited", reportPage{
					Title: reportText.RateLimitedTitle,
					Style: reportStyle(),
					Text:  reportText,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
