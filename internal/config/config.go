// Package config reads the binary's runtime configuration from the
// environment. Environment variables only: no config file and no third-party
// config library (ADR-019).
//
// This is the only place in the binary that calls os.Getenv; every other
// package takes a Config, so ADR-019's table is checkable against one file.
//
// Errors here are operator-facing, so English like the CLI and self-hosting
// docs; Indonesian is the treasurer's surface, the SPA and public report
// (ADR-014).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Defaults for the variables that have one; URUNI_BASE_URL is required.
const (
	DefaultDBPath    = "./uruni.db"
	DefaultPort      = 8080
	DefaultLogLevel  = slog.LevelInfo
	DefaultLogFormat = LogFormatText
	// DefaultUploadsDir is a dev-friendly relative default that the production
	// image and docker-compose.yml override to the volume-mounted /uploads
	// (ADR-019, ADR-011), as URUNI_DB defaults to ./uruni.db and becomes
	// /data/uruni.db in compose.
	DefaultUploadsDir = "./uploads"
	// DefaultBackupDir is the same split for the daily dumps (ADR-012/ADR-013):
	// a relative dev default, overridden to /backups in compose and the image.
	DefaultBackupDir = "./backups"
)

// Log output formats. Text is the default: operators read container logs with
// their eyes far more than with a shipper (ADR-022).
const (
	LogFormatText = "text"
	LogFormatJSON = "json"
)

// placeholderBaseURL is the value .env.example ships. Refusing it makes
// URUNI_BASE_URL the "did you configure this at all?" gate: booting on the
// template value yields a report link that works nowhere but the operator's
// browser (ADR-019).
const placeholderBaseURL = "https://uruni.example.com"

// ErrInvalidConfig wraps every configuration failure, so callers and tests
// branch on identity, not message text. The message names the variable at
// fault.
var ErrInvalidConfig = errors.New("invalid configuration")

// Config is the resolved runtime configuration: a value passed down from
// main, with no package-level singleton.
type Config struct {
	// DBPath is the SQLite file. SQLite is the only engine through 0.x, so there
	// is no DATABASE_URL (ADR-004).
	DBPath string
	// Port is the TCP port the server listens on.
	Port int
	// BaseURL is this instance's public origin. It builds the shareable report
	// link and its scheme decides whether the session cookie is Secure (ADR-007).
	// Required: an instance with no origin is unconfigured.
	BaseURL string
	// UploadsDir is the local volume receipt photos are written to (ADR-011).
	// Load does no filesystem I/O; see EnsureUploadsDirWritable, which `serve`
	// alone calls at boot.
	UploadsDir string
	// BackupDir is the local volume the daily dumps are written to
	// (ADR-012/ADR-013); not checked here either, see EnsureBackupDirWritable.
	BackupDir string
	// LogLevel and LogFormat configure the slog handler main builds (ADR-022).
	LogLevel  slog.Level
	LogFormat string
	// TrustedProxies are the peers whose X-Forwarded-For is believed (#447): the
	// login and restore-confirm rate limiters key on the client address, and a
	// header from anyone else is chosen by the client. Empty, the default,
	// ignores the header and keys on the TCP peer. docker-compose.yml sets it so
	// the shipped Caddy is trusted.
	TrustedProxies []netip.Prefix
}

// Load reads and validates the whole environment table in ADR-019. It returns
// the first problem found, not a list: an operator fixes one variable and
// re-runs.
func Load() (Config, error) {
	cfg := Config{
		DBPath:     DefaultDBPath,
		Port:       DefaultPort,
		UploadsDir: DefaultUploadsDir,
		BackupDir:  DefaultBackupDir,
		BaseURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("URUNI_BASE_URL")), "/"),
	}

	if v := strings.TrimSpace(os.Getenv("URUNI_DB")); v != "" {
		cfg.DBPath = v
	}

	if v := strings.TrimSpace(os.Getenv("URUNI_UPLOADS_DIR")); v != "" {
		cfg.UploadsDir = v
	}

	if v := strings.TrimSpace(os.Getenv("URUNI_BACKUP_DIR")); v != "" {
		cfg.BackupDir = v
	}

	if err := loadPort(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadBaseURL(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadLogging(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadTrustedProxies(&cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// loadTrustedProxies reads URUNI_TRUSTED_PROXIES: a comma-separated list of
// CIDR ranges or bare addresses. A bare address is its own single-host range.
func loadTrustedProxies(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("URUNI_TRUSTED_PROXIES"))
	if raw == "" {
		return nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			addr, addrErr := netip.ParseAddr(part)
			if addrErr != nil {
				return invalidValue("URUNI_TRUSTED_PROXIES", raw,
					"want comma-separated CIDR ranges or addresses, e.g. 172.16.0.0/12,10.0.0.0/8")
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, prefix.Masked())
	}
	return nil
}

func loadPort(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("PORT"))
	if raw == "" {
		return nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return invalidValue("PORT", raw, "want a TCP port between 1 and 65535")
	}
	cfg.Port = port
	return nil
}

func loadBaseURL(cfg *Config) error {
	switch cfg.BaseURL {
	case "":
		return invalid("URUNI_BASE_URL",
			"not set - the public origin this instance is reached at, e.g. https://uruni.example.com; `make setup` writes a loopback one for local dev")
	case placeholderBaseURL:
		// Safe to echo: an origin is not a credential, and the quoted template value
		// makes the fault obvious.
		return invalidValue("URUNI_BASE_URL", cfg.BaseURL,
			"still the placeholder from .env.example; set the origin this instance is actually reached at")
	}
	u, err := url.Parse(cfg.BaseURL)
	// The report link is pasted into a WhatsApp group, so a relative or
	// scheme-less value would work nowhere but the operator's browser.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidValue("URUNI_BASE_URL", cfg.BaseURL,
			"want an absolute origin, e.g. https://uruni.example.com")
	}
	return nil
}

func loadLogging(cfg *Config) error {
	cfg.LogLevel = DefaultLogLevel
	if raw := strings.TrimSpace(os.Getenv("URUNI_LOG_LEVEL")); raw != "" {
		// slog.Level parses debug/info/warn/error itself, case-insensitively.
		if err := cfg.LogLevel.UnmarshalText([]byte(raw)); err != nil {
			return invalidValue("URUNI_LOG_LEVEL", raw, "want debug, info, warn or error")
		}
	}

	cfg.LogFormat = DefaultLogFormat
	if raw := strings.TrimSpace(os.Getenv("URUNI_LOG_FORMAT")); raw != "" {
		format := strings.ToLower(raw)
		if format != LogFormatText && format != LogFormatJSON {
			return invalidValue("URUNI_LOG_FORMAT", raw, "want text or json")
		}
		cfg.LogFormat = format
	}

	return nil
}

// EnsureUploadsDirWritable checks that dir (Config.UploadsDir) exists, is a
// directory, and accepts a new file: the boot check `serve` alone runs, not
// `migrate`, `create-user`, `healthcheck` or `seed-e2e`, which write no
// receipt. Kept out of Load, which does no filesystem I/O.
//
// A missing or unwritable directory is the "root:root named volume" failure
// ADR-019's Dockerfile comment describes for /data and /uploads; this turns it
// into a clear boot error instead of the first upload failing silently.
func EnsureUploadsDirWritable(dir string) error {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return invalidValue("URUNI_UPLOADS_DIR", dir,
			"does not exist - create it (or point at an existing writable directory) before starting the server")
	case err != nil:
		return invalidValue("URUNI_UPLOADS_DIR", dir, "could not be read: "+err.Error())
	case !info.IsDir():
		return invalidValue("URUNI_UPLOADS_DIR", dir, "is not a directory")
	}

	probe, err := os.CreateTemp(dir, ".uruni-write-check-*")
	if err != nil {
		return invalidValue("URUNI_UPLOADS_DIR", dir, "is not writable: "+err.Error())
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// EnsureBackupDirWritable is EnsureUploadsDirWritable for URUNI_BACKUP_DIR,
// checked at boot for `serve` only: the same "root:root named volume" failure,
// for the daily dump directory.
func EnsureBackupDirWritable(dir string) error {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return invalidValue("URUNI_BACKUP_DIR", dir,
			"does not exist - create it (or point at an existing writable directory) before starting the server")
	case err != nil:
		return invalidValue("URUNI_BACKUP_DIR", dir, "could not be read: "+err.Error())
	case !info.IsDir():
		return invalidValue("URUNI_BACKUP_DIR", dir, "is not a directory")
	}

	probe, err := os.CreateTemp(dir, ".uruni-write-check-*")
	if err != nil {
		return invalidValue("URUNI_BACKUP_DIR", dir, "is not writable: "+err.Error())
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// invalid reports a bad variable without repeating its value: for the unset
// case, and for any variable that carries a credential. Anything printed here
// can end up in a container log pasted into an issue.
func invalid(name, why string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidConfig, name, why)
}

// invalidValue is invalid for variables whose value is safe to echo; showing
// what was read turns a puzzle into a one-minute fix.
func invalidValue(name, value, why string) error {
	return fmt.Errorf("%w: %s=%q - %s", ErrInvalidConfig, name, value, why)
}
