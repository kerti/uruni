import type { APIRequestContext, Locator, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'

// M8.12 (#443): what only a real browser can say about the app's
// non-functional promises, on the real binary with the built SPA embedded
// (so the real service worker from vite-plugin-pwa is what runs).
//
//   - Offline is unavailable: the banner shows mid-session, a write attempt
//     fails visibly and nothing is queued or posted later (CLAUDE.md rule 4).
//   - Installable PWA: the manifest is linked and parses, the service worker
//     registers and activates, and the precache holds the shell and no /api
//     or /report response.
//   - Phone-first layout: one column of at most 576px, no horizontal scroll,
//     on every main screen at 390px; at 800px the column is exactly 576px,
//     centred, with the header and the footer's buttons inside it.
//   - Beranda's Arus kas totals, and a negative pos shown plainly.
//
// Serial, one story on the seeded fixture (Tunai Rp 1.000.000, Kas Utama,
// the open envelope "Amplop Uji Laporan"). Money is seeded over the API.

const envelope = 'Amplop Uji Laporan'

type Named = { id: number; name: string }
type Balances = { fund_total: number; month: { in: number; out: number } }
type TransactionsPage = { transactions: unknown[]; nextCursor: string | null }

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

async function post(request: APIRequestContext, accountId: number, purposeId: number, direction: 'in' | 'out', amount: number) {
  await api(request, 'POST', '/api/transactions', {
    account_id: accountId,
    purpose_id: purposeId,
    direction,
    amount,
    occurred_on: jakartaToday(),
  })
}

// Beranda's Arus kas card, and one of its two figures.
function cashflow(page: Page) {
  return page.locator('section').filter({ has: page.getByRole('heading', { name: copy.home.cashflowHeading }) })
}
function figure(page: Page, label: string) {
  return cashflow(page).getByText(label, { exact: true }).locator('..')
}

function berandaRow(page: Page, name: string) {
  return page.getByRole('button', { name: new RegExp(`^${name}`) })
}

function offlineBanner(page: Page) {
  return page.getByRole('status').filter({ hasText: copy.common.offlineBanner })
}

async function box(locator: Locator) {
  const b = await locator.boundingBox()
  if (!b) throw new Error('element has no layout box')
  return b
}

// Every screen reachable from the footer, plus the ones it opens: the status
// matrix, Cek kas, the amplop list and the tarif screen.
const screens = [
  '/',
  '/history/transactions',
  '/history/dues',
  '/history/reimbursements',
  '/history/reconciliations',
  '/record',
  '/members',
  '/settings',
  '/dues',
  '/reconcile',
  '/incidentals',
  '/dues-tiers',
]

async function openScreen(page: Page, path: string) {
  await page.goto(path)
  await expect(page.getByRole('navigation', { name: copy.shell.nav.label })).toBeVisible()
  await page.waitForLoadState('networkidle')
}

// What the Shell promises on `path`, measured on real elements: no sideways
// scroll, and the header, the main column and the footer's buttons all sit
// in one column. At 390px that column is the viewport minus its 16px gutters;
// wider, it stops at 576px (max-w-xl) and centres.
async function expectOneColumn(page: Page, path: string) {
  const viewport = page.viewportSize()
  if (!viewport) throw new Error('no viewport')
  const where = `${path} at ${viewport.width}px`

  const overflow = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }))
  expect(overflow.scroll, `${where}: scrollWidth ${overflow.scroll} > clientWidth ${overflow.client}`).toBeLessThanOrEqual(overflow.client)

  const column = await box(page.getByRole('main').locator('> div'))
  const header = await box(page.getByRole('banner').locator('> div'))
  const nav = await box(page.getByRole('navigation', { name: copy.shell.nav.label }).locator('ul'))

  expect(column.width, `${where}: column ${column.width}px`).toBeLessThanOrEqual(576.5)
  if (viewport.width <= 576) {
    // The whole width less the gutters.
    expect(column.width, `${where}: column ${column.width}px`).toBeGreaterThanOrEqual(viewport.width - 32 - 1)
  } else {
    expect(column.width, `${where}: column ${column.width}px`).toBeCloseTo(576, 0)
    // Centred: as much room on the left as on the right.
    expect(Math.abs(column.x - (viewport.width - (column.x + column.width))), `${where}: column not centred`).toBeLessThanOrEqual(1)
    // The header's contents and the bar's buttons share the main column
    // (the bars' glass spans the screen; their contents do not).
    for (const [name, b] of [
      ['header', header],
      ['footer', nav],
    ] as const) {
      expect(Math.abs(b.x - column.x), `${where}: ${name} left edge`).toBeLessThanOrEqual(1)
      expect(Math.abs(b.width - 576), `${where}: ${name} width ${b.width}px`).toBeLessThanOrEqual(1)
    }
  }
  expect(column.x).toBeGreaterThanOrEqual(0)
  expect(column.x + column.width).toBeLessThanOrEqual(viewport.width + 0.5)

  // The footer's five destinations sit inside that column, each a thumb-size
  // target (Design-System.md: 44x44 minimum).
  const links = page.getByRole('navigation', { name: copy.shell.nav.label }).getByRole('link')
  expect(await links.count()).toBe(5)
  for (let i = 0; i < 5; i++) {
    const b = await box(links.nth(i))
    expect(b.height, `${where}: footer link ${i} is ${b.height}px tall`).toBeGreaterThanOrEqual(44)
    expect(b.width, `${where}: footer link ${i} is ${b.width}px wide`).toBeGreaterThanOrEqual(44)
    expect(b.x, `${where}: footer link ${i} left`).toBeGreaterThanOrEqual(nav.x - 1)
    expect(b.x + b.width, `${where}: footer link ${i} right`).toBeLessThanOrEqual(nav.x + nav.width + 1)
  }
  const logout = await box(page.getByRole('button', { name: copy.shell.logout, exact: true }))
  expect(logout.width, `${where}: logout is ${logout.width}px wide`).toBeGreaterThanOrEqual(44)
  expect(logout.height, `${where}: logout is ${logout.height}px tall`).toBeGreaterThanOrEqual(44)
  expect(logout.x + logout.width, `${where}: logout inside the header`).toBeLessThanOrEqual(header.x + header.width + 1)
}

test.describe('browser-only non-functionals', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('offline mid-session shows the banner, a write fails and nothing is queued', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const before = await api<Balances>(request, 'GET', '/api/balances')
    const rowsBefore = (await api<TransactionsPage>(request, 'GET', '/api/transactions')).transactions.length

    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(offlineBanner(page)).toBeHidden()

    // The connection drops under an open session.
    await page.context().setOffline(true)
    await expect(offlineBanner(page)).toBeVisible()

    // Catat is a client-side route, but the form loads its locations and
    // pos from the API, so offline it shows the same "butuh koneksi" state
    // with a retry instead of a form.
    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    const loadFailed = page.getByRole('main').getByRole('alert').filter({ hasText: copy.common.errors.network_error })
    await expect(loadFailed).toBeVisible()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeHidden()

    // Back online, the retry brings the form; she fills it in, and the
    // connection drops again just before she taps Simpan.
    await page.context().setOffline(false)
    await expect(offlineBanner(page)).toBeHidden()
    await loadFailed.getByRole('button', { name: copy.common.retry }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill('77000')
    await page.context().setOffline(true)
    await page.getByRole('button', { name: copy.record.submit }).click()

    // It fails visibly: no success message, still on the form, still offline.
    await expect(page.getByRole('alert').filter({ hasText: copy.common.errors.network_error })).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeHidden()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
    await expect(offlineBanner(page)).toBeVisible()

    // Back online: the banner clears on its own.
    await page.context().setOffline(false)
    await expect(offlineBanner(page)).toBeHidden()

    // Nothing was queued: even after a reload and a wait, the ledger holds
    // exactly what it held, and the balances have not moved.
    await page.reload()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
    await page.waitForLoadState('networkidle')
    const after = await api<Balances>(request, 'GET', '/api/balances')
    expect(after.fund_total).toBe(before.fund_total)
    expect(after.month).toEqual(before.month)
    expect((await api<TransactionsPage>(request, 'GET', '/api/transactions')).transactions.length).toBe(rowsBefore)
  })

  test('the manifest is linked and parses, and the service worker activates', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    const href = await page.locator('link[rel="manifest"]').getAttribute('href')
    expect(href).toBeTruthy()
    const response = await page.context().request.get(href as string)
    expect(response.ok()).toBe(true)
    const manifest = (await response.json()) as {
      name: string
      start_url: string
      display: string
      icons: { src: string; sizes: string; type: string }[]
    }
    expect(manifest.name).toBe('Uruni')
    expect(manifest.start_url).toBe('/')
    expect(manifest.display).toBe('standalone')
    expect(manifest.icons.map((i) => i.sizes)).toEqual(expect.arrayContaining(['192x192', '512x512']))
    for (const icon of manifest.icons) {
      const res = await page.context().request.get(icon.src)
      expect(res.ok(), `${icon.src} answered ${res.status()}`).toBe(true)
      expect(res.headers()['content-type']).toContain(icon.type)
    }

    // The worker registers and reaches "activated"; ready resolves only then.
    const state = await page.evaluate(async () => {
      const registration = await navigator.serviceWorker.ready
      return registration.active?.state ?? null
    })
    expect(state).toBe('activated')
  })

  test('the precache holds the shell and never an API or report response', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await page.evaluate(() => navigator.serviceWorker.ready)

    // Real API traffic first, from the page itself, so a cache that stored
    // responses would have something to hold. A reload brings the worker in
    // as controller, so the second round runs through it.
    for (let round = 0; round < 2; round++) {
      await page.reload()
      await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
      await page.goto('/history/transactions')
      await page.waitForLoadState('networkidle')
      await page.goto('/')
      await page.waitForLoadState('networkidle')
    }
    expect(await page.evaluate(() => navigator.serviceWorker.controller !== null)).toBe(true)

    const cached = await page.evaluate(async () => {
      const urls: string[] = []
      for (const name of await caches.keys()) {
        const cache = await caches.open(name)
        for (const req of await cache.keys()) urls.push(new URL(req.url).pathname)
      }
      return urls
    })
    expect(cached.length).toBeGreaterThan(0)
    expect(cached.some((p) => p.endsWith('/index.html') || p === '/')).toBe(true)
    expect(cached.some((p) => p.endsWith('.js'))).toBe(true)
    expect(cached.filter((p) => p.startsWith('/api/') || p.startsWith('/report') || p === '/healthz')).toEqual([])
  })

  test('one column at 390px on every main screen, no sideways scroll', async ({ page }) => {
    await logIn(page)
    expect(page.viewportSize()?.width).toBe(390)
    for (const path of screens) {
      await openScreen(page, path)
      await expectOneColumn(page, path)
    }
  })

  test('at 800px the column is exactly 576px, centred, and the bars sit inside it', async ({ page }) => {
    await logIn(page)
    await page.setViewportSize({ width: 800, height: 1000 })
    for (const path of screens) {
      await openScreen(page, path)
      await expectOneColumn(page, path)
    }
    await page.setViewportSize({ width: 390, height: 844 })
  })

  test("Arus kas shows the month's exact Masuk and Keluar", async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const tunai = await byName(request, '/api/accounts', 'accounts', 'Tunai')
    const main = await byName(request, '/api/purposes', 'purposes', 'Kas Utama')
    const before = await api<Balances>(request, 'GET', '/api/balances')

    await post(request, tunai, main, 'in', 200_000)
    await post(request, tunai, main, 'in', 50_000)
    await post(request, tunai, main, 'out', 30_000)
    await post(request, tunai, main, 'out', 20_000)

    const after = await api<Balances>(request, 'GET', '/api/balances')
    expect(after.month.in).toBe(before.month.in + 250_000)
    expect(after.month.out).toBe(before.month.out + 50_000)

    await page.goto('/')
    await expect(cashflow(page)).toBeVisible()
    await expect(figure(page, copy.home.monthIn)).toContainText(formatIDR(after.month.in))
    await expect(figure(page, copy.home.monthOut)).toContainText(formatIDR(after.month.out))
  })

  test('a negative pos shows its minus plainly in Saldo per pos', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const tunai = await byName(request, '/api/accounts', 'accounts', 'Tunai')
    const amplop = await byName(request, '/api/purposes', 'purposes', envelope)

    await page.goto('/')
    await expect(berandaRow(page, envelope)).toContainText(formatIDR(0))

    // The ledger never refuses an out larger than a pos holds (ADR-031), so
    // an empty envelope can be spent from.
    await post(request, tunai, amplop, 'out', 25_000)

    await page.reload()
    await expect(berandaRow(page, envelope)).toContainText(formatIDR(-25_000))
  })
})
