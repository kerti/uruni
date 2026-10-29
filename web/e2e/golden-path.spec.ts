// Imported rather than retyped: the copy lives in one place (ADR-014), and a
// spec asserting on a stale copy of a string is the drift centralizing it
// prevents. Relative, not the `@/` alias - that is a Vite/tsconfig concern and
// web/e2e is neither.
import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import type { Page } from '@playwright/test'

import { expect, logIn, seedEmail, seedPassword, test } from './fixtures'
import { expectAccessible } from './a11y'

// The fixture's money (cmd/uruni/seed_e2e.go): Tunai opens at Rp1.000.000 and
// Bank Uji Coba at zero. This file's instance is its own, so every figure below
// follows from that and from what the earlier tests posted.
const opening = 1_000_000
const spent = 50_000
const received = 20_000
const afterRecords = opening - spent + received

// Beranda's figures, each scoped to where it is shown: the same rupiah amount
// can also appear in Saldo per pos, Arus kas and recent activity.
function hero(page: Page) {
  return page.getByRole('region', { name: copy.home.balanceHeading })
}
function locationRow(page: Page, name: string) {
  return page.getByRole('listitem').filter({ has: page.getByText(name, { exact: true }) })
}

async function expectBalances(page: Page, tunai: number, bank: number) {
  await expect(hero(page)).toContainText(formatIDR(tunai + bank))
  await expect(locationRow(page, 'Tunai')).toContainText(formatIDR(tunai))
  await expect(locationRow(page, 'Bank Uji Coba')).toContainText(formatIDR(bank))
}

async function submitLogin(page: Page) {
  await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
  await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(seedPassword)
  await page.getByRole('button', { name: copy.auth.login.submit }).click()
}

// The golden path, end to end: log in -> record a transaction -> home (balance
// hero + reconciliation status) -> reconcile.
//
// `cmd/uruni/seed_e2e.go` seeds a bendahara account and a fund, so a seeded
// server answers GET /api/session with has_account: true and the path starts at
// Login, then lands straight on home (the fund probe's 200 branch). Register
// and the setup wizard are walked on an unseeded instance by first-run.spec.ts;
// their edge cases stay in the vitest suite (Register.test.tsx,
// Setup/Setup.test.tsx, App.test.tsx).
//
// The record test records against the fixture's default location without
// touching the account picker: that "location remembers last used" lands on
// the right account for a fresh browser is the vitest suite's job
// (RecordTransaction.test.tsx), which can control localStorage and a retired
// account; the fixture seeds only active accounts, so the retired-account
// exclusion has no e2e coverage either.
test.describe('golden path', { tag: '@smoke' }, () => {
  // @smoke as a whole: log in, record, Beranda and Cek kas are four of the
  // smoke tier's seven journeys (ADR-039), and the story only runs whole.
  // Serial: the tests are one story over one seeded instance - "record" posts
  // an entry "home" expects to find, and "reconcile" changes whether a
  // reconciliation has ever been taken, which "home" needs to still read as
  // never. The instance is this file's own (reset in beforeAll).
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('log in as the seeded treasurer and land past auth', async ({ page }) => {
    await page.goto('/')

    // The seeded instance has an account, so a logged-out visitor lands on
    // Login, not Register. The one real form login in the suite; every other
    // test signs in through logIn().
    await expect(page.getByText(copy.auth.login.heading)).toBeVisible()
    await expectAccessible(page)
    await submitLogin(page)

    // Reaching home proves the login and the fund probe's 200 branch: the
    // seeded fund means Setup is never rendered here.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Log out from the header (the app's only logout control) and back in: the
    // session really ended (Login, not Beranda), and a second login works.
    await page.getByRole('button', { name: copy.shell.logout, exact: true }).click()
    await expect(page.getByText(copy.auth.login.heading)).toBeVisible()
    await expect(page.getByText(copy.home.balanceHeading)).toBeHidden()
    await submitLogin(page)
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
  })

  test('record a transaction (M6.8)', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expectBalances(page, opening, 0)

    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
    await expectAccessible(page)

    // The fixture's cash account ("Tunai") is the first active account, so it
    // is the default location - no need to touch the picker.
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(spent))
    await page.getByRole('button', { name: copy.record.submit }).click()

    // A successful post returns to home with the success message, and the new
    // entry shows in recent activity without a manual refresh (Home refetches
    // on the "recorded" navigation).
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    // Scoped to recent activity: Arus kas shows the same amount as the month's
    // Total keluar.
    const recent = page.locator('section', { has: page.getByRole('heading', { name: copy.home.recentActivityHeading }) })
    await expect(recent.getByText(formatIDR(spent))).toBeVisible()
    await expectBalances(page, opening - spent, 0)

    // Money in, to Kas Utama: the form's default purpose, so only the direction
    // and amount are touched.
    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
    await page.getByRole('button', { name: copy.record.directionIn }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(received))
    await page.getByRole('button', { name: copy.record.submit }).click()

    await expect(page.getByText(copy.record.successIn)).toBeVisible()
    await expect(recent.getByText(formatIDR(received))).toBeVisible()
    await expectBalances(page, afterRecords, 0)

    // "Lihat semua" opens Riwayat on its Transaksi tab, where both rows are.
    await recent.getByRole('button', { name: copy.home.recentActivityViewAll }).click()
    await expect(page).toHaveURL(/\/history\/transactions$/)
    await expect(page.getByRole('searchbox', { name: copy.history.transactions.searchLabel })).toBeVisible()
    await expect(
      page
        .getByRole('main')
        .getByRole('listitem')
        .filter({ hasText: formatIDR(spent) }),
    ).toBeVisible()
  })

  test('home: balance hero + reconciliation status (M6.9)', async ({ page }) => {
    await logIn(page)
    await page.goto('/')

    // The balance hero, from GET /api/balances's fund_total.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The hero and each location, on a fresh load rather than the refetch after
    // a record.
    await expectBalances(page, afterRecords, 0)

    // The fixture never takes a reconciliation, so open-lines is empty (only
    // POST /api/reconciliations opens a line) and latest answers 404 not_found:
    // the banner's neutral first-run state - never the green "cocok", which
    // only a count earns, and never an error.
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.matched)).toBeHidden()
    await expectAccessible(page)
  })

  test('reconcile (M6.10)', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The fixture has never been reconciled before this spec (the instance is
    // reset for this file), so the banner is in its neutral first-run state -
    // and is the reconcile screen's entry point.
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeVisible()
    await page.getByText(copy.reconciliation.neverChecked).click()
    await expect(page.getByRole('heading', { name: copy.reconciliation.heading })).toBeVisible()

    // First count: every location holds what the ledger says (afterRecords in
    // Tunai, zero in the bank). Every line matches, so nothing to resolve.
    await page.getByLabel(copy.reconciliation.actualLabel('Bank Uji Coba')).fill('0')
    await page.getByLabel(copy.reconciliation.actualLabel('Tunai')).fill(String(afterRecords))
    // Each line says it matches, under its own count.
    await expect(page.getByText(copy.reconciliation.matched)).toHaveCount(2)
    await expectAccessible(page)
    await page.getByRole('button', { name: copy.reconciliation.submit }).click()

    // The confirmation renders from POST /api/reconciliations's response, never
    // the pre-submit preview.
    await expect(page.getByRole('button', { name: copy.reconciliation.backToHome })).toBeVisible()
    await expectAccessible(page)
    await page.getByRole('button', { name: copy.reconciliation.backToHome }).click()

    // Back on home without a manual refresh: the neutral copy is gone for good,
    // and the banner is the green "cocok".
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeHidden()
    await expect(page.getByText(copy.reconciliation.matched)).toBeVisible()

    // Second count, from the same banner: Tunai is one rupiah, certain to differ
    // from the ledger, and the gap is left open - left_open posts nothing, so
    // the balances stay. The other resolutions are proven per line by
    // Reconcile.test.tsx.
    await page.getByText(copy.reconciliation.matched).click()
    await expect(page.getByRole('heading', { name: copy.reconciliation.heading })).toBeVisible()
    await page.getByLabel(copy.reconciliation.actualLabel('Bank Uji Coba')).fill('0')
    await page.getByLabel(copy.reconciliation.actualLabel('Tunai')).fill('1')
    await page.getByRole('button', { name: copy.reconciliation.resolutionOptions.left_open }).click()
    await page.getByRole('button', { name: copy.reconciliation.submit }).click()
    await page.getByRole('button', { name: copy.reconciliation.backToHome }).click()

    // Home now reads "selisih RpX", X being the open gap's size.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.discrepancy(formatIDR(afterRecords - 1)))).toBeVisible()
    await expect(page.getByText(copy.reconciliation.matched)).toBeHidden()
    await expectBalances(page, afterRecords, 0)

    // Riwayat -> Cek kas: both snapshots, newest first - the selisih one, then
    // the cocok one - and the newest opens read-only with the line left open.
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.reconciliation.heading }).click()
    const rows = page.getByRole('button').filter({ hasText: /Selisih|Cocok/ })
    await expect(rows).toHaveCount(2)
    await expectAccessible(page)
    // nth: position is the assertion - the list is newest first.
    await expect(rows.nth(0)).toContainText('Selisih')
    await expect(rows.nth(1)).toContainText(copy.reconciliation.resolutionOptions.matched)
    await rows.nth(0).click()
    await expect(page.getByText('Tunai', { exact: true })).toBeVisible()
    await expect(page.getByText(copy.reconciliation.resolutionOptions.left_open)).toBeVisible()
    await expectAccessible(page)
  })
})
