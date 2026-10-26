// Package demo is the public demo's fixture (ADR-045): the fund, the treasurer
// login and the report slug that `serve` writes into an empty database when
// URUNI_DEMO is on, and the guard that keeps the setting off a real fund.
//
// It writes through the real ledger and auth packages and inserts nothing past
// them, so what the demo shows is what the product does. The fixture is written
// to an empty database at every boot (a Fly machine's filesystem is rebuilt from
// the image at every start), so there is no upgrade path and no state to keep.
//
// Everything here is public on purpose: the email, the password and the report
// slug are in the repository and on the login screen. They guard a fund of
// invented data that is wiped within the hour.
package demo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// The demo's fixed identity (ADR-045 decisions 2 and 5). The email sits under
// the reserved .test domain, like seed-e2e's. The password clears the
// password_too_common denylist (a test holds it to that).
const (
	Email = "demo@uruni.test"
	//nolint:gosec // not a credential leak - the demo's shared login is public by design, printed on its own login screen (ADR-045)
	Password = "coba-uruni"

	// FundName is the demo fund, invented: who the people are, not "kas" or
	// "contoh".
	FundName = "CS Shift Sore"

	// ReportSlug is fixed so the README can link to the demo's public report. It is
	// base62 and at least the schema's 22 characters, like every slug; SetUpFund
	// holds it to that.
	ReportSlug = "CsShiftSoreDemoUruni2026"
)

// ErrForeignData is what Ensure returns for a database that is not an empty one
// or an earlier boot of this demo. Its message names URUNI_DEMO, because the
// operator's fix is unsetting it (or pointing URUNI_DB somewhere empty).
var ErrForeignData = errors.New("URUNI_DEMO is set, but this database already holds a login or fund that is not the demo's; refusing to boot on it - unset URUNI_DEMO, or point URUNI_DB at an empty file")

// Ensure is the whole of the demo's boot step, run after migrations and before
// the server listens:
//
//   - no login: the database is empty, so the fixture is written;
//   - one login, and it is the demo's: an earlier boot already wrote it, so
//     nothing happens (no reseed);
//   - any other login, or a fund with no login: someone's real data, so
//     ErrForeignData.
//
// uploadsDir is the receipt volume the fixture's few receipt photos are written
// to, the one `serve` already proved writable. now is the clock the fixture is
// dated by; production passes time.Now.
func Ensure(ctx context.Context, sqlDB *sql.DB, uploadsDir string, now func() time.Time) error {
	q := store.New(sqlDB)
	users, err := q.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("reading the logins: %w", err)
	}
	for _, u := range users {
		if auth.NormalizeEmail(u.Email) != Email {
			return ErrForeignData
		}
	}
	if len(users) > 0 {
		return nil
	}

	funds, err := q.ListFunds(ctx)
	if err != nil {
		return fmt.Errorf("reading the funds: %w", err)
	}
	if len(funds) > 0 {
		return ErrForeignData
	}
	return seed(ctx, sqlDB, uploadsDir, now)
}

// seed writes the demo fixture into an empty database: the fund set up at the
// start of the window (M-13) with opening balances, fourteen months of history
// replayed through the real ledger, and then the login. The login is registered
// last: "the demo login exists" then means "the fixture was written", which is
// what Ensure reads it as.
func seed(ctx context.Context, sqlDB *sql.DB, uploadsDir string, now func() time.Time) error {
	cal, err := newCalendar(now())
	if err != nil {
		return fmt.Errorf("reading the fund's zone: %w", err)
	}
	// ledgerAt is the real ledger with its clock stood on a chosen instant, so
	// every entry passes the same date checks live use does (ADR-045 decision 5).
	ledgerAt := func(t time.Time) *ledger.Ledger {
		return ledger.New(sqlDB, ledger.WithClock(func() time.Time { return t }))
	}

	opened := cal.date(firstMonth, 1)
	setup, err := ledgerAt(cal.instant(firstMonth, 1)).SetUpFund(ctx, ledger.SetUpFundParams{
		FundName:   FundName,
		TimeZone:   tz.Jakarta,
		ReportSlug: ReportSlug,
		Accounts: []ledger.AccountInput{
			{Kind: ledger.AccountKindCash, Name: "Tunai", OpeningBalance: &ledger.OpeningBalance{Amount: openingCash, OccurredOn: opened}},
			{Kind: ledger.AccountKindBank, Name: "Rekening Bank", OpeningBalance: &ledger.OpeningBalance{Amount: openingBank, OccurredOn: opened}},
		},
	})
	if err != nil {
		return fmt.Errorf("setting up the demo fund: %w", err)
	}

	if err := replayHistory(ctx, ledgerAt, cal, setup, uploadsDir); err != nil {
		return fmt.Errorf("replaying the demo history: %w", err)
	}

	if _, err := auth.New(sqlDB).Register(ctx, Email, Password); err != nil {
		return fmt.Errorf("registering the demo login: %w", err)
	}
	return nil
}
