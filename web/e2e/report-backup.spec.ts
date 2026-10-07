import { readFileSync } from 'node:fs'

import type { APIRequestContext, Locator, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, logIn, seedPassword, test } from './fixtures'

// M8.11 (#442): the public report's month walk, its pos / anggota / status
// iuran filters, its PDF, and Salin / Bagikan on the Laporan publik card;
// then Cadangan's refusals (a wrong password, a file that is no backup), the
// "Harian" row the server wrote on its own, and a server-side row's download.
//
// report.spec.ts keeps the bare link, the Jenis filter, the 404 and the new
// link; restore.spec.ts keeps both restores. Nothing here restores anything:
// the point of the two refusals is that the fund is exactly as it was.
//
// The report is server-rendered Go, so its strings are not in copy/id.ts; each
// literal below names the reportText field in internal/http/report_copy.go it
// mirrors. Serial, one story on the seeded fixture (Tunai Rp 1.000.000, Kas
// Utama, Warga Satu and Warga Dua on Reguler Rp 50.000, one open envelope).
// Rows are seeded over the API after logging in; the report itself is public.

const envelope = 'Amplop Uji Laporan'

function jakartaToday(): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Jakarta' }).format(new Date())
}

// YYYY-MM, offset months from the running one, in Asia/Jakarta.
function jakartaMonth(offset = 0): string {
  const [y, m] = jakartaToday().split('-').map(Number)
  const d = new Date(Date.UTC(y, m - 1 + offset, 1))
  return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`
}

type Named = { id: number; name: string }

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

function txns(page: Page) {
  return page.locator('#transactions ul.txns li')
}

function txn(page: Page, note: string) {
  return txns(page).filter({ hasText: note })
}

async function openSettings(page: Page) {
  await page.goto('/')
  await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
  await page.getByRole('link', { name: copy.shell.nav.settings }).click()
  await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()
}

function reportCard(page: Page) {
  return page.locator('section').filter({ has: page.getByRole('heading', { name: copy.settings.report.heading }) })
}

async function fileHead(download: { path: () => Promise<string> }, bytes: number) {
  return readFileSync(await download.path()).subarray(0, bytes)
}

test.describe('report and backup', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const backupText = copy.settings.backup
  const confirmText = copy.restoreConfirm

  const prevMonth = jakartaMonth(-1)
  const thisMonth = jakartaMonth()
  let reportPath = ''

  test('seed two months of rows and read the report link', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const today = jakartaToday()
    const tunai = await byName(request, '/api/accounts', 'accounts', 'Tunai')
    const main = await byName(request, '/api/purposes', 'purposes', 'Kas Utama')
    const amplop = await byName(request, '/api/purposes', 'purposes', envelope)
    const wargaSatu = await byName(request, '/api/members', 'members', 'Warga Satu')
    const wargaDua = await byName(request, '/api/members', 'members', 'Warga Dua')

    const post = (purpose: number, direction: 'in' | 'out', amount: number, occurred_on: string, note: string, member_id?: number) =>
      api(request, 'POST', '/api/transactions', {
        account_id: tunai,
        purpose_id: purpose,
        direction,
        amount,
        occurred_on,
        note,
        ...(member_id ? { member_id } : {}),
      })

    // Last month: one in, one out, so there is a month to step back to.
    await post(main, 'in', 120_000, `${prevMonth}-15`, 'Sumbangan bulan lalu')
    await post(main, 'out', 30_000, `${prevMonth}-16`, 'Beli lampu bulan lalu')
    // This month: an expense with no member, a named gift to the amplop, and
    // Warga Satu's dues - so each filter keeps a different row.
    await post(main, 'out', 25_000, today, 'Beli sapu bulan ini')
    await post(amplop, 'in', 77_000, today, 'Sumbangan amplop bulan ini', wargaDua)
    await api(request, 'POST', '/api/dues-payments', {
      account_id: tunai,
      purpose_id: main,
      member_id: wargaSatu,
      occurred_on: today,
      note: null,
      periods: [{ dues_period: thisMonth, amount: 50_000 }],
    })

    await openSettings(page)
    const href = await reportCard(page).getByRole('link', { name: copy.settings.report.open }).getAttribute('href')
    expect(href).toMatch(/\/report\/[A-Za-z0-9]{22,}$/)
    reportPath = new URL(href ?? '').pathname
  })

  test('steps to last month and back, and picks a month from the list', async ({ page }) => {
    await page.goto(reportPath)
    const months = page.locator('#months')
    // The bare link is the running month; with a past month to go back to,
    // "Bulan sebelumnya" is there and "Bulan berikutnya" is not.
    await expect(page.locator('header p.muted')).toContainText('Bulan berjalan')
    await expect(months.getByRole('link', { name: 'Bulan berikutnya' })).toHaveCount(0)
    await expect(txn(page, 'Beli sapu bulan ini')).toHaveCount(1)

    // reportText.MonthPrev / MonthNext
    await months.getByRole('link', { name: 'Bulan sebelumnya' }).click()
    await expect(page).toHaveURL(new RegExp(`[?&]month=${prevMonth}`))
    await expect(months.locator('select#month')).toHaveValue(prevMonth)
    await expect(txn(page, 'Sumbangan bulan lalu')).toHaveCount(1)
    await expect(txn(page, 'Beli lampu bulan lalu')).toHaveCount(1)
    await expect(txn(page, 'Beli sapu bulan ini')).toHaveCount(0)
    await expect(page.locator('header p.muted')).not.toContainText('Bulan berjalan')

    await months.getByRole('link', { name: 'Bulan berikutnya' }).click()
    await expect(months.locator('select#month')).toHaveValue(thisMonth)
    await expect(txn(page, 'Beli sapu bulan ini')).toHaveCount(1)

    // The month list submits on change, like every select on the page.
    await months.locator('select#month').selectOption(prevMonth)
    await expect(page).toHaveURL(new RegExp(`[?&]month=${prevMonth}`))
    await expect(txn(page, 'Sumbangan bulan lalu')).toHaveCount(1)
  })

  test('filters this month by pos, anggota and status iuran', async ({ page }) => {
    await page.goto(`${reportPath}?month=${thisMonth}`)
    const transactions = page.locator('#transactions')

    // reportText.FilterPurpose: the pos filter keeps the amplop's gift only.
    await transactions.locator('select[name="purpose"]').selectOption({ label: envelope })
    await expect(page).toHaveURL(/[?&]purpose=/)
    await expect(txns(page)).toHaveCount(1)
    await expect(txn(page, 'Sumbangan amplop bulan ini')).toHaveCount(1)
    await expect(transactions.locator('select[name="purpose"]')).toHaveValue(/\d+/)

    // reportText.FilterMember: from the pos filter to a member's rows. The
    // pos stays chosen, so clear it first by going back to the bare month.
    await page.goto(`${reportPath}?month=${thisMonth}`)
    await transactions.locator('select[name="member"]').selectOption({ label: 'Warga Satu' })
    await expect(page).toHaveURL(/[?&]member=/)
    await expect(txns(page)).toHaveCount(1)
    await expect(txns(page).first()).toContainText('Warga Satu')
    await expect(txn(page, 'Beli sapu bulan ini')).toHaveCount(0)

    await page.goto(`${reportPath}?month=${thisMonth}`)
    await transactions.locator('select[name="member"]').selectOption({ label: 'Warga Dua' })
    await expect(txns(page)).toHaveCount(1)
    await expect(txn(page, 'Sumbangan amplop bulan ini')).toHaveCount(1)

    // reportText.DuesFilter: Warga Satu paid this month, Warga Dua did not.
    await page.goto(`${reportPath}?month=${thisMonth}`)
    const dues = page.locator('#dues')
    const rows = dues.locator('ul.dues li')
    await expect(rows.filter({ hasText: 'Warga Satu' })).toHaveCount(1)
    await expect(rows.filter({ hasText: 'Warga Dua' })).toHaveCount(1)

    await dues.getByLabel('Status iuran').selectOption({ label: 'Lunas' })
    await expect(page).toHaveURL(/[?&]dues=paid/)
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText('Warga Satu')
    // The filter is on the dues list only: every transaction is still there.
    await expect(txn(page, 'Beli sapu bulan ini')).toHaveCount(1)
    await expect(txn(page, 'Sumbangan amplop bulan ini')).toHaveCount(1)

    await page.goto(`${reportPath}?month=${thisMonth}`)
    await dues.getByLabel('Status iuran').selectOption({ label: 'Belum bayar' })
    await expect(page).toHaveURL(/[?&]dues=unpaid/)
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText('Warga Dua')
  })

  test('downloads the month as a PDF from the report page', async ({ page }) => {
    await page.goto(`${reportPath}?month=${prevMonth}`)
    const downloadEvent = page.waitForEvent('download')
    await page.getByRole('link', { name: 'Unduh PDF' }).click()
    const download = await downloadEvent

    // reportText.PDFFilename
    expect(download.suggestedFilename()).toMatch(/^laporan-kas-\d{4}-\d{2}\.pdf$/)
    expect(download.suggestedFilename()).toContain(prevMonth)
    expect((await fileHead(download, 5)).toString('latin1')).toBe('%PDF-')
    expect(readFileSync(await download.path()).length).toBeGreaterThan(1_000)
  })

  test('downloads the current month as a PDF from Pengaturan', async ({ page }) => {
    await logIn(page)
    await openSettings(page)
    const card = reportCard(page)
    await expect(card.getByRole('button', { name: copy.settings.report.downloadPdf })).toBeEnabled()

    const downloadEvent = page.waitForEvent('download')
    await card.getByRole('button', { name: copy.settings.report.downloadPdf }).click()
    const download = await downloadEvent

    expect(download.suggestedFilename()).toBe(`laporan-kas-${thisMonth}.pdf`)
    expect((await fileHead(download, 5)).toString('latin1')).toBe('%PDF-')
    expect(readFileSync(await download.path()).length).toBeGreaterThan(1_000)
  })

  test('Salin copies the link and says so', async ({ page }) => {
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
    await logIn(page)
    await openSettings(page)
    const card = reportCard(page)
    const href = await card.getByRole('link', { name: copy.settings.report.open }).getAttribute('href')

    await card.getByRole('button', { name: copy.settings.report.copy, exact: true }).click()
    await expect(card.getByRole('button', { name: copy.settings.report.copied, exact: true })).toBeVisible()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(href)
    // It reads Salin again after a moment.
    await expect(card.getByRole('button', { name: copy.settings.report.copy, exact: true })).toBeVisible()
  })

  test('Bagikan is absent without a share sheet, and hands the link to one when there is', async ({ page }) => {
    await logIn(page)
    await openSettings(page)
    const card = reportCard(page)
    const href = await card.getByRole('link', { name: copy.settings.report.open }).getAttribute('href')

    // Headless Chromium has no navigator.share: the button is not rendered.
    expect(await page.evaluate(() => typeof navigator.share)).toBe('undefined')
    await expect(card.getByRole('button', { name: copy.settings.report.share })).toHaveCount(0)

    // A phone has one. The stub records what the button hands it.
    await page.addInitScript(() => {
      const calls: unknown[] = []
      ;(window as unknown as { __shared: unknown[] }).__shared = calls
      navigator.share = (data?: ShareData) => {
        calls.push(data)
        return Promise.resolve()
      }
    })
    await page.reload()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()
    await card.getByRole('button', { name: copy.settings.report.share }).click()
    await expect
      .poll(() => page.evaluate(() => (window as unknown as { __shared: unknown[] }).__shared))
      .toEqual([{ title: copy.settings.report.shareTitle, url: href }])
  })

  test('a wrong password is refused with the app copy, and nothing is restored', async ({ page }) => {
    await logIn(page)
    await openSettings(page)

    const downloadEvent = page.waitForEvent('download')
    await page.getByRole('button', { name: backupText.download, exact: true }).click()
    const zipPath = await (await downloadEvent).path()

    await page.getByLabel(backupText.restoreLabel).and(page.locator('input[type="file"]')).setInputFiles(zipPath)
    const dialog = page.getByRole('dialog', { name: confirmText.heading })
    await expect(dialog.getByLabel(confirmText.passwordLabel)).toBeVisible()
    await dialog.getByLabel(confirmText.passwordLabel).fill(seedPassword + '-salah')
    await dialog.getByRole('button', { name: confirmText.confirm, exact: true }).click()

    await expect(dialog.getByRole('alert')).toHaveText(copy.common.errors.invalid_credentials)
    // Still the same page, same session: no reload, no login screen, and the
    // password field is there for another try.
    await expect(dialog.getByLabel(confirmText.passwordLabel)).toBeVisible()
    await dialog.getByRole('button', { name: confirmText.cancel }).click()
    await expect(dialog).not.toBeVisible()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()

    // A restore writes a safety-net dump first; there is none.
    const preRestorePrefix = backupText.downloadRowAria(backupText.kindPreRestore, '').trim()
    await expect(page.getByRole('button', { name: new RegExp(`^${preRestorePrefix}`) })).toHaveCount(0)
  })

  test('a file that is not a backup is refused with the app copy, and nothing is restored', async ({ page }) => {
    await logIn(page)
    await openSettings(page)

    await page
      .getByLabel(backupText.restoreLabel)
      .and(page.locator('input[type="file"]'))
      .setInputFiles({ name: 'bukan-cadangan.zip', mimeType: 'application/zip', buffer: Buffer.from('this is not a zip file at all') })

    const dialog = page.getByRole('dialog', { name: confirmText.heading })
    await expect(dialog.getByRole('alert')).toHaveText(copy.common.errors.restore_invalid_file)
    // No preview, so no password field and nothing to confirm.
    await expect(dialog.getByLabel(confirmText.passwordLabel)).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(dialog).not.toBeVisible()

    const preRestorePrefix = backupText.downloadRowAria(backupText.kindPreRestore, '').trim()
    await expect(page.getByRole('button', { name: new RegExp(`^${preRestorePrefix}`) })).toHaveCount(0)
  })

  test('the server wrote a Harian backup on its own, and its row downloads as a zip', async ({ page }) => {
    await logIn(page)
    await openSettings(page)

    // The boot dump (EnsureBootDump): a fresh instance has no dump in the
    // current format, so starting the server wrote one of kind daily.
    const dailyPrefix = backupText.downloadRowAria(backupText.kindDaily, '').trim()
    const row: Locator = page.getByRole('button', { name: new RegExp(`^${dailyPrefix}`) })
    await expect(row.first()).toBeVisible()

    const downloadEvent = page.waitForEvent('download')
    await row.first().click()
    const download = await downloadEvent

    expect(download.suggestedFilename()).toMatch(/^uruni-\d{8}-\d{6}-daily-fv\d+-[0-9a-f]{12}\.zip$/)
    expect((await fileHead(download, 2)).toString('latin1')).toBe('PK')
    expect(readFileSync(await download.path()).length).toBeGreaterThan(100)
  })
})
