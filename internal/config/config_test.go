package config

import (
	"errors"
	"log/slog"
	"maps"
	"strings"
	"testing"
)

// The Makefile exports .env into every `make test` run, so a test that only set
// the variable it cares about would read the developer's real PORT or secret.
// env clears the whole table first, then applies the case's overrides — which
// also makes "unset" a value a test can assert on.
func env(t *testing.T, overrides map[string]string) {
	t.Helper()
	for _, name := range []string{
		"URUNI_DB", "PORT", "URUNI_BASE_URL", "URUNI_SESSION_SECRET",
		"SMTP_URL", "URUNI_LOG_LEVEL", "URUNI_LOG_FORMAT",
	} {
		t.Setenv(name, "")
	}
	for name, value := range overrides {
		t.Setenv(name, value)
	}
}

// A secret every test that isn't *about* the secret can lean on. Built from the
// placeholder rather than written out, so no high-entropy string literal exists
// in the repo at all: Load only cares that the value is neither empty nor the
// placeholder, so a realistic-looking random one buys no coverage — and reads
// as a genuine leaked credential to gitleaks and gosec alike.
const testSecret = "not-" + placeholderSessionSecret

func TestLoadDefaultsEverythingItCan(t *testing.T) {
	env(t, map[string]string{"URUNI_SESSION_SECRET": testSecret})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.DBPath != DefaultDBPath {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, DefaultDBPath)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != LogFormatText {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, LogFormatText)
	}
	// Optional, and unset here — the report link (M7) and emailed backups (M8)
	// are the only things that need them.
	if cfg.BaseURL != "" || cfg.SMTPURL != "" {
		t.Errorf("BaseURL/SMTPURL = %q/%q, want both empty", cfg.BaseURL, cfg.SMTPURL)
	}
}

func TestLoadReadsEveryVariable(t *testing.T) {
	env(t, map[string]string{
		"URUNI_DB":             "/data/uruni.db",
		"PORT":                 "8099",
		"URUNI_BASE_URL":       "https://uruni.example.com/",
		"URUNI_SESSION_SECRET": testSecret,
		// Credential-free on purpose: this test is about every variable being
		// read through, and an SMTP URL needs no auth to prove that.
		"SMTP_URL":         "smtp://smtp.example.com:587",
		"URUNI_LOG_LEVEL":  "debug",
		"URUNI_LOG_FORMAT": "json",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	want := Config{
		DBPath: "/data/uruni.db",
		Port:   8099,
		// The trailing slash is trimmed so callers can join paths without
		// producing "https://host//report/xyz".
		BaseURL:       "https://uruni.example.com",
		SessionSecret: testSecret,
		SMTPURL:       "smtp://smtp.example.com:587",
		LogLevel:      slog.LevelDebug,
		LogFormat:     LogFormatJSON,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		// mentions is the variable name the message must name — the operator's
		// whole job on a boot failure is knowing which line of .env to fix.
		mentions string
	}{
		{"port not a number", map[string]string{"PORT": "eight thousand"}, "PORT"},
		{"port zero", map[string]string{"PORT": "0"}, "PORT"},
		{"port above range", map[string]string{"PORT": "70000"}, "PORT"},
		{"base url relative", map[string]string{"URUNI_BASE_URL": "/report"}, "URUNI_BASE_URL"},
		{"base url no scheme", map[string]string{"URUNI_BASE_URL": "uruni.example.com"}, "URUNI_BASE_URL"},
		{"smtp url wrong scheme", map[string]string{"SMTP_URL": "https://smtp.example.com"}, "SMTP_URL"},
		{"smtp url no host", map[string]string{"SMTP_URL": "smtp://"}, "SMTP_URL"},
		{"log level unknown", map[string]string{"URUNI_LOG_LEVEL": "chatty"}, "URUNI_LOG_LEVEL"},
		{"log format unknown", map[string]string{"URUNI_LOG_FORMAT": "xml"}, "URUNI_LOG_FORMAT"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overrides := map[string]string{"URUNI_SESSION_SECRET": testSecret}
			maps.Copy(overrides, tc.overrides)
			env(t, overrides)

			_, err := Load()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Load() = %v, want ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), tc.mentions) {
				t.Errorf("Load() = %q, want it to name %s", err, tc.mentions)
			}
		})
	}
}

func TestLoadRefusesAnUnusableSessionSecret(t *testing.T) {
	// Both halves of the same fault: nothing set, or the value .env.example
	// ships. Booting on either signs sessions with a value the whole internet
	// knows (ADR-019).
	for _, secret := range []string{"", placeholderSessionSecret} {
		env(t, map[string]string{"URUNI_SESSION_SECRET": secret})

		_, err := Load()
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Load() with URUNI_SESSION_SECRET=%q = %v, want ErrInvalidConfig", secret, err)
		}
		if !strings.Contains(err.Error(), "URUNI_SESSION_SECRET") {
			t.Errorf("Load() = %q, want it to name URUNI_SESSION_SECRET", err)
		}
	}
}

// A wrong secret is still a secret: if it reached an error message it would
// reach the container logs, and from there an issue thread. Same for SMTP_URL,
// which carries a password.
func TestLoadNeverEchoesACredential(t *testing.T) {
	// A secret that is set but wrong still fails somewhere downstream; whatever
	// the message is, it must not be the value. Assembled, not written out, for
	// the same reason as testSecret.
	const wrongSecret = "wrong-" + placeholderSessionSecret
	env(t, map[string]string{"URUNI_SESSION_SECRET": wrongSecret, "SMTP_URL": "not a url"})
	if _, err := Load(); err != nil && strings.Contains(err.Error(), wrongSecret) {
		t.Errorf("Load() leaked the session secret: %q", err)
	}

	// Invalid on its percent-escape, so it is rejected *as a URL* — the path
	// where echoing the value would be most tempting. Assembled from parts
	// rather than written as one literal so the fixture does not itself read as
	// a checked-in `scheme://user:pass@host` credential.
	const canary = "hunter2"
	smtp := "smtps://bendahara:" + canary + "@smtp.example.com:587/%zz"
	env(t, map[string]string{"URUNI_SESSION_SECRET": testSecret, "SMTP_URL": smtp})
	if _, err := Load(); err == nil {
		t.Fatal("Load() with a malformed SMTP_URL = nil, want an error")
	} else if strings.Contains(err.Error(), canary) {
		t.Errorf("Load() leaked the SMTP password: %q", err)
	}
}

// ADR-004: SQLite is the only engine through 0.x, so DATABASE_URL must not be a
// variable the binary reacts to at all — not honoured, not warned about.
func TestLoadIgnoresDatabaseURL(t *testing.T) {
	env(t, map[string]string{"URUNI_SESSION_SECRET": testSecret})
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/uruni")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.DBPath != DefaultDBPath {
		t.Errorf("DBPath = %q, want the SQLite default %q", cfg.DBPath, DefaultDBPath)
	}
}
