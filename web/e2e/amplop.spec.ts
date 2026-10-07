import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'

// M8.9 (#440): one amplop's whole life, past the open-contribute-close that
// incidentals.spec.ts walks. Named contributions and the participation list,
// paying out of it, cancelling a contribution, correcting the occasion,
// minimum and recipients, closing with a shortfall, reopening for a late
// entry, and closing again at exactly zero - with Target, Terkumpul, Terpakai
// and Sisa read after every step that moves them.
//
// The fixture's two members (Warga Satu, Warga Dua) joined in 2024, so both
// are expected to give to an amplop opened today (ADR-034). Serial, one story
// on the seeded instance; this file's own amplop, not the fixture's.

const occasion = 'Pernikahan Anak Warga'
const renamed = 'Pernikahan Anak Warga Satu'
const t = copy.incidentals

function berandaRow(page: Page, name: string) {
  return page.getByRole('button', { name: new RegExp(`^${name}`) })
}

async function openFromBeranda(page: Page, name: string) {
  await page.goto('/')
  await berandaRow(page, name).click()
  await expect(page.getByText(t.detail.collectedLabel)).toBeVisible()
}

// The detail's totals card: each figure on the line its label names.
async function expectTotals(page: Page, f: { target?: number; collected: number; disbursed: number; balance: number }) {
  const line = (label: string) => page.getByText(label, { exact: true }).locator('..')
  if (f.target !== undefined) await expect(line(t.detail.targetLabel)).toContainText(formatIDR(f.target))
  await expect(line(t.detail.collectedLabel)).toContainText(formatIDR(f.collected))
  await expect(line(t.detail.disbursedLabel)).toContainText(formatIDR(f.disbursed))
  await expect(line(t.detail.remainingLabel)).toContainText(formatIDR(f.balance))
}

// A participation row, by member, in the expected list or under Sumbangan
// lain - scoped to Partisipasi, since the detail's own transaction list names
// the giver too.
function participant(page: Page, name: string) {
  return page
    .locator('section', { has: page.getByRole('heading', { name: t.participation.heading }) })
    .getByRole('listitem')
    .filter({ has: page.getByText(name, { exact: true }) })
}

// The record form, already on this amplop's pos (the detail's own Catat, or a
// participation row's), finished with a direction, an amount and a note.
async function finishRecord(page: Page, direction: 'in' | 'out', amount: number, note: string) {
  await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
  await page.getByRole('button', { name: direction === 'in' ? copy.record.directionIn : copy.record.directionOut }).click()
  await page.getByLabel(copy.record.amountLabel).fill(String(amount))
  await page.getByLabel(copy.record.noteLabel).fill(note)
  await page.getByRole('button', { name: copy.record.submit }).click()
  await expect(page.getByText(direction === 'in' ? copy.record.successIn : copy.record.successOut)).toBeVisible()
}

async function close(page: Page) {
  await page.getByRole('button', { name: t.actions.close, exact: true }).click()
  await page.getByRole('combobox', { name: t.close.accountLabel }).click()
  await page.getByRole('listbox').getByRole('option', { name: 'Tunai' }).click()
  await page.getByRole('button', { name: t.close.submit, exact: true }).click()
  await expect(page.getByText(t.close.success)).toBeVisible()
}

test.describe('amplop in depth', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('open with a target and a minimum; both members are expected and have not given', async ({ page }) => {
    await logIn(page)
    await page.goto('/settings')
    await page.getByRole('button', { name: copy.settings.incidentals.add }).click()
    const dialog = page.getByRole('dialog', { name: t.open.heading })
    await dialog.getByLabel(t.open.occasionLabel).fill(occasion)
    await dialog.getByLabel(t.open.targetLabel).fill('100000')
    await dialog.getByLabel(t.open.minimumLabel).fill('30000')
    await dialog.getByRole('button', { name: t.open.submit, exact: true }).click()
    await expect(dialog).toBeHidden()

    await openFromBeranda(page, occasion)
    await expectTotals(page, { target: 100_000, collected: 0, disbursed: 0, balance: 0 })
    await expect(participant(page, 'Warga Satu')).toContainText(t.participation.states.notGiven)
    await expect(participant(page, 'Warga Dua')).toContainText(t.participation.states.notGiven)
  })

  test('named contributions: one in full, one under the minimum', async ({ page }) => {
    await logIn(page)

    // From the participation row: the form arrives with the pos and the
    // giver already chosen.
    await openFromBeranda(page, occasion)
    await page.getByRole('button', { name: t.participation.recordAria('Warga Satu') }).click()
    await expect(page.getByRole('combobox', { name: copy.record.contributorLabel })).toContainText('Warga Satu')
    await finishRecord(page, 'in', 50_000, 'Sumbangan Warga Satu')

    // Through Catat, naming the giver in "Dari siapa?".
    await openFromBeranda(page, occasion)
    await page.getByRole('button', { name: t.actions.record, exact: true }).click()
    await page.getByRole('button', { name: copy.record.directionIn }).click()
    await page.getByRole('combobox', { name: copy.record.contributorLabel }).click()
    await page.getByRole('listbox').getByRole('option', { name: 'Warga Dua' }).click()
    await finishRecord(page, 'in', 20_000, 'Sumbangan Warga Dua')

    await openFromBeranda(page, occasion)
    await expectTotals(page, { target: 100_000, collected: 70_000, disbursed: 0, balance: 70_000 })
    await expect(participant(page, 'Warga Satu')).toContainText(t.participation.states.given)
    await expect(participant(page, 'Warga Satu')).toContainText(formatIDR(50_000))
    await expect(participant(page, 'Warga Dua')).toContainText(t.participation.states.underMinimum)
    await expect(participant(page, 'Warga Dua')).toContainText(formatIDR(20_000))
  })

  test('pay out of the amplop: Terpakai rises and Sisa falls', async ({ page }) => {
    await logIn(page)
    await openFromBeranda(page, occasion)
    await page.getByRole('button', { name: t.actions.record, exact: true }).click()
    await finishRecord(page, 'out', 45_000, 'Sewa tenda')

    await openFromBeranda(page, occasion)
    await expectTotals(page, { target: 100_000, collected: 70_000, disbursed: 45_000, balance: 25_000 })
  })

  test('cancel a sumbangan from Riwayat: the giver is back to not given', async ({ page }) => {
    await logIn(page)
    await page.goto('/history')
    const row = page
      .getByRole('main')
      .getByRole('listitem')
      .filter({ has: page.getByText('Sumbangan Warga Dua', { exact: true }) })
    await row.getByRole('button', { name: copy.history.transactions.reverse }).click()
    await page.getByRole('button', { name: copy.history.transactions.reverseConfirm }).click()
    await expect(page.getByText(copy.history.transactions.reverseSuccess)).toBeVisible()

    await openFromBeranda(page, occasion)
    await expectTotals(page, { target: 100_000, collected: 50_000, disbursed: 45_000, balance: 5_000 })
    await expect(participant(page, 'Warga Dua')).toContainText(t.participation.states.notGiven)
  })

  test('correct the occasion, the minimum and the recipients', async ({ page }) => {
    await logIn(page)
    await openFromBeranda(page, occasion)
    await page.getByRole('button', { name: t.actions.rename, exact: true }).click()
    const dialog = page.getByRole('dialog', { name: t.rename.heading })
    await dialog.getByLabel(t.rename.nameLabel).fill(renamed)
    const minimum = dialog.getByLabel(t.open.minimumLabel)
    await minimum.fill('')
    await minimum.fill('60000')
    // Warga Satu is who the amplop is for: no longer expected to give.
    await dialog.getByRole('checkbox', { name: 'Warga Satu' }).check()
    await dialog.getByRole('button', { name: t.rename.save }).click()
    await expect(page.getByText(t.rename.success)).toBeVisible()

    await expect(page.getByRole('heading', { name: renamed })).toBeVisible()
    await expect(page.getByText(t.participation.recipientsLine('Warga Satu'))).toBeVisible()
    // Warga Satu gave anyway, so the gift stays, under Sumbangan lain.
    await expect(page.getByText(t.participation.otherHeading)).toBeVisible()
    await expect(participant(page, 'Warga Satu')).toContainText(formatIDR(50_000))
    await expect(participant(page, 'Warga Satu')).not.toContainText(t.participation.states.given)
    await expect(participant(page, 'Warga Dua')).toContainText(t.participation.states.notGiven)
  })

  test('spend past what is left, close: the shortfall is covered from Kas Utama', async ({ page }) => {
    await logIn(page)
    await openFromBeranda(page, renamed)
    await page.getByRole('button', { name: t.actions.record, exact: true }).click()
    await finishRecord(page, 'out', 15_000, 'Konsumsi')

    await openFromBeranda(page, renamed)
    await expectTotals(page, { collected: 50_000, disbursed: 60_000, balance: -10_000 })
    await close(page)
    await expect(page.getByText(t.close.rolledLabel(-10_000))).toBeVisible()
    await expect(page.getByText(t.detail.remainingLabel, { exact: true }).locator('..')).toContainText(formatIDR(0))
  })

  test('reopen, take a late entry that nets to zero, and close again', async ({ page }) => {
    await logIn(page)
    await page.goto('/settings')
    await page.getByRole('button', { name: new RegExp(copy.settings.incidentals.closedRow) }).click()
    await page.getByRole('button', { name: new RegExp(renamed) }).click()
    await page.getByRole('button', { name: t.actions.reopen }).click()
    await expect(page.getByText(t.reopen.success)).toBeVisible()

    // Open again, so it is back on Beranda. A late gift from Warga Dua, then
    // the last bill for exactly that much.
    await openFromBeranda(page, renamed)
    await page.getByRole('button', { name: t.participation.recordAria('Warga Dua') }).click()
    await finishRecord(page, 'in', 10_000, 'Sumbangan susulan')
    await openFromBeranda(page, renamed)
    await expectTotals(page, { collected: 60_000, disbursed: 60_000, balance: 10_000 })
    await page.getByRole('button', { name: t.actions.record, exact: true }).click()
    await finishRecord(page, 'out', 10_000, 'Cetak undangan')

    await openFromBeranda(page, renamed)
    await expectTotals(page, { collected: 60_000, disbursed: 70_000, balance: 0 })
    await close(page)
    // A closed amplop's line is its whole life's net, Terkumpul less
    // Terpakai (Incidentals.tsx): the late entry nets to zero, so the
    // shortfall Kas Utama covered at the first close still stands.
    await expect(page.getByText(t.close.rolledLabel(-10_000))).toBeVisible()
    await expect(page.getByText(t.close.rolledLabel(-10_000)).locator('..')).toContainText(formatIDR(10_000))
  })

  test('close an amplop that comes out exactly even', async ({ page }) => {
    // The fixture's own amplop, empty until now: what comes in all goes out.
    const fixtureEnvelope = 'Amplop Uji Laporan'
    await logIn(page)
    await openFromBeranda(page, fixtureEnvelope)
    await page.getByRole('button', { name: t.actions.record, exact: true }).click()
    await finishRecord(page, 'in', 25_000, 'Iuran kerja bakti')
    await openFromBeranda(page, fixtureEnvelope)
    await page.getByRole('button', { name: t.actions.record, exact: true }).click()
    await finishRecord(page, 'out', 25_000, 'Beli konsumsi')

    await openFromBeranda(page, fixtureEnvelope)
    await expectTotals(page, { collected: 25_000, disbursed: 25_000, balance: 0 })
    await close(page)
    await expect(page.getByText(t.close.rolledLabel(0))).toBeVisible()
  })
})
