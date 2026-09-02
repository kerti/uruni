import { expect, test } from '@playwright/test'

// Imported rather than retyped as a literal: the copy lives in one place
// (ADR-014) and this spec asserting on a stale copy of a string is exactly
// the drift that centralizing it exists to prevent. Relative, not the `@/`
// alias — that alias is a Vite/tsconfig concern and web/e2e is neither.
import { copy } from '../src/copy/id'

// The golden path this spec will eventually walk end to end: log in →
// first-run setup → record a transaction → home (balance hero +
// reconciliation status) → reconcile. Each step below is a placeholder for
// the milestone that gives it a real screen to assert against — filled in as
// part of that milestone's own definition of done, not here:
//
//   M6.4  register / login (this slice)
//   M6.5  first-run setup (fund, accounts, dues tier) (this slice)
//   M6.8  record a transaction (this slice)
//   M6.9  home (balance hero + reconciliation status)
//   M6.10 reconcile
//
// M6.2's router shell still exists (the smoke page is the placeholder every
// screen but auth eventually replaces), but M6.4 gives this spec its first
// screen to walk through: `cmd/uruni/seed_e2e.go`'s fixture command seeds
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

    // Past auth is still M6.2's placeholder shell today (M6.9 replaces it
    // with a real home) - reaching it proves both the login itself and the
    // fund probe's 200 branch worked: the seeded fund means Setup is never
    // rendered here, only SmokePage. See the file header comment for why the
    // wizard's own steps have no e2e coverage.
    await expect(page.getByText(copy.smoke.heading)).toBeVisible()
  })

  test('record a transaction (M6.8)', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.smoke.heading)).toBeVisible()

    await page.getByRole('link', { name: copy.record.addAction }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()

    // The fixture's cash account ("Tunai") is the first active account, so
    // it is the default location - no need to touch the picker.
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill('50000')
    await page.getByRole('button', { name: copy.record.submit }).click()

    // A successful post returns to home and shows the success message there
    // - there is no recent-activity list yet (M6.9's job).
    await expect(page.getByText(copy.smoke.heading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
  })
  test.fixme('home: balance hero + reconciliation status (M6.9)', async () => {})
  test.fixme('reconcile (M6.10)', async () => {})
})
