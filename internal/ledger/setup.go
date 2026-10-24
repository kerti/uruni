package ledger

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// reportSlugAlphabet is base62: digits and both cases, so the slug drops into
// a URL path with no escaping.
const reportSlugAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// reportSlugLength is 32. The schema's CHECK (length(report_slug) >= 22) sets
// a floor of roughly a UUID's entropy in base62, but PRD section 7.9 treats
// the slug as a security-relevant unguessable token that, once shared,
// changes only through ReplaceReportSlug (the treasurer's deliberate act). 32
// base62 characters is about 190 bits, past the floor and a UUID's 122 bits,
// at the free cost of a longer URL nobody types.
const reportSlugLength = 32

// randInt is crypto/rand.Int, indirected so tests can drive the failure
// below. A failing random source is not a real condition, but this is the one
// call in setup whose silent failure would matter: the link is only
// unguessable if this call produced what it claims. The test that stubs this
// proves setup aborts rather than falling back to something weaker.
var randInt = rand.Int

// generateReportSlug returns a random base62 string for fund.report_slug,
// drawn from crypto/rand, never math/rand: PRD section 7.9 relies on it being
// unguessable, and math/rand's default source is neither seeded for that nor
// safe against an adversary who can observe its other output.
//
// rand.Int is used per character, not raw bytes reduced mod 62, which would
// slightly bias the low characters (256 is not a multiple of 62); rand.Int's
// rejection sampling has no such bias, and this runs once per fund.
//
// No collision retry: at this length a collision is far less likely than a
// hardware fault, and the schema's UNIQUE on report_slug is the backstop;
// CreateFund surfaces it as an ordinary unique-violation.
func generateReportSlug() (string, error) {
	alphabetSize := big.NewInt(int64(len(reportSlugAlphabet)))
	b := make([]byte, reportSlugLength)
	for i := range b {
		n, err := randInt(rand.Reader, alphabetSize)
		if err != nil {
			return "", fmt.Errorf("generating report slug: %w", err)
		}
		b[i] = reportSlugAlphabet[n.Int64()]
	}
	return string(b), nil
}

// AccountInput is one location the treasurer names at setup (#78): the wizard
// lets her choose and name each one, rather than SetUpFund fixing a
// cash/bank pair.
type AccountInput struct {
	Kind string // "cash" or "bank" - the schema's own CHECK (kind IN (...)) is the single source of truth for the set
	Name string // non-empty after trimming - the schema's own CHECK (length(trim(name)) > 0) refuses it, not pre-validated here (ADR-027)

	// OpeningBalance is this account's starting figure, optional (#230): a
	// location and its opening balance are born together in this withTx, or not
	// at all. nil is the same as Amount 0; neither posts a row.
	OpeningBalance *OpeningBalance
}

// SetUpFundParams is every argument SetUpFund needs to bring a brand-new
// fund into existence.
type SetUpFundParams struct {
	// FundName is the treasurer's name for the fund (PRD section 7.1: "name the
	// fund"). Non-empty after trimming.
	FundName string

	// TimeZone is the fund's time zone (ADR-041), one of tz.Names(); empty means
	// tz.Default. Any other name is ErrInvalidArgument, because the opening
	// balances' ceiling cannot be read without it.
	TimeZone string

	// Accounts is every location the fund starts with (#78). At least one is
	// required: a fund with no accounts could record nothing (every transaction
	// needs an account_id).
	Accounts []AccountInput

	// Now is the clock the opening balances' ceiling is read from, in TimeZone
	// (ADR-041): the fund row does not exist yet, so the zone comes from here.
	// Zero means time.Now().
	Now time.Time
}

// SetUpFundResult names every row the caller needs immediately: the whole
// fund row (it carries report_slug, which the public report needs), the main
// purpose id, and every created account as full rows, in the order Accounts
// was given.
type SetUpFundResult struct {
	Fund          store.Fund
	MainPurposeID int64
	Accounts      []store.Account
}

// SetUpFund writes the rows a fund cannot function without - the fund, its
// kind='main' purpose, and every account the treasurer asked for - inside one
// withTx, and returns their ids.
//
// The atomic core is this small and no smaller. A transaction needs both an
// account_id and a purpose_id (schema FKs), so a fund with no main purpose
// and no accounts could not record its first entry; a crash partway would
// leave a fund that exists, appears in ListFunds, and has nowhere to post, the
// orphan shape #42's OpenIncidental closed for a purpose without an
// incidental row. N accounts (#78) are still written in the same transaction
// as the fund and main purpose.
//
// Opening balances are folded in, one per account, in this withTx (#230: a
// location and its opening balance are born together, or not at all). That is
// safe, unlike members, dues tiers and dues rates, because an opening
// balance's failures are refused before withTx opens (validateOpeningBalance
// rejects a negative amount or malformed date on every account first), and a
// location born without one has no later standalone way to gain one. Members,
// tiers and rates are not here: a member is written with its own first tier
// row by CreateMember (ADR-040), and tiers/rates have ordinary unique
// constraints a create-then-retry handles with a 409. Folding them in would
// let one mistyped tier reference abort a good fund and its accounts. The
// caller composes them afterward as retriable calls.
//
// A second run is refused. The pre-check runs in this withTx and returns
// ErrFundAlreadyExists the moment a fund is found (see that sentinel for why
// this guard alone has no unique index).
func (l *Ledger) SetUpFund(ctx context.Context, p SetUpFundParams) (SetUpFundResult, error) {
	name := strings.TrimSpace(p.FundName)
	if name == "" {
		return SetUpFundResult{}, invalidField("name", "fund name must not be empty")
	}
	if len(p.Accounts) == 0 {
		return SetUpFundResult{}, invalidField("accounts", "at least one account is required")
	}
	timeZone := p.TimeZone
	if timeZone == "" {
		timeZone = tz.Default
	}
	loc, err := tz.Load(timeZone)
	if err != nil {
		return SetUpFundResult{}, invalidField("time_zone", "time_zone: %v", err)
	}
	window := windowAt(p.Now, loc)
	// Every opening balance is validated before withTx opens: a refused one
	// anywhere in the batch must leave no fund and no account behind.
	for _, a := range p.Accounts {
		if a.OpeningBalance != nil {
			if err := validateOpeningBalance(*a.OpeningBalance, window.entry()); err != nil {
				// The same check as POST /api/accounts, one level deeper on the wire.
				var fe *FieldError
				if errors.As(err, &fe) {
					fe.Field = "accounts." + fe.Field
				}
				return SetUpFundResult{}, err
			}
		}
	}

	var result SetUpFundResult
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		existing, err := q.ListFunds(ctx)
		if err != nil {
			return fmt.Errorf("checking for an existing fund: %w", err)
		}
		if len(existing) > 0 {
			return ErrFundAlreadyExists
		}

		slug, err := generateReportSlug()
		if err != nil {
			return err
		}

		now := time.Now().Unix()

		fund, err := q.CreateFund(ctx, store.CreateFundParams{
			Name: name, Currency: "IDR", TimeZone: timeZone, ReportSlug: slug, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating fund: %w", err)
		}

		// "Kas Utama" is the domain's name for the routine purpose (CONTEXT.md,
		// ADR-014), not user input, so SetUpFundParams has no field for it.
		mainPurpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
			FundID: fund.ID, Kind: "main", Name: "Kas Utama", CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating main purpose: %w", err)
		}

		// #78: the treasurer chooses and names each account. Kind and name shape
		// (CHECK (kind IN ('cash','bank')), CHECK (length(trim(name)) > 0)) are the
		// schema's to refuse (ADR-027); a bad row aborts the whole withTx, as the
		// purpose-insert failure above does.
		accounts := make([]store.Account, 0, len(p.Accounts))
		for _, a := range p.Accounts {
			account, err := q.CreateAccount(ctx, store.CreateAccountParams{
				FundID: fund.ID, Kind: a.Kind, Name: a.Name, CreatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("creating account %q: %w", a.Name, err)
			}

			if a.OpeningBalance != nil && a.OpeningBalance.Amount > 0 {
				if _, err := postOpeningBalanceRow(ctx, q, fund.ID, account.ID, mainPurpose.ID, *a.OpeningBalance, now); err != nil {
					return fmt.Errorf("posting opening balance for account %q: %w", a.Name, err)
				}
			}

			accounts = append(accounts, account)
		}

		result = SetUpFundResult{
			Fund:          fund,
			MainPurposeID: mainPurpose.ID,
			Accounts:      accounts,
		}
		return nil
	})
	if err != nil {
		return SetUpFundResult{}, fmt.Errorf("setting up fund: %w", err)
	}
	return result, nil
}

// ReplaceReportSlug gives the fund a fresh report slug from SetUpFund's
// generator and returns the updated fund row (ADR-035's "Buat tautan baru").
// It is the only way a slug changes after setup: the old one is overwritten
// and 404s on /report, with no history or redirect, since a redirect would
// defeat replacing a leaked link. A failed random source aborts rather than
// writing anything weaker.
func (l *Ledger) ReplaceReportSlug(ctx context.Context, fundID int64) (store.Fund, error) {
	slug, err := generateReportSlug()
	if err != nil {
		return store.Fund{}, err
	}
	var fund store.Fund
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		fund, err = q.UpdateFundReportSlug(ctx, store.UpdateFundReportSlugParams{ReportSlug: slug, ID: fundID})
		return err
	})
	if err != nil {
		return store.Fund{}, fmt.Errorf("replacing the report slug: %w", err)
	}
	return fund, nil
}
