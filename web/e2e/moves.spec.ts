import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'

// M8.6 (#437): money that moves without leaving the fund. Pindah lokasi
// moves it between places, Pindah pos between purposes, and Perbaiki pos
// re-tags a posted row after the fact. Each one leaves the fund total where
// it was and moves exactly the figures it names - which is the whole promise
// these three make to the treasurer, so each test reads the total and the
// figures on Beranda before and after.
//
// The fixture (cmd/uruni/seed_e2e.go): Tunai opens at Rp 1.000.000, Bank Uji
// Coba at zero, everything in Kas Utama, and one empty open envelope. Serial,
// one story: each test starts from the figures the one before left.

const envelope = 'Amplop Uji Laporan'
const opening = 1_000_000
const moved = 200_000
const allocated = 100_000
const spent = 30_000

function hero(page: Page) {
  return page.getByRole('region', { name: copy.home.balanceHeading })
}
function locationRow(page: Page, name: string) {
  return page.getByRole('listitem').filter({ has: page.getByText(name, { exact: true }) })
}
// Saldo per pos: one button per pos, named from the pos's own name first.
function purposeRow(page: Page, name: string) {
  return page.getByRole('button', { name: new RegExp(`^${name}`) })
}

async function expectBeranda(page: Page, f: { tunai: number; bank: number; main: number; envelope: number }) {
  await expect(hero(page)).toContainText(formatIDR(f.tunai + f.bank))
  await expect(locationRow(page, 'Tunai')).toContainText(formatIDR(f.tunai))
  await expect(locationRow(page, 'Bank Uji Coba')).toContainText(formatIDR(f.bank))
  await expect(purposeRow(page, 'Kas Utama')).toContainText(formatIDR(f.main))
  await expect(purposeRow(page, envelope)).toContainText(formatIDR(f.envelope))
}

async function pick(page: Page, label: string, option: string) {
  // The themed Select is a button plus a portalled listbox; the option is
  // scoped to the listbox because Radix's hidden native select carries the
  // same text.
  await page.getByRole('combobox', { name: label }).click()
  await page.getByRole('listbox').getByRole('option', { name: option }).click()
}

async function openRecord(page: Page) {
  await page.getByRole('link', { name: copy.shell.nav.record }).click()
  await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
}

test.describe('moves and corrections', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('Pindah lokasi moves cash to the bank and leaves the fund total alone', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expectBeranda(page, { tunai: opening, bank: 0, main: opening, envelope: 0 })

    await openRecord(page)
    await page.getByRole('button', { name: copy.record.directionTransfer }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(moved))
    await pick(page, copy.record.fromLocationLabel, 'Tunai')
    await pick(page, copy.record.toLocationLabel, 'Bank Uji Coba')
    await page.getByRole('button', { name: copy.record.submit }).click()

    await expect(page.getByText(copy.record.successTransfer)).toBeVisible()
    await expectBeranda(page, { tunai: opening - moved, bank: moved, main: opening, envelope: 0 })
  })

  test('Pindah pos moves money from Kas Utama to an amplop and leaves the fund total alone', async ({ page }) => {
    await logIn(page)
    await page.goto('/')

    await openRecord(page)
    await page.getByRole('button', { name: copy.record.directionPurpose }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(allocated))
    await pick(page, copy.record.fromPurposeLabel, 'Kas Utama')
    await pick(page, copy.record.toPurposeLabel, envelope)
    await page.getByRole('button', { name: copy.record.submit }).click()

    await expect(page.getByText(copy.record.successPurposeMove)).toBeVisible()
    // The locations do not move at all: a pos says what money is for, not
    // where it is.
    await expectBeranda(page, { tunai: opening - moved, bank: moved, main: opening - allocated, envelope: allocated })
  })

  test('Perbaiki pos re-tags a posted spend to the amplop, and only the pos figures move', async ({ page }) => {
    await logIn(page)
    await page.goto('/')

    // A spend tagged Kas Utama (the form's default pos) that belonged to the
    // amplop.
    await openRecord(page)
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(spent))
    await page.getByRole('button', { name: copy.record.submit }).click()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    const afterSpend = { tunai: opening - moved - spent, bank: moved, main: opening - allocated - spent, envelope: allocated }
    await expectBeranda(page, afterSpend)

    // Riwayat > Transaksi: the spend's own row, found by its amount, carries
    // the correction control on its pos name.
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    const row = page.getByRole('listitem').filter({ hasText: formatIDR(spent) })
    await row.getByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') }).click()

    const dialog = page.getByRole('dialog', { name: copy.purposeCorrection.heading })
    await expect(dialog.getByText(copy.purposeCorrection.explainer)).toBeVisible()
    await pick(page, copy.purposeCorrection.pickerLabel, envelope)
    await dialog.getByRole('button', { name: copy.purposeCorrection.save }).click()
    await expect(page.getByText(copy.purposeCorrection.success)).toBeVisible()

    // The posted row is never edited (it is immutable); it now says it was
    // corrected.
    await expect(row.getByText(copy.purposeCorrection.corrected)).toBeAttached()

    // Back on Beranda: the cash and the fund total are untouched, and the
    // spend has moved from Kas Utama to the amplop.
    await page.getByRole('link', { name: copy.shell.nav.home }).click()
    await expectBeranda(page, { ...afterSpend, main: afterSpend.main + spent, envelope: afterSpend.envelope - spent })
  })
})
