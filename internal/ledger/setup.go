package ledger

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// reportSlugAlphabet is base62: digits and both cases, so the slug is safe to
// drop straight into a URL path with no escaping.
const reportSlugAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// reportSlugLength is 32. The schema's own CHECK (length(report_slug) >= 22)
// already picked a floor - "roughly a UUID's worth of entropy in base62" - so
// 22 would satisfy it, but this generator is the slug's only source, not a
// re-derivation of something else, and PRD §7.9 treats it as a
// security-relevant unguessable token that a link, once shared, never
// rotates. 32 base62 characters is about 190 bits, comfortably past the
// schema's floor and past a UUID's 122 bits, at a cost (10 more characters in
// a URL nobody types by hand) that is free to pay once at setup.
const reportSlugLength = 32

// generateReportSlug returns a random base62 string for fund.report_slug,
// drawn from crypto/rand, never math/rand: PRD §7.9 relies on this being
// unguessable, and math/rand's default source is neither seeded for that nor
// safe against an adversary who can observe other output from it.
//
// rand.Int is used per character rather than reading raw bytes and reducing
// mod 62, which would bias the low characters of the alphabet very slightly
// toward the low end of the byte range (256 is not a multiple of 62).
// rand.Int's rejection sampling has no such bias, and this runs once per
// fund, so the extra syscalls cost nothing that matters.
//
// No collision retry: at this length a collision is far less likely than a
// hardware fault during the same call, and the schema's UNIQUE on
// report_slug is the backstop if the impossible ever happens - CreateFund
// would surface it as an ordinary unique-violation, mapped like any other by
// M4's error handling, not silently accepted.
// randInt is crypto/rand.Int, indirected so the tests can drive the failure
// below. A random source that errors is not a real operating condition, but
// this is the one call in setup whose silent failure would matter: a fund is
// created once, its slug never rotates, and the link is only unguessable if
// this call actually produced what it claims to have. The test that stubs
// this proves setup aborts rather than falling back to something weaker.
var randInt = rand.Int

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

// AccountInput is one location the treasurer names at setup - #78's
// resolution of "which accounts does a fund start with": the wizard lets her
// choose and name each one, rather than SetUpFund fixing a cash/bank pair
// for her.
type AccountInput struct {
	Kind string // "cash" or "bank" - the schema's own CHECK (kind IN (...)) is the single source of truth for the set
	Name string // non-empty after trimming - the schema's own CHECK (length(trim(name)) > 0) refuses it, not pre-validated here (ADR-027)
}

// SetUpFundParams is every argument SetUpFund needs to bring a brand-new
// fund into existence.
type SetUpFundParams struct {
	// FundName is the treasurer's own name for the fund (PRD §7.1: "name the
	// fund"). Non-empty after trimming.
	FundName string

	// Accounts is every location the treasurer wants the fund to start with
	// (#78). At least one is required - a fund with zero accounts could
	// record nothing (every transaction needs an account_id), the same
	// reasoning FundName's own empty check already applies, just aimed at a
	// slice instead of a string.
	Accounts []AccountInput
}

// SetUpFundResult names every row the caller needs immediately: the whole
// fund row (it carries report_slug, which M7's public report needs and
// nothing else in this response can substitute for), the main purpose id,
// and every created account, full rows (not bare ids) so the caller doesn't
// have to re-read them back out to learn kind/name - in the order Accounts
// was given.
type SetUpFundResult struct {
	Fund          store.Fund
	MainPurposeID int64
	Accounts      []store.Account
}

// SetUpFund writes the rows a fund cannot function without - the fund
// itself, its one kind='main' purpose, and every account the treasurer asked
// for - inside one withTx, and returns their ids.
//
// The atomic core is deliberately this small and no smaller. A transaction
// requires both an account_id and a purpose_id (schema FKs), so a fund with
// no main purpose and no accounts cannot record even its first entry - a
// crash partway through would leave a fund that exists, appears in
// ListFunds, and has nowhere to post anything, the same orphan shape #42's
// OpenIncidental closed for a purpose with no incidental row behind it,
// inverted onto a fund with nothing underneath it at all. #78 does not
// change that argument, only its size: N accounts instead of a fixed two,
// still written inside the same transaction as the fund and its main
// purpose.
//
// Members, dues tiers, dues rates and the two opening balances are
// deliberately NOT here. Members carry no cross-row invariant beyond what
// the schema already enforces (ADR-027's own carve-out - no domain wrapper
// for them at all). Tiers and rates have only ordinary unique constraints a
// create-then-retry handles with a 409. PostOpeningBalance already has
// exactly the resumable shape #51 built: a clean ErrOpeningBalanceExists on
// retry, a zero amount posts nothing. Folding any of these in would mean one
// mistyped tier reference aborts a perfectly good fund and its accounts -
// the opposite of what a "first-run setup" flow should risk. They are
// composed afterward by the caller as ordinary retriable calls.
//
// A second run is refused. The pre-check runs inside this same withTx and
// returns ErrFundAlreadyExists the moment a fund is found - see that
// sentinel's own doc comment for why this guard, alone among this package's
// pre-checks, has no unique index backing it.
func (l *Ledger) SetUpFund(ctx context.Context, p SetUpFundParams) (SetUpFundResult, error) {
	name := strings.TrimSpace(p.FundName)
	if name == "" {
		return SetUpFundResult{}, fmt.Errorf("%w: fund name must not be empty", ErrInvalidArgument)
	}
	if len(p.Accounts) == 0 {
		return SetUpFundResult{}, fmt.Errorf("%w: at least one account is required", ErrInvalidArgument)
	}

	var result SetUpFundResult
	err := l.withTx(ctx, func(q store.Querier) error {
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
			Name: name, Currency: "IDR", ReportSlug: slug, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating fund: %w", err)
		}

		// "Kas Utama" is the domain's own name for the routine purpose
		// (CONTEXT.md, ADR-014) - not user input, so there is no field for
		// it on SetUpFundParams.
		mainPurpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
			FundID: fund.ID, Kind: "main", Name: "Kas Utama", CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating main purpose: %w", err)
		}

		// #78: the treasurer chooses and names each account, no longer a
		// fixed cash/bank pair - kind and name shape (CHECK (kind IN
		// ('cash','bank')), CHECK (length(trim(name)) > 0)) are the schema's
		// job alone to refuse, the same split every other direct-CRUD write
		// in this codebase already uses (ADR-027); a bad row here aborts the
		// whole withTx, same as the purpose-insert failure above.
		accounts := make([]store.Account, 0, len(p.Accounts))
		for _, a := range p.Accounts {
			account, err := q.CreateAccount(ctx, store.CreateAccountParams{
				FundID: fund.ID, Kind: a.Kind, Name: a.Name, CreatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("creating account %q: %w", a.Name, err)
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
