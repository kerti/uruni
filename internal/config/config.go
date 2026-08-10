// Package config reads the binary's runtime configuration from the
// environment. Environment variables only — there is no config file, and no
// third-party config library (ADR-019).
//
// This package is the *only* place in the binary that calls os.Getenv. Every
// other package takes a Config. That keeps the documented table in ADR-019
// checkable against one file rather than against a grep of the whole tree.
//
// Errors here are operator-facing, so they are in English like the rest of the
// CLI and the self-hosting docs; Indonesian is the treasurer's surface, the SPA
// and the public report (ADR-014).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Defaults for the variables that have one. The rest are either optional
// (URUNI_BASE_URL, SMTP_URL) or required outright (URUNI_SESSION_SECRET).
const (
	DefaultDBPath    = "./uruni.db"
	DefaultPort      = 8080
	DefaultLogLevel  = slog.LevelInfo
	DefaultLogFormat = LogFormatText
)

// Log output formats. Text is the default because the operator reads container
// logs with their eyes far more often than with a log shipper (ADR-022).
const (
	LogFormatText = "text"
	LogFormatJSON = "json"
)

// placeholderSessionSecret is the value .env.example ships. Booting on it would
// mean every self-hosted instance signs sessions with a public constant, so the
// server refuses to start rather than quietly accepting it (ADR-019).
const placeholderSessionSecret = "change-me"

// ErrInvalidConfig wraps every configuration failure, so callers and tests can
// branch on identity rather than on message text. The message names the
// variable at fault; which one it is, is what the operator needs to know.
var ErrInvalidConfig = errors.New("invalid configuration")

// Config is the resolved runtime configuration. It is a value, passed down from
// main — there is no package-level singleton to reach for.
type Config struct {
	// DBPath is the SQLite file. SQLite is the only engine through 0.x, so
	// there is no DATABASE_URL to parse (ADR-004).
	DBPath string
	// Port is the TCP port the server listens on.
	Port int
	// BaseURL is this instance's public origin, used to build the shareable
	// report link (M7). Empty until an operator sets one.
	BaseURL string
	// SessionSecret signs session cookies (M5). Required, and never logged.
	SessionSecret string
	// SMTPURL is optional, for emailed backups. Validated here, used at M8
	// (ADR-012). Contains a password, so it is never echoed in an error.
	SMTPURL string
	// LogLevel and LogFormat configure the slog handler main builds (ADR-022).
	LogLevel  slog.Level
	LogFormat string
}

// Load reads and validates the whole environment table in ADR-019. It returns
// the first problem it finds rather than a list: an operator fixes one variable
// and re-runs, and a wall of errors on boot reads worse than one line.
func Load() (Config, error) {
	cfg := Config{
		DBPath:  DefaultDBPath,
		Port:    DefaultPort,
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("URUNI_BASE_URL")), "/"),
	}

	if v := strings.TrimSpace(os.Getenv("URUNI_DB")); v != "" {
		cfg.DBPath = v
	}

	if err := loadPort(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadSessionSecret(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadBaseURL(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadSMTPURL(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadLogging(&cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
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

func loadSessionSecret(cfg *Config) error {
	secret := strings.TrimSpace(os.Getenv("URUNI_SESSION_SECRET"))
	switch secret {
	case "":
		return invalid("URUNI_SESSION_SECRET",
			"not set — generate one with `openssl rand -base64 48`, or run `make setup`")
	case placeholderSessionSecret:
		return invalid("URUNI_SESSION_SECRET",
			"still the placeholder from .env.example — generate a real one with `openssl rand -base64 48`")
	}
	cfg.SessionSecret = secret
	return nil
}

func loadBaseURL(cfg *Config) error {
	if cfg.BaseURL == "" {
		return nil
	}
	u, err := url.Parse(cfg.BaseURL)
	// The report link is pasted into a WhatsApp group, so a relative or
	// scheme-less value would produce a link that works nowhere but the
	// operator's own browser.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidValue("URUNI_BASE_URL", cfg.BaseURL,
			"want an absolute origin, e.g. https://uruni.example.com")
	}
	return nil
}

func loadSMTPURL(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("SMTP_URL"))
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Host == "" {
		// Deliberately does not echo the value: it carries the SMTP password.
		return invalid("SMTP_URL", "want smtp://user:pass@host:port (or smtps://)")
	}
	cfg.SMTPURL = raw
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

// invalid reports a bad variable without repeating its value — for secrets and
// for URLs that carry credentials. Anything printed here can end up in a
// container log the operator pastes into an issue.
func invalid(name, why string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidConfig, name, why)
}

// invalidValue is the same, for variables whose value is safe to echo. Showing
// what was actually read is the difference between a one-minute fix and a
// puzzle, so use it wherever the value cannot be a credential.
func invalidValue(name, value, why string) error {
	return fmt.Errorf("%w: %s=%q — %s", ErrInvalidConfig, name, value, why)
}
