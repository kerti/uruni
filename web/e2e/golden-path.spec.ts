import { expect, test } from '@playwright/test'

// Imported rather than retyped as a literal: the copy lives in one place
// (ADR-014) and this spec asserting on a stale copy of a string is exactly
// the drift that centralizing it exists to prevent. Relative, not the `@/`
// alias — that alias is a Vite/tsconfig concern and web/e2e is neither.
import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'

// The golden path this spec walks end to end, for the first time as of
// M6.10: log in → first-run setup → record a transaction → home (balance
// hero + reconciliation status) → reconcile. Each step below was a
// placeholder for the milestone that gave it a real screen to assert
// against — filled in as part of that milestone's own definition of done:
//
//   M6.4  register / login
//   M6.5  first-run setup (fund, accounts, dues tier)
//   M6.8  record a transaction
//   M6.9  home (balance hero + reconciliation status)
//   M6.10 reconcile (this slice)
//
// M6.4 gives this spec its first screen to walk through:
// `cmd/uruni/seed_e2e.go`'s fixture command seeds
// e2e's instance with a bendahara account already registered, so a seeded
// server always answers GET /api/session with has_account: true and the
// Register screen is never reachable here — the golden path starts at
// Login. Register has no e2e coverage as a result; it is covered instead by
// the vitest suite (web/src/screens/Register.test.tsx), which can reach a
// fresh, unregistered instance a shared e2e fixture cannot.
//
// The same is true one layer in for M6.5's setup wizard: the same fixture
// also seeds a fund (cmd/uruni/seed_e2e.go), so GET /api/fund always answers
// 200 on a seeded instance and the wizard is never reachable here either —
// logging in lands straight past it on the home placeholder, which is what
// this slice's own test below proves (App.tsx's fund probe took the 200
// branch). The wizard's own four steps - the minimum-one-location guard, the
// optional-balance skip path, the skippable roster, and POST /api/setup
// firing exactly once - are covered instead by the vitest suite
// (web/src/screens/Setup/Setup.test.tsx and App.test.tsx), which can reach a
// fresh, fund-less instance a shared e2e fixture cannot.
//
// M6.8's own test below records against the fixture's default location
// without touching the account picker on purpose — proving the "location
// remembers last used" default lands on the right account for a fresh
// browser (nothing remembered yet, so the first active account) is the
// vitest suite's job (RecordTransaction.test.tsx), which can control
// localStorage and a retired account directly; the e2e fixture seeds only
// active accounts (per #141's own ruling), so the exclusion itself has no
// e2e coverage either.
test.describe('golden path', () => {
  // Serial, not the project's default fullyParallel: true. Every spec below
  // shares one seeded database (playwright.config.ts's webServer) and reads
  // it as a continuous story - "record a transaction" posts a real entry
  // the "home" spec then expects to find, and (new as of M6.10) "reconcile"
  // is the first spec that changes whether a reconciliation has ever been
  // taken at all, which the "home" spec above it depends on staying false
  // until it has run. None of that is safe under out-of-order or concurrent
  // execution, so this describe opts out of the project default for just
  // this file.
  test.describe.configure({ mode: 'serial' })

  // The seeded treasurer account (cmd/uruni/seed_e2e.go) - literals, not an
  // import, because that file is Go and this spec can't reach into it; only
  // copy crosses the language boundary via the import above.
  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'

  test('log in as the seeded treasurer and land past auth', async ({ page }) => {
    await page.goto('/')

    // The seeded instance already has an account, so a fresh, logged-out
    // visitor lands on Login, not Register.
    await expect(page.getByText(copy.auth.login.heading)).toBeVisible()

    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()

    // Reaching home proves both the login itself and the fund probe's 200
    // branch worked: the seeded fund means Setup is never rendered here.
    // See the file header comment for why the wizard's own steps have no
    // e2e coverage.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
  })

  test('record a transaction (M6.8)', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()

    // The fixture's cash account ("Tunai") is the first active account, so
    // it is the default location - no need to touch the picker.
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill('50000')
    await page.getByRole('button', { name: copy.record.submit }).click()

    // A successful post returns to home and shows the success message there,
    // and the new entry is visible in recent activity without a manual
    // refresh (Home refetches on the "recorded" navigation - App.tsx).
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    await expect(page.getByText(formatIDR(50_000))).toBeVisible()
  })

  test('home: balance hero + reconciliation status (M6.9)', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()

    // The balance hero, from GET /api/balances's fund_total.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Per-location balances - the fixture's own two accounts
    // (cmd/uruni/seed_e2e.go), whatever their current balance reads as.
    await expect(page.getByText('Tunai')).toBeVisible()
    await expect(page.getByText('Bank Uji Coba')).toBeVisible()

    // The fixture never takes a reconciliation, so open-lines is always
    // empty (only POST /api/reconciliations can ever open a line) and latest
    // always answers 404 not_found. That is the banner's neutral first-run
    // state - never the green "cocok", which only an actual count earns, and
    // never an error.
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.matched)).toBeHidden()
  })

  test('reconcile (M6.10)', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The fixture has never been reconciled before this spec runs (fresh
    // per `make e2e-reset`), so the banner is still in its neutral
    // first-run state - and is the reconcile screen's entry point (M6.10's
    // own ruling: "the reconciliation banner is the natural affordance").
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeVisible()
    await page.getByText(copy.reconciliation.neverChecked).click()
    await expect(page.getByRole('heading', { name: copy.reconciliation.heading })).toBeVisible()

    // Bank Uji Coba is never touched by any other spec in this file (only
    // "record a transaction" posts anything, and always against Tunai, the
    // default location) - it is reliably exactly 0, so counting it as 0 is
    // a safe zero-gap "matched" line. Tunai's own recorded figure is *not*
    // something this spec hardcodes: it is derived (opening balance minus
    // whatever else has posted against it), and re-deriving that arithmetic
    // here would just be restating M6.8's own seed/record math with extra
    // steps. Typing "1" is certain to differ from Tunai's real balance and
    // is resolved as left_open, which never re-checks the figure it was
    // given against the ledger (TakeReconciliation posts nothing for
    // left_open) - the one resolution that cannot be rejected regardless of
    // what Tunai's true recorded amount is. The plain matched path and the
    // other two resolutions are already proven directly, per line, by
    // Reconcile.test.tsx's own vitest suite.
    await page.getByLabel(copy.reconciliation.actualLabel('Bank Uji Coba')).fill('0')
    await page.getByLabel(copy.reconciliation.actualLabel('Tunai')).fill('1')
    await expect(page.getByText(copy.reconciliation.matched)).toBeVisible()

    await page.getByRole('button', { name: copy.reconciliation.resolutionOptions.left_open }).click()
    await page.getByRole('button', { name: copy.reconciliation.submit }).click()

    // The confirmation renders from POST /api/reconciliations's own
    // response, never from the pre-submit preview above.
    await expect(page.getByRole('button', { name: copy.reconciliation.backToHome })).toBeVisible()
    await page.getByRole('button', { name: copy.reconciliation.backToHome }).click()

    // Back on home, without a manual refresh, the count that was just taken
    // is reflected - the neutral first-run copy is gone for good once a
    // count exists, whatever state the banner lands in.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeHidden()
  })
})
