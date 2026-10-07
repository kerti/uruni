// Imported rather than retyped as a literal: the copy lives in one place
// (ADR-014) and this spec asserting on a stale copy of a string is exactly
// the drift that centralizing it exists to prevent. Relative, not the `@/`
// alias - that alias is a Vite/tsconfig concern and web/e2e is neither.
import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import type { Page } from '@playwright/test'

import { expect, logIn, seedEmail, seedPassword, test } from './fixtures'

// The fixture's money (cmd/uruni/seed_e2e.go): Tunai opens at Rp 1.000.000
// and Bank Uji Coba at zero. This file's instance is its own, so every figure
// below follows from that and from what the tests before it posted.
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

// The golden path this spec walks end to end, for the first time as of
// M6.10: log in -> first-run setup -> record a transaction -> home (balance
// hero + reconciliation status) -> reconcile. Each step below was a
// placeholder for the milestone that gave it a real screen to assert
// against - filled in as part of that milestone's own definition of done:
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
// Register screen is never reachable here - the golden path starts at
// Login. Register has no e2e coverage as a result; it is covered instead by
// the vitest suite (web/src/screens/Register.test.tsx), which can reach a
// fresh, unregistered instance a seeded e2e one cannot (`instance.reset({
// seed: false })` can, for a journey that wants the real thing).
//
// The same is true one layer in for M6.5's setup wizard: the same fixture
// also seeds a fund (cmd/uruni/seed_e2e.go), so GET /api/fund always answers
// 200 on a seeded instance and the wizard is never reachable here either -
// logging in lands straight past it on the home placeholder, which is what
// this slice's own test below proves (App.tsx's fund probe took the 200
// branch). The wizard's own four steps - the minimum-one-location guard, the
// optional-balance skip path, the skippable roster, and POST /api/setup
// firing exactly once - are covered instead by the vitest suite
// (web/src/screens/Setup/Setup.test.tsx and App.test.tsx), which can reach a
// fresh, fund-less instance a seeded e2e one cannot.
//
// M6.8's own test below records against the fixture's default location
// without touching the account picker on purpose - proving the "location
// remembers last used" default lands on the right account for a fresh
// browser (nothing remembered yet, so the first active account) is the
// vitest suite's job (RecordTransaction.test.tsx), which can control
// localStorage and a retired account directly; the e2e fixture seeds only
// active accounts (per #141's own ruling), so the exclusion itself has no
// e2e coverage either.
test.describe('golden path', { tag: '@smoke' }, () => {
  // @smoke as a whole: log in, record, Beranda and Cek kas are four of the
  // smoke tier's seven journeys (ADR-039), and the story only runs whole.
  // Serial: the tests below are one story over one seeded instance - "record
  // a transaction" posts an entry the "home" test then expects to find, and
  // "reconcile" is the one that changes whether a reconciliation has ever
  // been taken, which "home" needs to still read as never. The instance is
  // this file's own (reset in beforeAll), so no other file can disturb it.
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('log in as the seeded treasurer and land past auth', async ({ page }) => {
    await page.goto('/')

    // The seeded instance already has an account, so a fresh, logged-out
    // visitor lands on Login, not Register. This is the one real form login
    // in the suite; every other test signs in through logIn().
    await expect(page.getByText(copy.auth.login.heading)).toBeVisible()
    await submitLogin(page)

    // Reaching home proves both the login itself and the fund probe's 200
    // branch worked: the seeded fund means Setup is never rendered here.
    // See the file header comment for why the wizard's own steps have no
    // e2e coverage.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Log out from the header, the app's only logout control, and back in:
    // the session really ended (Login, not Beranda), and a second login in
    // the same browser works.
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

    // The fixture's cash account ("Tunai") is the first active account, so
    // it is the default location - no need to touch the picker.
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(spent))
    await page.getByRole('button', { name: copy.record.submit }).click()

    // A successful post returns to home and shows the success message there,
    // and the new entry is visible in recent activity without a manual
    // refresh (Home refetches on the "recorded" navigation - App.tsx).
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    // Scoped to recent activity: Arus kas (ADR-038) shows the same amount
    // as the month's Total keluar.
    const recent = page.locator('section', { has: page.getByRole('heading', { name: copy.home.recentActivityHeading }) })
    await expect(recent.getByText(formatIDR(spent))).toBeVisible()
    await expectBalances(page, opening - spent, 0)

    // Money in, to Kas Utama: the form's default purpose, so only the
    // direction and the amount are touched.
    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
    await page.getByRole('button', { name: copy.record.directionIn }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(received))
    await page.getByRole('button', { name: copy.record.submit }).click()

    await expect(page.getByText(copy.record.successIn)).toBeVisible()
    await expect(recent.getByText(formatIDR(received))).toBeVisible()
    await expectBalances(page, afterRecords, 0)
  })

  test('home: balance hero + reconciliation status (M6.9)', async ({ page }) => {
    await logIn(page)
    await page.goto('/')

    // The balance hero, from GET /api/balances's fund_total.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The hero and each location, in rupiah, on a fresh load rather than
    // the refetch after a record.
    await expectBalances(page, afterRecords, 0)

    // The fixture never takes a reconciliation, so open-lines is always
    // empty (only POST /api/reconciliations can ever open a line) and latest
    // always answers 404 not_found. That is the banner's neutral first-run
    // state - never the green "cocok", which only an actual count earns, and
    // never an error.
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.matched)).toBeHidden()
  })

  test('reconcile (M6.10)', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The fixture has never been reconciled before this spec runs (the
    // instance is reset for this file), so the banner is still in its neutral
    // first-run state - and is the reconcile screen's entry point (M6.10's
    // own ruling: "the reconciliation banner is the natural affordance").
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeVisible()
    await page.getByText(copy.reconciliation.neverChecked).click()
    await expect(page.getByRole('heading', { name: copy.reconciliation.heading })).toBeVisible()

    // First count: every location holds exactly what the ledger says, which
    // the record test above fixed at afterRecords in Tunai and zero in the
    // bank. Every line matches, so there is nothing to resolve.
    await page.getByLabel(copy.reconciliation.actualLabel('Bank Uji Coba')).fill('0')
    await page.getByLabel(copy.reconciliation.actualLabel('Tunai')).fill(String(afterRecords))
    // Each line says it matches, under its own count.
    await expect(page.getByText(copy.reconciliation.matched)).toHaveCount(2)
    await page.getByRole('button', { name: copy.reconciliation.submit }).click()

    // The confirmation renders from POST /api/reconciliations's own
    // response, never from the pre-submit preview above.
    await expect(page.getByRole('button', { name: copy.reconciliation.backToHome })).toBeVisible()
    await page.getByRole('button', { name: copy.reconciliation.backToHome }).click()

    // Back on home, without a manual refresh: the neutral first-run copy is
    // gone for good, and the banner is the green "cocok".
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.neverChecked)).toBeHidden()
    await expect(page.getByText(copy.reconciliation.matched)).toBeVisible()

    // Second count, from the same banner: Tunai is one rupiah, certain to
    // differ from the ledger, and the gap is left open - left_open posts
    // nothing, so the balances stay as they were. The other two resolutions
    // are proven per line by Reconcile.test.tsx.
    await page.getByText(copy.reconciliation.matched).click()
    await expect(page.getByRole('heading', { name: copy.reconciliation.heading })).toBeVisible()
    await page.getByLabel(copy.reconciliation.actualLabel('Bank Uji Coba')).fill('0')
    await page.getByLabel(copy.reconciliation.actualLabel('Tunai')).fill('1')
    await page.getByRole('button', { name: copy.reconciliation.resolutionOptions.left_open }).click()
    await page.getByRole('button', { name: copy.reconciliation.submit }).click()
    await page.getByRole('button', { name: copy.reconciliation.backToHome }).click()

    // Home now reads "selisih Rp X", X being the open gap's size.
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.reconciliation.discrepancy(formatIDR(afterRecords - 1)))).toBeVisible()
    await expect(page.getByText(copy.reconciliation.matched)).toBeHidden()
    await expectBalances(page, afterRecords, 0)

    // Riwayat -> Cek kas (#227): both snapshots, newest first - the selisih
    // one, then the cocok one - and the newest opens read-only with the line
    // left open.
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.reconciliation.heading }).click()
    const rows = page.getByRole('button').filter({ hasText: /Selisih|Cocok/ })
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0)).toContainText('Selisih')
    await expect(rows.nth(1)).toContainText(copy.reconciliation.resolutionOptions.matched)
    await rows.nth(0).click()
    await expect(page.getByText('Tunai', { exact: true })).toBeVisible()
    await expect(page.getByText(copy.reconciliation.resolutionOptions.left_open)).toBeVisible()
  })
})
