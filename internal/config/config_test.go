package config

import (
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Makefile exports .env into every `make test` run, so a test that only set
// the variable it cares about would read the developer's real PORT or base URL.
// env clears the whole table first, then applies the case's overrides - which
// also makes "unset" a value a test can assert on.
func env(t *testing.T, overrides map[string]string) {
	t.Helper()
	for _, name := range []string{
		"URUNI_DB", "PORT", "URUNI_BASE_URL", "URUNI_UPLOADS_DIR",
		"SMTP_URL", "URUNI_LOG_LEVEL", "URUNI_LOG_FORMAT",
	} {
		t.Setenv(name, "")
	}
	for name, value := range overrides {
		t.Setenv(name, value)
	}
}

// An origin every test that isn't *about* the base URL can lean on. Anything
// absolute and not the placeholder will do; `.test` is reserved by RFC 2606, so
// it can never become somebody's real instance.
const testBaseURL = "https://uruni.test"

func TestLoadDefaultsEverythingItCan(t *testing.T) {
	env(t, map[string]string{"URUNI_BASE_URL": testBaseURL})

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
	if cfg.UploadsDir != DefaultUploadsDir {
		t.Errorf("UploadsDir = %q, want %q", cfg.UploadsDir, DefaultUploadsDir)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != LogFormatText {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, LogFormatText)
	}
	// Optional, and unset here - emailed backups (M8) are the only thing that
	// needs it.
	if cfg.SMTPURL != "" {
		t.Errorf("SMTPURL = %q, want empty", cfg.SMTPURL)
	}
}

func TestLoadReadsEveryVariable(t *testing.T) {
	env(t, map[string]string{
		"URUNI_DB":          "/data/uruni.db",
		"PORT":              "8099",
		"URUNI_UPLOADS_DIR": "/uploads",
		"URUNI_BASE_URL":    testBaseURL + "/",
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
		DBPath:     "/data/uruni.db",
		Port:       8099,
		UploadsDir: "/uploads",
		// The trailing slash is trimmed so callers can join paths without
		// producing "https://host//report/xyz".
		BaseURL:   testBaseURL,
		SMTPURL:   "smtp://smtp.example.com:587",
		LogLevel:  slog.LevelDebug,
		LogFormat: LogFormatJSON,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		// mentions is the variable name the message must name - the operator's
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
			overrides := map[string]string{"URUNI_BASE_URL": testBaseURL}
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

// URUNI_BASE_URL is the one variable an operator must set, so it carries the
// "is this instance configured at all?" check: unset and the .env.example
// placeholder are both refusals, and both name the variable (#119, ADR-019).
func TestLoadRefusesAnUnconfiguredBaseURL(t *testing.T) {
	for _, base := range []string{"", placeholderBaseURL, placeholderBaseURL + "/"} {
		env(t, map[string]string{"URUNI_BASE_URL": base})

		_, err := Load()
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Load() with URUNI_BASE_URL=%q = %v, want ErrInvalidConfig", base, err)
		}
		if !strings.Contains(err.Error(), "URUNI_BASE_URL") {
			t.Errorf("Load() = %q, want it to name URUNI_BASE_URL", err)
		}
	}
}

// SMTP_URL carries a password: if it reached an error message it would reach
// the container logs, and from there an issue thread.
func TestLoadNeverEchoesACredential(t *testing.T) {
	// Invalid on its percent-escape, so it is rejected *as a URL* - the path
	// where echoing the value would be most tempting. Assembled from parts
	// rather than written as one literal so the fixture does not itself read as
	// a checked-in `scheme://user:pass@host` credential.
	const canary = "hunter2"
	smtp := "smtps://bendahara:" + canary + "@smtp.example.com:587/%zz"
	env(t, map[string]string{"URUNI_BASE_URL": testBaseURL, "SMTP_URL": smtp})
	if _, err := Load(); err == nil {
		t.Fatal("Load() with a malformed SMTP_URL = nil, want an error")
	} else if strings.Contains(err.Error(), canary) {
		t.Errorf("Load() leaked the SMTP password: %q", err)
	}
}

// ADR-004: SQLite is the only engine through 0.x, so DATABASE_URL must not be a
// variable the binary reacts to at all - not honoured, not warned about.
func TestLoadIgnoresDatabaseURL(t *testing.T) {
	env(t, map[string]string{"URUNI_BASE_URL": testBaseURL})
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/uruni")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.DBPath != DefaultDBPath {
		t.Errorf("DBPath = %q, want the SQLite default %q", cfg.DBPath, DefaultDBPath)
	}
}

// #153: the compose stack overrides URUNI_UPLOADS_DIR to the volume-mounted
// /uploads explicitly, the same way it does URUNI_DB - this is that override
// read through, on its own rather than folded into
// TestLoadReadsEveryVariable, since the default itself (a relative dev path)
// is worth its own defaults test above.
func TestLoadReadsUploadsDirOverride(t *testing.T) {
	env(t, map[string]string{"URUNI_BASE_URL": testBaseURL, "URUNI_UPLOADS_DIR": "/uploads"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.UploadsDir != "/uploads" {
		t.Errorf("UploadsDir = %q, want %q", cfg.UploadsDir, "/uploads")
	}
}

func TestEnsureUploadsDirWritableAcceptsAWritableDir(t *testing.T) {
	if err := EnsureUploadsDirWritable(t.TempDir()); err != nil {
		t.Errorf("EnsureUploadsDirWritable(writable temp dir) = %v, want nil", err)
	}
}

func TestEnsureUploadsDirWritableRefusesAMissingDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	err := EnsureUploadsDirWritable(missing)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("EnsureUploadsDirWritable(missing) = %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "URUNI_UPLOADS_DIR") {
		t.Errorf("EnsureUploadsDirWritable(missing) = %q, want it to name URUNI_UPLOADS_DIR", err)
	}
}

func TestEnsureUploadsDirWritableRefusesAFileNotADir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	err := EnsureUploadsDirWritable(file)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("EnsureUploadsDirWritable(a file) = %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "URUNI_UPLOADS_DIR") {
		t.Errorf("EnsureUploadsDirWritable(a file) = %q, want it to name URUNI_UPLOADS_DIR", err)
	}
}

// A directory with no write permission is the "seeded root:root" failure
// ADR-019's Dockerfile comment describes - skipped as root, since root
// ignores the permission bits this test relies on to fail the write.
func TestEnsureUploadsDirWritableRefusesAReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission bits do not apply")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // a directory needs its execute bit to be traversable at all; 0600 (gosec's default ceiling) would not be a directory mode
		t.Fatalf("chmod fixture dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restoring t.TempDir()'s own directory mode so it can clean up after itself

	err := EnsureUploadsDirWritable(dir)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("EnsureUploadsDirWritable(read-only dir) = %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "URUNI_UPLOADS_DIR") {
		t.Errorf("EnsureUploadsDirWritable(read-only dir) = %q, want it to name URUNI_UPLOADS_DIR", err)
	}
}
