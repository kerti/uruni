import type { APIRequestContext, Locator, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIsoDate } from '../src/lib/dates'
import { expect, logIn, test } from './fixtures'

// Reversing an ordinary transaction (ADR-042): one door, the entry detail's
// Batalkan transaksi, which hands off to the form under the row; the pair then
// reads as linked in Riwayat and on the public report.
//
// Two rows, one on each side of a cek kas, because the form's date line is the
// ledger's own rule stated up front: a row no count has covered is reversed on
// its own day (Dicatat pada ...), one a count has covered is reversed today
// (Dicatat hari ini, ...). Everything is seeded over the API except the
// journey itself. Serial, one story on the seeded fixture (Tunai, Kas Utama).

const t = copy.history.transactions

type Named = { id: number; name: string }

// The day the app reads everything in: Asia/Jakarta, the fund's zone until one
// is set (ADR-041).
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
  const row = (Array.isArray(body) ? body : body[key]).find((r) => r.name === name)
  if (!row) throw new Error(`${name} not found in GET ${path}`)
  return row.id
}

function row(page: Page, note: string): Locator {
  return page
    .getByRole('main')
    .getByRole('listitem')
    .filter({ has: page.getByText(note, { exact: true }) })
}

async function openForm(page: Page, note: string) {
  await row(page, note)
    .getByRole('button', { name: /^Lihat rincian:/ })
    .click()
  await page.getByRole('dialog', { name: copy.transactionDetail.heading }).getByRole('button', { name: t.reverse }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
}

test.describe('reversing an ordinary transaction', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const counted = 'Kopi salah arah'
  const uncounted = 'Voucher salah arah'
  const today = jakartaToday()
  // Always on or before today and in the same month, so the report's running
  // month holds both pairs.
  const firstOfMonth = `${today.slice(0, 7)}-01`

  test('seed: a spend a cek kas has counted, then a coupon no count has covered', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const tunai = await byName(request, '/api/accounts', 'accounts', 'Tunai')
    const main = await byName(request, '/api/purposes', 'purposes', 'Kas Utama')
    const post = (direction: 'in' | 'out', amount: number, note: string, occurredOn: string) =>
      api(request, 'POST', '/api/transactions', {
        account_id: tunai,
        purpose_id: main,
        direction,
        amount,
        occurred_on: occurredOn,
        note,
      })

    await post('out', 40_000, counted, today)
    const balances = await api<{ accounts: { id: number; balance: number }[] }>(request, 'GET', '/api/balances')
    await api(request, 'POST', '/api/reconciliations', {
      counts: balances.accounts.map((a) => ({ account_id: a.id, actual_amount: a.balance, resolution: 'matched' })),
    })
    // Posted after the count, so the count does not cover it.
    await post('in', 25_000, uncounted, firstOfMonth)
  })

  test('a row no cek kas has counted: the form says it is recorded on its own day, and the pair reads linked', async ({ page }) => {
    await logIn(page)
    await page.goto('/history')
    await expect(row(page, uncounted)).toBeVisible()
    await expect(row(page, uncounted).getByRole('button', { name: t.reverse })).toHaveCount(0)

    await openForm(page, uncounted)
    const form = row(page, uncounted)
    await expect(form.getByText(t.reverseExplainer)).toBeVisible()
    await expect(form.getByText(t.reverseDateOriginal(formatIsoDate(firstOfMonth)))).toBeVisible()
    await form.getByRole('button', { name: t.reverseConfirm }).click()

    await expect(page.getByText(t.reverseSuccess)).toBeVisible()
    await expect(row(page, uncounted).getByText(copy.dues.history.reversedBadge)).toBeVisible()
    await expect(page.getByText(copy.rowLabels.unnamedReversal.text(formatIsoDate(firstOfMonth))).first()).toBeVisible()

    await row(page, uncounted)
      .getByRole('button', { name: /^Lihat rincian:/ })
      .click()
    const dialog = page.getByRole('dialog', { name: copy.transactionDetail.heading })
    await expect(dialog.getByText(copy.dues.history.reversedBadge)).toBeVisible()
    await expect(dialog.getByRole('button', { name: t.reverse })).toHaveCount(0)
  })

  test('a row a cek kas has counted: the form says it is recorded today', async ({ page }) => {
    await logIn(page)
    await page.goto('/history')

    await openForm(page, counted)
    const form = row(page, counted)
    await expect(form.getByText(t.reverseDateToday)).toBeVisible()
    await expect(form.getByText(/^Dicatat pada/)).toHaveCount(0)
    await form.getByLabel(t.reverseNoteLabel).fill('Salah arah')
    await form.getByRole('button', { name: t.reverseConfirm }).click()

    await expect(page.getByText(t.reverseSuccess)).toBeVisible()
    await expect(row(page, counted).getByText(copy.dues.history.reversedBadge)).toBeVisible()
  })

  test('the public report reads both pairs as linked', async ({ page }) => {
    await logIn(page)
    const fund = await api<{ report_slug: string }>(page.context().request, 'GET', '/api/fund')

    await page.goto(`/report/${fund.report_slug}`)
    // reportText.RowReversal and reportText.RowReversed (report_copy.go): two
    // originals marked, two reversals naming the day they reverse.
    await expect(page.getByText('Sudah dibatalkan', { exact: true })).toHaveCount(2)
    await expect(page.getByText(`Pembatalan \u00b7 transaksi ${formatIsoDate(firstOfMonth)}`).first()).toBeVisible()
    await expect(page.getByText(`Pembatalan \u00b7 transaksi ${formatIsoDate(today)}`).first()).toBeVisible()
  })
})
