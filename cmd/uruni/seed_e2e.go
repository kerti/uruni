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
)

// seedE2EEmail and seedE2EPassword are the treasurer login every e2e spec
// signs in with. Not read from the environment: the fixture is meant to be
// identical on every machine that runs `make e2e`, so a spec can hardcode
// them too rather than plumbing them through as config.
const (
	seedE2EEmail = "bendahara@e2e.uruni.test"
	//nolint:gosec // not a credential leak - a fixed, publicly known fixture password for a throwaway e2e database, not a real secret
	seedE2EPassword = "e2e-fixture-password"
)

// seedE2EFundName and the account/member/tier names below are deliberately
// invented placeholders, never a real person's or RT's name - the repo is
// public and the pre-commit PII guard only scans staged diffs, not what a
// fixture prints (CLAUDE.md). They read as ordinary Indonesian domain data
// (fund/account/member names are the treasurer's copy, ADR-014), unlike this
// file's own comments and errors, which are the CLI's English operator
// surface.
const seedE2EFundName = "Kas RT Uji Coba"

// seedE2E resets, migrates and seeds the database at URUNI_DB with a small,
// fixed fixture: one fund, a cash and a bank account, a registered treasurer
// login, two members on one dues tier with a current rate, and an opening
// balance on the cash account. That is exactly enough for golden-path.spec.ts
// to have a treasurer to log in as and a fund to look at once M6.4 onward
// give the SPA screens to click through.
//
// Deliberately NOT config.Load: that loader hard-refuses to boot without a
// valid URUNI_BASE_URL (ADR-019 — "did you configure this instance at all?"),
// which is the right gate for `serve` and wrong for a throwaway seeding
// command that has no HTTP origin to build links from. seed-e2e reads
// URUNI_DB directly instead, so `make e2e-reset` (and a bare `URUNI_DB=...
// go run ./cmd/uruni seed-e2e` in a shell with no .env at all) both work.
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

	// config.Config{} (its zero value): LogLevel's zero value is slog.LevelInfo
	// and LogFormat's zero value is not LogFormatJSON, so newLogger builds the
	// same plain text handler `serve`/`migrate` use by default - built directly
	// rather than via config.Load, which this command deliberately skips (see
	// the doc comment above).
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

	setup, err := l.SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: seedE2EFundName,
		Accounts: []ledger.AccountInput{
			{Kind: "cash", Name: "Tunai"},
			{Kind: "bank", Name: "Bank Uji Coba"},
		},
	})
	if err != nil {
		return fmt.Errorf("setting up the e2e fund: %w", err)
	}
	cashAccount := setup.Accounts[0]

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
		if _, err := q.CreateMember(ctx, store.CreateMemberParams{
			FundID: setup.Fund.ID, Name: name, TierID: &tierID, JoinedOn: &joinedOn, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("creating e2e member %q: %w", name, err)
		}
	}

	occurredOn := time.Now().Format("2006-01-02")
	if _, err := l.PostOpeningBalance(ctx, ledger.PostOpeningBalanceParams{
		FundID: setup.Fund.ID, AccountID: cashAccount.ID, PurposeID: setup.MainPurposeID,
		Amount: money.Amount(1_000_000), OccurredOn: occurredOn,
	}); err != nil {
		return fmt.Errorf("posting the e2e opening balance: %w", err)
	}

	return nil
}

// requireThrowawayDBPath is ADR-019's own promise for seed-e2e: "Dev-only;
// refuses to run against a non-throwaway DB." This command deletes and
// recreates whatever it points at, so the bar for "looks throwaway" is
// deliberately narrow rather than merely "not the default":
//
//   - URUNI_DB unset refuses outright. It never falls back to
//     config.DefaultDBPath ("./uruni.db") the way `serve`/`migrate` do -
//     falling back here would make a bare `go run ./cmd/uruni seed-e2e` in a
//     developer's working copy silently wipe their real dev database.
//   - The path must resolve under the OS temp directory (matching both
//     os.TempDir() and the literal "/tmp" the Makefile's own E2E_DB uses -
//     on macOS the two differ, since /tmp is a symlink into a per-user temp
//     root that os.TempDir() also returns).
//   - Its base filename must contain "e2e", so a stray temp file with some
//     other name is still refused.
//
// Both conditions together are what "/tmp/uruni-e2e.db" satisfies and
// "./uruni.db" (or any path under the repo) does not.
func requireThrowawayDBPath(dbPath string) error {
	if dbPath == "" {
		return fmt.Errorf("seed-e2e refuses to run: URUNI_DB is unset — point it at a throwaway path (e.g. %s) before seeding", e2eExampleDBPath)
	}

	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return fmt.Errorf("seed-e2e refuses to run: could not resolve URUNI_DB=%q: %w", dbPath, err)
	}

	if !underTempDir(abs) || !strings.Contains(strings.ToLower(filepath.Base(abs)), "e2e") {
		return fmt.Errorf("seed-e2e refuses to run against %q — it does not look like a throwaway e2e database (expected a path like %s under a temp directory, with \"e2e\" in the filename)", dbPath, e2eExampleDBPath)
	}
	return nil
}

// e2eExampleDBPath is named in refusal messages so an operator has something
// concrete to copy - the Makefile's own E2E_DB.
const e2eExampleDBPath = "/tmp/uruni-e2e.db"

// underTempDir reports whether abs sits inside a temp directory: os.TempDir()
// itself, or the literal "/tmp" the Makefile hardcodes as E2E_DB - the two
// are the same directory on Linux but not always on macOS, where /tmp is a
// symlink into a per-user root (/private/var/folders/...) that os.TempDir()
// already returns resolved. Both abs and each root are resolved through
// resolveExistingPrefix first, so a database file that does not exist yet
// (the common case - seed-e2e is what creates it) still compares correctly
// against a symlinked root.
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

// resolveExistingPrefix returns path with symlinks resolved, walking up to
// the nearest ancestor that actually exists when path itself does not (a
// seed target the command has not created yet) - filepath.EvalSymlinks alone
// fails outright on a path that doesn't exist.
func resolveExistingPrefix(path string) string {
	clean := filepath.Clean(path)
	var suffix string
	for {
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			// Reached the root without finding anything that resolves; return
			// the original, uneval'd path rather than looping forever.
			return path
		}
		suffix = filepath.Join(filepath.Base(clean), suffix)
		clean = parent
	}
}
