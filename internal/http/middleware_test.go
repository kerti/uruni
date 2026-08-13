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
	// neither here, but the assertion pins the shape — only method, path,
	// status and duration ever appear on this line.
	for _, forbidden := range []string{"member", "amount", "rupiah"} {
		if strings.Contains(strings.ToLower(line), forbidden) {
			t.Errorf("log line %q unexpectedly contains %q", line, forbidden)
		}
	}
}
