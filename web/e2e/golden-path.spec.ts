import { expect, test } from '@playwright/test'

// Imported rather than retyped as a literal: the copy lives in one place
// (ADR-014) and this spec asserting on a stale copy of a string is exactly
// the drift that centralizing it exists to prevent. Relative, not the `@/`
// alias — that alias is a Vite/tsconfig concern and web/e2e is neither.
import { copy } from '../src/copy/id'

// The golden path this spec will eventually walk end to end: register the
// treasurer → first-run setup → record a transaction → home (balance hero +
// reconciliation status) → reconcile. Each step below is a placeholder for
// the milestone that gives it a real screen to assert against — filled in as
// part of that milestone's own definition of done, not here:
//
//   M6.4  register / login
//   M6.5  first-run setup (fund, accounts, dues tier)
//   M6.8  record a transaction
//   M6.9  home (balance hero + reconciliation status)
//   M6.10 reconcile
//
// Only M6.2's router shell exists today, so this file's one real assertion
// is scoped to what that shell actually renders: a fresh, unauthenticated
// session loads and shows something coherent, not a blank page or a crash.
test.describe('golden path', () => {
  test('the app loads and renders a coherent unauthenticated state', async ({ page }) => {
    await page.goto('/')

    // M6.2's placeholder shell (App.tsx's SmokePage) is what's live today.
    // By text, not by a heading role: that copy renders inside shadcn's
    // CardTitle, which is a <div data-slot="card-title"> and carries no
    // heading role — App.test.tsx queries it the same way for the same
    // reason. Asserting on rendered copy at all (rather than on the served
    // HTML) is the point: index.html is a bare <div id="root">, so this only
    // passes if React actually mounted against the real server.
    await expect(page.getByText(copy.smoke.heading)).toBeVisible()

    // The button proves the page is interactive, not merely painted — and it
    // has a genuine ARIA role to hang that on, unlike the title above.
    await expect(page.getByRole('button', { name: copy.smoke.check })).toBeVisible()
  })

  test.fixme('register the treasurer (M6.4)', async () => {})
  test.fixme('first-run setup: fund, accounts, dues tier (M6.5)', async () => {})
  test.fixme('record a transaction (M6.8)', async () => {})
  test.fixme('home: balance hero + reconciliation status (M6.9)', async () => {})
  test.fixme('reconcile (M6.10)', async () => {})
})
