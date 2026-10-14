package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kerti/uruni"
)

// #521: the notices are public (no session), plain text, and exactly the file
// embedded in the binary - not the SPA shell the fallback would otherwise send.
func TestThirdPartyNoticesIsPublicPlainText(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/third-party-notices", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /third-party-notices = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	if rec.Body.String() != string(uruni.ThirdPartyNotices()) {
		t.Error("body is not the embedded THIRD-PARTY-NOTICES")
	}
	if strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Error("answered with the SPA shell")
	}
}

// The embedded file is the generated one, not the placeholder: it names the
// components that must be attributed.
func TestEmbeddedNoticesNameTheBundledComponents(t *testing.T) {
	t.Parallel()
	text := string(uruni.ThirdPartyNotices())
	for _, want := range []string{"modernc.org/sqlite", "Plus Jakarta Sans", "Noto Sans", "workbox-core", "shadcn/ui"} {
		if !strings.Contains(text, want) {
			t.Errorf("THIRD-PARTY-NOTICES does not mention %q", want)
		}
	}
}
