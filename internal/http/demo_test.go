package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// ADR-045: with Deps.Demo set the router is the public demo's; with it nil none
// of the demo behaviors exist. Each is checked in both modes.

const (
	demoTestEmail = "demo@uruni.test"
	//nolint:gosec // not a credential leak - a fixed test value standing in for the public demo password
	demoTestPassword = "coba-uruni"
)

// demoRouter is a router over a fresh database with the treasurer registered
// and signed in, in demo mode or not. It returns the router and the session
// cookie value.
func demoRouter(t *testing.T, demo bool) (http.Handler, string) {
	t.Helper()
	sqlDB := testStoreDB(t)
	deps := Deps{
		Assets: testAssets(), Build: testBuild, Ledger: ledger.New(sqlDB), Queries: store.New(sqlDB), DB: sqlDB, Logger: testLogger(),
		Auth: auth.New(sqlDB, auth.WithPasswordFuncs(
			func(p string) (string, error) { return "stub:" + p, nil },
			func(p, encoded string) (bool, error) { return encoded == "stub:"+p, nil },
		)),
		BaseURL: "", UploadsDir: t.TempDir(), BackupDir: t.TempDir(),
	}
	if demo {
		deps.Demo = &DemoLogin{Email: demoTestEmail, Password: demoTestPassword}
	}
	r := New(deps)
	reg := postRegister(t, r, demoTestEmail, demoTestPassword)
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d (body: %s)", reg.Code, reg.Body.String())
	}
	return r, sessionCookie(reg)
}

func demoDo(r http.Handler, method, path, cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != "" {
		req.AddCookie(requestCookie("session", cookie))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSessionProbeCarriesTheDemoLoginOnlyInDemoMode(t *testing.T) {
	t.Parallel()

	r, _ := demoRouter(t, true)
	rec := demoDo(r, http.MethodGet, "/api/session", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/session = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v (body: %s)", err, rec.Body.String())
	}
	want := map[string]any{"authenticated": false, "has_account": true, "demo_email": demoTestEmail, "demo_password": demoTestPassword}
	if len(got) != len(want) {
		t.Errorf("demo probe = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("demo probe[%q] = %v, want %v", k, got[k], v)
		}
	}

	r, _ = demoRouter(t, false)
	rec = demoDo(r, http.MethodGet, "/api/session", "", "")
	if body := strings.TrimSpace(rec.Body.String()); body != `{"authenticated":false,"has_account":true}` {
		t.Errorf("non-demo probe = %s, want only authenticated and has_account", body)
	}
}

func TestChangingTheLoginIsRefusedOnlyInDemoMode(t *testing.T) {
	t.Parallel()
	bodies := []struct{ path, body string }{
		{"/api/user/email", `{"email":"lain@uruni.test","current_password":"` + demoTestPassword + `"}`},
		{"/api/user/password", `{"new_password":"kata-sandi-baru-1","current_password":"` + demoTestPassword + `"}`},
	}

	t.Run("demo", func(t *testing.T) {
		t.Parallel()
		r, cookie := demoRouter(t, true)
		for _, c := range bodies {
			rec := demoDo(r, http.MethodPost, c.path, cookie, c.body)
			if rec.Code != http.StatusForbidden {
				t.Errorf("POST %s = %d, want 403 (body: %s)", c.path, rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "demo_login_locked" {
				t.Errorf("POST %s code = %q, want demo_login_locked", c.path, got.Code)
			}
		}
		// The login is untouched: the published credentials still sign in.
		if rec := loginWith(t, r, demoTestEmail, demoTestPassword, "198.51.100.7"); rec.Code != http.StatusOK {
			t.Errorf("login with the demo credentials after the refused changes = %d, want 200", rec.Code)
		}
	})

	t.Run("signed out is still a 401, not a hint about the mode", func(t *testing.T) {
		t.Parallel()
		r, _ := demoRouter(t, true)
		for _, c := range bodies {
			if rec := demoDo(r, http.MethodPost, c.path, "", c.body); rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s with no session = %d, want 401", c.path, rec.Code)
			}
		}
	})

	t.Run("nothing else under /api/user is blocked in demo", func(t *testing.T) {
		t.Parallel()
		r, cookie := demoRouter(t, true)
		if rec := demoDo(r, http.MethodGet, "/api/user", cookie, ""); rec.Code != http.StatusOK {
			t.Errorf("GET /api/user in demo = %d, want 200", rec.Code)
		}
	})

	t.Run("not demo", func(t *testing.T) {
		t.Parallel()
		for _, c := range bodies {
			r, cookie := demoRouter(t, false)
			rec := demoDo(r, http.MethodPost, c.path, cookie, c.body)
			if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
				t.Errorf("POST %s outside demo = %d, want a success (body: %s)", c.path, rec.Code, rec.Body.String())
			}
		}
	})
}

func TestEveryResponseIsNoindexOnlyInDemoMode(t *testing.T) {
	t.Parallel()
	paths := []string{"/healthz", "/api/session", "/api/nope", "/", "/some/spa/route", "/assets/app.js", "/report/unknown-slug"}

	for _, demo := range []bool{true, false} {
		r, _ := demoRouter(t, demo)
		for _, p := range paths {
			got := demoDo(r, http.MethodGet, p, "", "").Header().Get("X-Robots-Tag")
			switch {
			case demo && got != "noindex, nofollow":
				t.Errorf("demo GET %s: X-Robots-Tag = %q, want noindex, nofollow", p, got)
			case !demo && p != "/report/unknown-slug" && got != "":
				t.Errorf("non-demo GET %s: X-Robots-Tag = %q, want none", p, got)
			}
		}
	}
}
