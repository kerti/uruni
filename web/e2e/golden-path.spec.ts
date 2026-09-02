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
//   M6.5  first-run setup (fund, accounts, dues tier)
//   M6.8  record a transaction
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

    // Past auth is still M6.2's placeholder shell today (M6.5 replaces it
    // with real setup-or-home routing) - reaching it proves the login
    // itself worked, which is all this slice owns.
    await expect(page.getByText(copy.smoke.heading)).toBeVisible()
  })

  test.fixme('first-run setup: fund, accounts, dues tier (M6.5)', async () => {})
  test.fixme('record a transaction (M6.8)', async () => {})
  test.fixme('home: balance hero + reconciliation status (M6.9)', async () => {})
  test.fixme('reconcile (M6.10)', async () => {})
})
