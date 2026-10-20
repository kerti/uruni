package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// seedE2EEmail and seedE2EPassword are the treasurer login every e2e spec
// signs in with. Not from the environment: the fixture is identical on every
// machine running `make e2e`, so a spec can hardcode them.
const (
	seedE2EEmail = "bendahara@e2e.uruni.test"
	//nolint:gosec // not a credential leak - a fixed, publicly known fixture password for a throwaway e2e database, not a real secret
	seedE2EPassword = "e2e-fixture-password"
)

// seedE2EFundName and the account/member/tier names below are invented
// placeholders, never a real person's or RT's name: the repo is public and
// the pre-commit PII guard scans only staged diffs, not what a fixture prints
// (CLAUDE.md). They read as ordinary Indonesian domain data (the treasurer's
// copy, ADR-014), unlike this file's English operator comments and errors.
const seedE2EFundName = "Kas RT Uji Coba"

// seedE2EEnvelope is the one envelope the fixture opens; report.spec.ts reads
// it on the public report.
const seedE2EEnvelope = "Amplop Uji Laporan"

// seedE2E migrates and seeds the database at URUNI_DB with a small, fixed
// fixture. It never resets: `make e2e-reset` deletes the file first, and on a
// database already seeded the treasurer's registration fails, so it refuses
// rather than seeding twice. The fixture: one fund, a cash and a bank
// account, a registered treasurer login, two members on one dues tier with a
// current rate, and an opening balance on the cash account; enough for
// golden-path.spec.ts to log in and see a fund.
//
// Deliberately not config.Load: that hard-refuses to boot without a valid
// URUNI_BASE_URL (ADR-019), right for `serve` and wrong for a throwaway
// seeding command with no HTTP origin. seed-e2e reads URUNI_DB directly, so
// `make e2e-reset` and a bare `URUNI_DB=... go run ./cmd/uruni seed-e2e` work
// with no .env.
func seedE2E(ctx context.Context) error {
	dbPath := strings.TrimSpace(os.Getenv("URUNI_DB"))
	if err := requireThrowawayDBPath(dbPath); err != nil {
		return err
	}

	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	// config.Config{}: LogLevel's zero value is slog.LevelInfo and LogFormat's is
	// not LogFormatJSON, so newLogger builds the plain text handler `serve` and
	// `migrate` use by default, without config.Load (see above).
	logger := newLogger(config.Config{}, os.Stderr)
	if _, err := db.Up(ctx, sqlDB, logger); err != nil {
		return fmt.Errorf("migrating %s: %w", dbPath, err)
	}

	l := ledger.New(sqlDB)
	q := store.New(sqlDB)
	au := auth.New(sqlDB)

	if _, err := au.Register(ctx, seedE2EEmail, seedE2EPassword); err != nil {
		return fmt.Errorf("registering the e2e treasurer login: %w", err)
	}

	// #230: the cash account's opening balance is born with it, inside
	// SetUpFund's transaction.
	occurredOn := time.Now().In(tz.Jakarta).Format("2006-01-02") // the treasurer's day, not the host's (#379)
	setup, err := l.SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: seedE2EFundName,
		Accounts: []ledger.AccountInput{
			{
				Kind: "cash", Name: "Tunai",
				OpeningBalance: &ledger.OpeningBalance{Amount: money.Amount(1_000_000), OccurredOn: occurredOn},
			},
			{Kind: "bank", Name: "Bank Uji Coba"},
		},
	})
	if err != nil {
		return fmt.Errorf("setting up the e2e fund: %w", err)
	}

	now := time.Now().Unix()
	tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{
		FundID: setup.Fund.ID, Name: "Reguler", CreatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("creating the e2e dues tier: %w", err)
	}
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
		TierID: tier.ID, Amount: money.Amount(50_000).Int64(), EffectiveFrom: "2024-01", CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("creating the e2e dues rate: %w", err)
	}

	joinedOn := "2024-01-01"
	tierID := tier.ID
	for _, name := range []string{"Warga Satu", "Warga Dua"} {
		if _, err := l.CreateMember(ctx, ledger.CreateMemberParams{
			FundID: setup.Fund.ID, Name: name, TierID: &tierID, JoinedOn: joinedOn,
		}); err != nil {
			return fmt.Errorf("creating e2e member %q: %w", name, err)
		}
	}

	// One open envelope with nothing in it, for report.spec.ts: the public
	// report lists open envelopes in their opening month. Empty, so no other
	// spec's balances change.
	if _, err := l.OpenIncidental(ctx, ledger.OpenIncidentalParams{
		FundID: setup.Fund.ID, Occasion: seedE2EEnvelope, OpenedOn: occurredOn,
	}); err != nil {
		return fmt.Errorf("opening the e2e envelope: %w", err)
	}

	return nil
}

// requireThrowawayDBPath is ADR-019's promise for seed-e2e: "Dev-only;
// refuses to run against a non-throwaway DB." The command deletes and
// recreates what it points at, so the bar for "looks throwaway" is narrow:
//
//   - URUNI_DB unset refuses outright. It never falls back to
//     config.DefaultDBPath ("./uruni.db") as `serve`/`migrate` do: a bare
//     `go run ./cmd/uruni seed-e2e` in a working copy would wipe the real dev
//     database.
//   - The path must resolve under the OS temp directory (matching both
//     os.TempDir() and the literal "/tmp" the Makefile's E2E_DB uses; on
//     macOS they differ, /tmp being a symlink into a per-user temp root).
//   - Its base filename must contain "e2e", so a stray temp file with another
//     name is still refused.
//
// Both together are what "/tmp/uruni-e2e.db" satisfies and "./uruni.db" (or
// any path under the repo) does not.
func requireThrowawayDBPath(dbPath string) error {
	if dbPath == "" {
		return fmt.Errorf("seed-e2e refuses to run: URUNI_DB is unset - point it at a throwaway path (e.g. %s) before seeding", e2eExampleDBPath)
	}

	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return fmt.Errorf("seed-e2e refuses to run: could not resolve URUNI_DB=%q: %w", dbPath, err)
	}

	if !underTempDir(abs) || !strings.Contains(strings.ToLower(filepath.Base(abs)), "e2e") {
		return fmt.Errorf("seed-e2e refuses to run against %q - it does not look like a throwaway e2e database (expected a path like %s under a temp directory, with \"e2e\" in the filename)", dbPath, e2eExampleDBPath)
	}
	return nil
}

// e2eExampleDBPath is named in refusal messages so an operator has something
// to copy: the Makefile's E2E_DB.
const e2eExampleDBPath = "/tmp/uruni-e2e.db"

// underTempDir reports whether abs sits inside a temp directory: os.TempDir()
// or the literal "/tmp" the Makefile hardcodes as E2E_DB (the same directory
// on Linux, not always on macOS, where /tmp is a symlink into a per-user root
// that os.TempDir() returns resolved). Both abs and each root go through
// resolveExistingPrefix, so a database file that does not exist yet (seed-e2e
// creates it) still compares correctly against a symlinked root.
func underTempDir(abs string) bool {
	resolvedAbs := resolveExistingPrefix(abs)
	for _, root := range []string{os.TempDir(), "/tmp"} {
		resolvedRoot := resolveExistingPrefix(root)
		if rel, err := filepath.Rel(resolvedRoot, resolvedAbs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveExistingPrefix returns path with symlinks resolved, walking up to the
// nearest existing ancestor when path does not exist (a seed target not yet
// created); filepath.EvalSymlinks alone fails on a missing path.
func resolveExistingPrefix(path string) string {
	clean := filepath.Clean(path)
	var suffix string
	for {
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			// Reached the root without anything resolving; return the original path
			// rather than loop forever.
			return path
		}
		suffix = filepath.Join(filepath.Base(clean), suffix)
		clean = parent
	}
}
