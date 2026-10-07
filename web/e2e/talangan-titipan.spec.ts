import type { APIRequestContext, Locator, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'

// M8.10 (#441): the two ways money sits in the fund without being the
// fund's own. Talangan - a member's own money paid for the kas (removing a
// claim, its receipt photo, what Beranda says the fund owes, the Semua tab) -
// and Titipan, money held for someone else (made in Pengaturan, received and
// forwarded as two ordinary records, its own pos on Beranda and on the
// public report, renamed).
//
// Paging and search on Belum dibayar live in browsing.spec.ts; this file adds
// the Semua tab's search, where a settled claim is still found.
//
// Serial, one story on the seeded fixture (Tunai Rp 1.000.000, Kas Utama,
// Warga Satu and Warga Dua). Claims are seeded over the API: the record form
// has its own journey in reimbursements.spec.ts.

// The same 8x8 JPEG receipts.spec.ts uploads: written by Go's own encoder, so
// the server's decoder is guaranteed to accept it.
const TINY_JPEG = Buffer.from(
  '/9j/2wCEAAYEBQYFBAYGBQYHBwYIChAKCgkJChQODwwQFxQYGBcUFhYaHSUfGhsjHBYWICwgIyYnKSopGR8tMC0oMCUoKSgBBwcHCggKEwoKEygaFhooKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKP/AABEIAAgACAMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/APNqKKK1PnT/2Q==',
  'base64',
)

const titipan = 'Kas Bidang Uji'
const titipanRenamed = 'Kas Bidang Pusat'

type Named = { id: number; name: string }

function jakartaToday(): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Jakarta' }).format(new Date())
}

async function api<T>(request: APIRequestContext, method: 'GET' | 'POST', path: string, data?: unknown): Promise<T> {
  const response = method === 'GET' ? await request.get(path) : await request.post(path, { data })
  expect(response.ok(), `${method} ${path} answered ${response.status()}: ${await response.text()}`).toBe(true)
  return (await response.json()) as T
}

async function byName(request: APIRequestContext, path: string, key: string, name: string): Promise<number> {
  const body = await api<Named[] | Record<string, Named[]>>(request, 'GET', path)
  const list = Array.isArray(body) ? body : body[key]
  const row = list.find((r) => r.name === name)
  if (!row) throw new Error(`${name} not found in GET ${path}`)
  return row.id
}

// Scoped to main: the footer's five tabs are list items too.
function rows(page: Page) {
  return page.getByRole('main').getByRole('listitem')
}

// A claim's card, by its note (an element of its own in the card).
function claim(page: Page, note: string) {
  return rows(page).filter({ has: page.getByText(note, { exact: true }) })
}

// The report's "Saldo per pos" list (reportText.PurposesLabel).
function reportPos(page: Page) {
  return page.locator('section').filter({ has: page.getByRole('heading', { name: 'Saldo per pos' }) })
}

function berandaRow(page: Page, name: string) {
  return page.getByRole('button', { name: new RegExp(`^${name}`) })
}

async function openTalangan(page: Page) {
  await page.getByRole('link', { name: copy.shell.nav.history }).click()
  await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
  await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()
}

// The row's "more" menu, then Hapus. Retried as a pair, for the reason
// choosePhotoAction gives: a menu still animating closed turns the trigger
// click into a toggle.
async function chooseHapus(card: Locator, page: Page) {
  await expect(async () => {
    const item = page.getByRole('menuitem', { name: copy.reimbursements.actions.delete, exact: true })
    if (!(await item.isVisible())) await card.getByRole('button', { name: copy.reimbursements.actions.menuAria }).click()
    await item.click({ timeout: 2_000 })
  }).toPass()
}

// Hapus from the menu, then the confirmation's own Hapus.
async function removeClaim(card: Locator, page: Page) {
  await chooseHapus(card, page)
  await card.getByRole('button', { name: copy.reimbursements.actions.delete, exact: true }).click()
}

// The photo's "more" menu, then one of its items. Retried as a pair - see
// receipts.spec.ts, which this is copied from: a menu still animating closed
// turns the trigger click into a toggle.
async function choosePhotoAction(page: Page, dialog: Locator, item: string) {
  await expect(async () => {
    const menuItem = page.getByRole('menuitem', { name: item })
    if (!(await menuItem.isVisible())) await dialog.getByRole('button', { name: copy.receipts.photoMenuAria }).click()
    await menuItem.click({ timeout: 2_000 })
  }).toPass()
}

// Catat -> Masuk or Keluar -> Pos -> amount -> Simpan; lands on Beranda.
async function recordForPos(page: Page, direction: 'in' | 'out', pos: string, amount: number) {
  await page.getByRole('link', { name: copy.shell.nav.record }).click()
  await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
  await page.getByRole('button', { name: direction === 'in' ? copy.record.directionIn : copy.record.directionOut }).click()
  await page.getByRole('combobox', { name: copy.record.purposeLabel }).click()
  await page.getByRole('listbox').getByRole('option', { name: pos }).click()
  await page.getByLabel(copy.record.amountLabel).fill(String(amount))
  await page.getByRole('button', { name: copy.record.submit }).click()
  await expect(page.getByText(direction === 'in' ? copy.record.successIn : copy.record.successOut)).toBeVisible()
}

test.describe('talangan and titipan', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const salah = 'Salah catat'
  const terpal = 'Beli terpal'
  const galon = 'Beli galon'

  test('Beranda owes the members exactly what is unpaid, and a tap opens Talangan', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const today = jakartaToday()
    const main = await byName(request, '/api/purposes', 'purposes', 'Kas Utama')
    const tunai = await byName(request, '/api/accounts', 'accounts', 'Tunai')
    const wargaSatu = await byName(request, '/api/members', 'members', 'Warga Satu')
    const wargaDua = await byName(request, '/api/members', 'members', 'Warga Dua')

    await api(request, 'POST', '/api/reimbursements', {
      member_id: wargaSatu,
      purpose_id: main,
      amount: 15_000,
      incurred_on: today,
      note: salah,
    })
    await api(request, 'POST', '/api/reimbursements', {
      member_id: wargaDua,
      purpose_id: main,
      amount: 40_000,
      incurred_on: today,
      note: terpal,
    })
    // A settled claim is not owed any more: it must not be in the total.
    const settled = await api<{ id: number }>(request, 'POST', '/api/reimbursements', {
      member_id: wargaSatu,
      purpose_id: main,
      amount: 9_000,
      incurred_on: today,
      note: galon,
    })
    await api(request, 'POST', `/api/reimbursements/${settled.id}/settle`, { account_id: tunai, occurred_on: today })

    await page.goto('/')
    const owed = page.getByRole('button', { name: new RegExp(copy.home.owedToMembers) })
    await expect(owed).toBeVisible()
    await expect(owed).toContainText(formatIDR(55_000))

    await owed.click()
    await expect(page).toHaveURL(/\/history\/reimbursements$/)
    await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()
    await expect(claim(page, salah)).toBeVisible()
    await expect(claim(page, terpal)).toBeVisible()
    // The settled one is not on Belum dibayar.
    await expect(claim(page, galon)).toHaveCount(0)
  })

  test('the Semua tab searches the whole history, a settled claim included', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openTalangan(page)
    const search = page.getByRole('searchbox', { name: copy.history.reimbursements.searchLabel })

    // Belum dibayar: the settled claim is not there to be found.
    await search.fill('galon')
    await expect(page.getByText(copy.history.reimbursements.noResults('galon'))).toBeVisible()

    // Semua: the same search finds it, marked Dibayar. The search survives
    // the tab switch (it is in the URL).
    await page.getByRole('button', { name: copy.reimbursements.allTab }).click()
    await expect(claim(page, galon)).toBeVisible()
    await expect(claim(page, galon).getByText(copy.reimbursements.status.settled)).toBeVisible()
    await expect(claim(page, terpal)).toHaveCount(0)

    // By member, across statuses.
    await search.fill('Warga Dua')
    await expect(claim(page, terpal)).toBeVisible()
    await expect(claim(page, galon)).toHaveCount(0)
  })

  test('a receipt added after the fact is viewed, and Hapus is refused until the photo is deleted', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openTalangan(page)
    const card = claim(page, salah)

    // Attach: the row control offers to add, the dialog takes the photo.
    await card.getByRole('button', { name: copy.receipts.rowControlAria(false) }).click()
    const dialog = page.getByRole('dialog', { name: copy.receipts.dialogHeading, exact: true })
    await expect(dialog.getByText(copy.receipts.emptyReimbursement)).toBeVisible()
    await dialog.locator('input[type="file"]').setInputFiles({ name: 'nota.jpg', mimeType: 'image/jpeg', buffer: TINY_JPEG })
    await dialog.getByRole('button', { name: copy.receipts.addFromRow, exact: true }).last().click()
    const photo = dialog.getByRole('button', { name: copy.receipts.zoomAria }).locator('img')
    await expect(photo).toHaveCount(1)

    // View: the photo is read back from the server and opens full screen.
    await dialog.getByRole('button', { name: copy.receipts.zoomAria }).click()
    await expect(page.getByRole('button', { name: copy.common.close }).last()).toBeVisible()
    await page.getByRole('button', { name: copy.common.close }).last().click()
    await expect(page.getByRole('dialog')).toHaveCount(1)
    await dialog.getByRole('button', { name: copy.common.close }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)

    // The row now carries the photo (the control opens the viewer).
    await expect(card.getByRole('button', { name: copy.receipts.rowControlAria(true) })).toBeVisible()

    // Hapus while the photo is still on it: refused, with the fix named, and
    // the claim stays.
    await removeClaim(card, page)
    await expect(page.getByRole('alert')).toContainText(copy.reimbursements.errors.referenced_by_other_records)
    await expect(card).toBeVisible()

    // Delete the photo behind its own confirm.
    await card.getByRole('button', { name: copy.receipts.rowControlAria(true) }).click()
    await expect(dialog).toBeVisible()
    await choosePhotoAction(page, dialog, copy.receipts.delete)
    await expect(dialog.getByText(copy.receipts.deleteConfirm)).toBeVisible()
    await dialog.getByRole('button', { name: copy.receipts.delete }).click()
    await expect(dialog.getByText(copy.receipts.emptyReimbursement)).toBeVisible()
    await expect(photo).toHaveCount(0)
    await dialog.getByRole('button', { name: copy.common.close }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(card.getByRole('button', { name: copy.receipts.rowControlAria(false) })).toBeVisible()
  })

  test('Hapus removes a claim entered by mistake, and Beranda owes less', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openTalangan(page)
    const card = claim(page, salah)

    // Cancelled, the claim stays.
    await chooseHapus(card, page)
    await card.getByRole('button', { name: copy.reimbursements.settle.cancel }).click()
    await expect(card).toBeVisible()

    await removeClaim(card, page)
    await expect(page.getByText(copy.reimbursements.delete.success)).toBeVisible()
    await expect(card).toHaveCount(0)
    await expect(claim(page, terpal)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.home }).click()
    const owed = page.getByRole('button', { name: new RegExp(copy.home.owedToMembers) })
    await expect(owed).toContainText(formatIDR(40_000))
  })

  // The report is public, so the URL is read from the logged-in Pengaturan
  // card and then navigated like any reader would.
  let reportPath = ''

  test('create a Titipan in Pengaturan; it starts at zero on Beranda', async ({ page }) => {
    await logIn(page)
    await page.goto('/settings')
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()
    await page.getByRole('button', { name: copy.settings.passThrough.add }).click()
    const dialog = page.getByRole('dialog', { name: copy.settings.passThrough.add })
    await dialog.getByLabel(copy.settings.passThrough.nameLabel).fill(titipan)
    await dialog.getByRole('button', { name: copy.settings.passThrough.add }).click()
    await expect(dialog).toBeHidden()
    await expect(page.getByRole('button', { name: copy.settings.passThrough.editAria(titipan) })).toBeVisible()

    const card = page.locator('section').filter({ has: page.getByRole('heading', { name: copy.settings.report.heading }) })
    const href = await card.getByRole('link', { name: copy.settings.report.open }).getAttribute('href')
    expect(href).toMatch(/\/report\/[A-Za-z0-9]{22,}$/)
    reportPath = new URL(href ?? '').pathname

    await page.goto('/')
    await expect(page.getByText(copy.home.purposeBreakdownHeading)).toBeVisible()
    await expect(berandaRow(page, titipan)).toContainText(formatIDR(0))
  })

  test('receive money for it, then forward part of it: its own pos moves, the fund total follows', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    const fundTotal = async () => (await api<{ fund_total: number }>(page.context().request, 'GET', '/api/balances')).fund_total

    // The kas stands at Rp 1.000.000 less the settled Rp 9.000 claim.
    // Masuk: held for someone else, but it counts in the kas while it sits there.
    await recordForPos(page, 'in', titipan, 250_000)
    await expect(berandaRow(page, titipan)).toContainText(formatIDR(250_000))
    expect(await fundTotal()).toBe(1_241_000)

    // Keluar: forwarded on.
    await recordForPos(page, 'out', titipan, 100_000)
    await expect(berandaRow(page, titipan)).toContainText(formatIDR(150_000))
    expect(await fundTotal()).toBe(1_141_000)
  })

  test('the public report lists the Titipan as its own pos', async ({ page }) => {
    await page.goto(reportPath)
    const pos = reportPos(page)
    const line = pos.getByRole('listitem').filter({ has: page.getByText(titipan, { exact: true }) })
    await expect(line).toHaveCount(1)
    await expect(line).toContainText(formatIDR(150_000))
  })

  test('rename it: the new name shows in Pengaturan, Beranda and the report, the money stays', async ({ page }) => {
    await logIn(page)
    await page.goto('/settings')
    await page.getByRole('button', { name: copy.settings.passThrough.editAria(titipan) }).click()
    const dialog = page.getByRole('dialog', { name: copy.settings.passThrough.editTitle })
    await dialog.getByLabel(copy.settings.passThrough.nameLabel).fill(titipanRenamed)
    await dialog.getByRole('button', { name: copy.settings.passThrough.save }).click()
    await expect(dialog).toBeHidden()
    await expect(page.getByRole('button', { name: copy.settings.passThrough.editAria(titipanRenamed) })).toBeVisible()
    await expect(page.getByRole('button', { name: copy.settings.passThrough.editAria(titipan) })).toHaveCount(0)

    await page.goto('/')
    await expect(berandaRow(page, titipanRenamed)).toContainText(formatIDR(150_000))
    await expect(berandaRow(page, titipan)).toHaveCount(0)

    await page.goto(reportPath)
    const pos = reportPos(page)
    const line = pos.getByRole('listitem').filter({ has: page.getByText(titipanRenamed, { exact: true }) })
    await expect(line).toContainText(formatIDR(150_000))
    await expect(pos.getByText(titipan, { exact: true })).toHaveCount(0)
  })
})
