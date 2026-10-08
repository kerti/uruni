package http

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestLoggingMiddlewareRoundTrips exercises requestLogger directly
// against a placeholder handler: the middleware itself is what M4's other
// slices depend on, not any particular production route.
func TestRequestLoggingMiddlewareRoundTrips(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	placeholder := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/placeholder", nil)
	requestLogger(logger)(placeholder).ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}

	line := buf.String()
	for _, want := range []string{
		"method=GET",
		"path=/placeholder",
		"status=418",
		"duration_ms=",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q missing %q", line, want)
		}
	}

	// ADR-022's logging rule: no member name, no amount. The request carries
	// neither here, but the assertion pins the shape - only method, path,
	// status and duration ever appear on this line.
	for _, forbidden := range []string{"member", "amount", "rupiah"} {
		if strings.Contains(strings.ToLower(line), forbidden) {
			t.Errorf("log line %q unexpectedly contains %q", line, forbidden)
		}
	}
}

// TestRequestLoggerRedactsTheReportSlug is #447's: the slug is the public
// report's only guard, so it never reaches a log line - not on the page, not
// on the PDF, not on a /report/ path no route matches.
func TestRequestLoggerRedactsTheReportSlug(t *testing.T) {
	t.Parallel()
	const slug = "Ab3dEf6hIj9kLm2nOp5qRs8tUv1wXy4z"

	for _, tc := range []struct{ path, want string }{
		{"/report/" + slug, "path=/report/:slug "},
		{"/report/" + slug + "/pdf", "path=/report/:slug/pdf "},
		{"/report/" + slug + "/no/such/thing", "path=/report/:slug/no/such/thing "},
		{"/report/", "path=/report/ "},
		{"/reports/" + slug, "path=/reports/" + slug + " "},
	} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

		requestLogger(logger)(ok).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))

		line := buf.String()
		if !strings.Contains(line, tc.want) {
			t.Errorf("GET %s logged %q, want it to contain %q", tc.path, line, tc.want)
		}
		if strings.HasPrefix(tc.path, "/report/") && strings.Contains(line, slug) {
			t.Errorf("GET %s logged the slug: %q", tc.path, line)
		}
	}
}

// TestEveryResponseCarriesTheSecurityHeaders is #447's: the SPA shell, the
// API (signed in or not) and the public report, including its 404, all
// answer with the same baseline - and the report keeps its stricter
// Referrer-Policy.
func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	for _, tc := range []struct{ path, referrer string }{
		{"/", "same-origin"},
		{"/riwayat", "same-origin"},
		{"/api/session", "same-origin"},
		{"/api/transactions", "same-origin"},
		{"/healthz", "same-origin"},
		{"/report/no-such-slug", "no-referrer"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		h := rec.Header()
		for name, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         tc.referrer,
			"X-Frame-Options":         "DENY",
			"Content-Security-Policy": "frame-ancestors 'none'",
		} {
			if got := h.Get(name); got != want {
				t.Errorf("GET %s (%d): %s = %q, want %q", tc.path, rec.Code, name, got, want)
			}
		}
	}
}
