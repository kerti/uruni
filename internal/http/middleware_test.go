package http

import (
	"bytes"
	"encoding/json"
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
// Referrer-Policy and its own nonce CSP (TestReportCSPNonceMatchesThePage).
func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	for _, tc := range []struct{ path, referrer, csp string }{
		{"/", "same-origin", appCSP},
		{"/riwayat", "same-origin", appCSP},
		{"/api/session", "same-origin", appCSP},
		{"/api/transactions", "same-origin", appCSP},
		{"/healthz", "same-origin", appCSP},
		{"/report/no-such-slug", "no-referrer", ""},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		h := rec.Header()
		for name, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         tc.referrer,
			"X-Frame-Options":         "DENY",
			"Content-Security-Policy": tc.csp,
		} {
			if want == "" {
				continue
			}
			if got := h.Get(name); got != want {
				t.Errorf("GET %s (%d): %s = %q, want %q", tc.path, rec.Code, name, got, want)
			}
		}
		if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("GET %s: Content-Security-Policy = %q, want frame-ancestors 'none'", tc.path, csp)
		}
	}
}

// The app's script-src is 'self' alone: no 'unsafe-inline', no
// 'unsafe-eval', no other origin. That is the directive the policy exists
// for, so it is pinned on its own rather than only as part of a string.
func TestAppCSPAllowsOnlySameOriginScript(t *testing.T) {
	t.Parallel()
	var scriptSrc string
	for d := range strings.SplitSeq(appCSP, ";") {
		if f := strings.Fields(d); len(f) > 0 && f[0] == "script-src" {
			scriptSrc = strings.Join(f[1:], " ")
		}
	}
	if scriptSrc != "'self'" {
		t.Errorf("appCSP script-src = %q, want 'self'", scriptSrc)
	}
}

// A write a browser sends from another origin is refused before any handler
// runs - cross-site, and same-site too, which SameSite=Lax alone lets through
// (#481). Same-origin writes, writes with no browser headers at all, and
// reads from anywhere pass. POST /api/logout answers 204 whenever it is
// reached, so any other status is the guard's.
func TestCrossOriginWritesAreRefused(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	for _, tc := range []struct {
		name, method, path string
		headers            map[string]string
		want               int
	}{
		{"cross-site fetch", http.MethodPost, "/api/logout", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"same-site sibling subdomain", http.MethodPost, "/api/logout", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://other.example.com"}, http.StatusForbidden},
		{"older browser, foreign Origin", http.MethodPost, "/api/logout", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"same-origin fetch", http.MethodPost, "/api/logout", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://uruni.example.com"}, http.StatusNoContent},
		{"older browser, own Origin", http.MethodPost, "/api/logout", map[string]string{"Origin": "https://uruni.example.com"}, http.StatusNoContent},
		{"typed into the address bar", http.MethodPost, "/api/logout", map[string]string{"Sec-Fetch-Site": "none"}, http.StatusNoContent},
		{"no browser headers (curl)", http.MethodPost, "/api/logout", nil, http.StatusNoContent},
		{"cross-site read", http.MethodGet, "/api/session", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, "https://uruni.example.com"+tc.path, nil)
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Errorf("%s: %s %s = %d, want %d (body: %s)", tc.name, tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
			continue
		}
		if tc.want == http.StatusForbidden {
			var env errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "cross_origin_request" {
				t.Errorf("%s: body = %s, want the cross_origin_request envelope", tc.name, rec.Body.String())
			}
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("%s: Set-Cookie = %q, want none: a refused request never reaches the session", tc.name, got)
			}
		}
	}
}
