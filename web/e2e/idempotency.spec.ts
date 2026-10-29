import type { APIRequestContext } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, logIn, test } from './fixtures'

// A save whose answer is lost, then Simpan again, makes one row (ADR-043).
//
// Simpan is disabled while a write is in flight, so a double tap cannot be driven.
// The real journey: the request commits, its answer never comes back, she taps
// again. `setOffline` cannot stand in (an offline request never arrives), so
// route.fetch() lets it through and route.abort() drops the answer. The second
// tap carries the same Idempotency-Key and gets the first result, marked
// Idempotent-Replayed. Serial; rows are read over the API.

type Balances = { fund_total: number }
type Row = { id: number; note: string | null; amount: number }
type TransactionsPage = { transactions: Row[] }

async function get<T>(request: APIRequestContext, path: string): Promise<T> {
  const response = await request.get(path)
  expect(response.ok(), `GET ${path} answered ${response.status()}: ${await response.text()}`).toBe(true)
  return (await response.json()) as T
}

test.describe('a lost response, then Simpan again', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const note = 'Parkir balai warga'
  const amount = 63_000

  test('makes one row, and the second answer is the first one replayed', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const before = await get<Balances>(request, '/api/balances')

    // The write reaches the server and commits; only its answer is dropped, once.
    let dropped = false
    await page.route('**/api/transactions', async (route) => {
      if (route.request().method() !== 'POST' || dropped) return route.fallback()
      dropped = true
      await route.fetch()
      await route.abort('connectionreset')
    })

    const keys: (string | undefined)[] = []
    page.on('request', (req) => {
      if (req.method() === 'POST' && new URL(req.url()).pathname === '/api/transactions') keys.push(req.headers()['idempotency-key'])
    })

    await page.goto('/record')
    await expect(page.getByRole('heading', { name: copy.record.headingOut })).toBeVisible()
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(amount))
    await page.getByLabel(copy.record.noteLabel, { exact: true }).fill(note)
    await page.getByRole('button', { name: copy.record.submit }).click()

    await expect(page.getByRole('alert').filter({ hasText: copy.common.errors.write_outcome_unknown })).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeHidden()
    expect(dropped).toBe(true)

    const landed = await get<TransactionsPage>(request, '/api/transactions')
    expect(landed.transactions.filter((row) => row.note === note)).toHaveLength(1)
    expect((await get<Balances>(request, '/api/balances')).fund_total).toBe(before.fund_total - amount)

    // She taps Simpan again, with nothing changed. The answer comes back this
    // time, as the first one's result.
    const replay = page.waitForResponse((res) => res.request().method() === 'POST' && new URL(res.url()).pathname === '/api/transactions')
    await page.getByRole('button', { name: copy.record.submit }).click()
    const second = await replay
    expect(second.status()).toBe(201)
    expect(second.headers()['idempotent-replayed']).toBe('true')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()

    // The same key both times, and still one row, the fund down once.
    expect(keys).toHaveLength(2)
    expect(keys[0]).toMatch(/^[0-9a-f]{32}$/)
    expect(keys[1]).toBe(keys[0])
    const after = await get<TransactionsPage>(request, '/api/transactions')
    expect(after.transactions.filter((row) => row.note === note)).toHaveLength(1)
    expect((await get<Balances>(request, '/api/balances')).fund_total).toBe(before.fund_total - amount)
  })
})
