import type { APIRequestContext, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR, MAX_AMOUNT } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'

// The widest legal figure, Rp 999.999.999.999 (money.MaxAmount) and its
// negative, laid out on a phone. Seeds ceiling-sized amounts over the API -
// balances, Arus kas, a negative Titipan, a dues rate and payment, a Talangan,
// an envelope with a ceiling target and minimum, and a Cek kas left open - then
// walks the main screens at 390px and asserts that no page scrolls sideways and
// that every money figure is whole: on one line, inside the viewport, not
// clipped by an ancestor, never an ellipsis (a clipped number is a wrong
// number). Layout is measured on real elements, so it is a browser's job
// (Vitest has no layout).
//
// Serial, one story on the seeded fixture (Tunai Rp 1.000.000, Bank Uji Coba,
// Kas Utama, the envelope "Amplop Uji Laporan", two members on a tier).

const ceiling = MAX_AMOUNT
const widest = formatIDR(ceiling)

type Named = { id: number; name: string }

function jakartaToday(): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Jakarta' }).format(new Date())
}

async function api<T>(request: APIRequestContext, method: 'GET' | 'POST', path: string, data?: unknown): Promise<T> {
  const response = method === 'GET' ? await request.get(path) : await request.post(path, { data })
  expect(response.ok(), `${method} ${path} answered ${response.status()}: ${await response.text()}`).toBe(true)
  return (await response.json()) as T
}

async function list(request: APIRequestContext, path: string, key: string): Promise<Named[]> {
  const body = await api<Named[] | Record<string, Named[]>>(request, 'GET', path)
  return Array.isArray(body) ? body : body[key]
}

async function seedCeilingFigures(request: APIRequestContext) {
  const accounts = await list(request, '/api/accounts', 'accounts')
  const purposes = await list(request, '/api/purposes', 'purposes')
  const members = await list(request, '/api/members', 'members')
  const tiers = await list(request, '/api/dues-tiers', 'dues_tiers')
  const tunai = accounts.find((a) => a.name === 'Tunai')?.id ?? 0
  const bank = accounts.find((a) => a.name === 'Bank Uji Coba')?.id ?? 0
  const main = purposes.find((p) => p.name === 'Kas Utama')?.id ?? 0
  const today = jakartaToday()
  const post = (account_id: number, purpose_id: number, direction: 'in' | 'out', amount: number, note?: string) =>
    api(request, 'POST', '/api/transactions', { account_id, purpose_id, direction, amount, occurred_on: today, note })

  // Tunai holds the ceiling (its opening Rp 1.000.000 spent first), and the
  // fund total, Kas Utama and this month's Masuk run past it.
  await post(tunai, main, 'out', 1_000_000)
  await post(tunai, main, 'in', ceiling, 'Dana hibah besar sekali untuk uji lebar')
  // A Titipan below zero by the ceiling: a negative figure at full width.
  const titipan = await api<Named>(request, 'POST', '/api/pass-through-purposes', { name: 'Titipan Uji Lebar' })
  await post(bank, titipan.id, 'out', ceiling)
  // An envelope with a ceiling target and minimum, holding the ceiling.
  const amplop = await api<{ purpose_id: number }>(request, 'POST', '/api/incidentals', {
    occasion: 'Amplop Raksasa Untuk Uji',
    target_amount: ceiling,
    opened_on: today,
    minimum_per_member: ceiling,
    recipient_member_ids: [],
  })
  await post(tunai, amplop.purpose_id, 'in', ceiling)
  // Dues: a ceiling rate from last month on, one member's month paid in full.
  await api(request, 'POST', `/api/dues-tiers/${tiers[0].id}/rates`, { amount: ceiling, effective_from: '2024-02' })
  await api(request, 'POST', '/api/dues-payments', {
    account_id: tunai,
    member_id: members[0].id,
    occurred_on: today,
    periods: [{ dues_period: today.slice(0, 7), amount: ceiling }],
  })
  // A Talangan at the ceiling, not yet paid back.
  await api(request, 'POST', '/api/reimbursements', {
    member_id: members[1].id,
    purpose_id: main,
    amount: ceiling,
    incurred_on: today,
    note: 'Talangan besar',
  })
  // Cek kas: Tunai counted at zero and left open, a difference as wide as the
  // balance; the bank matches.
  const balances = await api<{ accounts: { id: number; name: string; balance: number }[] }>(request, 'GET', '/api/balances')
  await api(request, 'POST', '/api/reconciliations', {
    counts: balances.accounts.map((a) =>
      a.name === 'Tunai'
        ? { account_id: a.id, actual_amount: 0, resolution: 'left_open' }
        : { account_id: a.id, actual_amount: a.balance, resolution: 'matched' },
    ),
  })
}

// Runs in the page: every "Rp ..." figure in the text, measured with a Range.
// A figure is broken if it wraps onto two lines, sits outside the viewport,
// is cut off by an ancestor that clips, or sits in an ellipsised box; and an
// input is broken if its value is wider than its box.
function measureFigures() {
  const issues: string[] = []
  const viewport = document.documentElement.clientWidth
  const figure = /-?Rp\s?[\d.]+/g
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent ?? ''
    figure.lastIndex = 0
    for (let match = figure.exec(text); match; match = figure.exec(text)) {
      const range = document.createRange()
      range.setStart(node, match.index)
      range.setEnd(node, match.index + match[0].length)
      const rects = [...range.getClientRects()].filter((r) => r.width > 0)
      if (rects.length === 0) continue
      const where = JSON.stringify(match[0])
      if (new Set(rects.map((r) => Math.round(r.top / 4))).size > 1) issues.push(`wraps mid-figure: ${where}`)
      if (rects.some((r) => r.right > viewport + 0.5 || r.left < -0.5)) issues.push(`outside the viewport: ${where}`)
      for (let el = node.parentElement; el && el !== document.body; el = el.parentElement) {
        const style = getComputedStyle(el)
        if (style.overflowX !== 'visible' && rects.some((r) => r.right > el.getBoundingClientRect().right + 0.5)) {
          issues.push(`clipped by <${el.tagName.toLowerCase()}>: ${where}`)
          break
        }
        if (style.textOverflow === 'ellipsis' && el.scrollWidth > el.clientWidth) {
          issues.push(`ellipsis: ${where}`)
          break
        }
      }
    }
  }
  for (const input of document.querySelectorAll<HTMLInputElement>('input:not([type=file]):not([type=hidden])')) {
    if (input.scrollWidth > input.clientWidth + 1) issues.push(`input clips its value: ${input.value}`)
  }
  return { scroll: document.documentElement.scrollWidth, client: viewport, issues }
}

async function expectFiguresFit(page: Page, where: string) {
  const { scroll, client, issues } = await page.evaluate(measureFigures)
  expect(scroll, `${where}: scrollWidth ${scroll} > clientWidth ${client}`).toBeLessThanOrEqual(client)
  expect(issues, where).toEqual([])
}

async function openScreen(page: Page, path: string) {
  await page.goto(path)
  await expect(page.getByRole('navigation', { name: copy.shell.nav.label })).toBeVisible()
  await page.waitForLoadState('networkidle')
}

const screens = [
  '/',
  '/history/transactions',
  '/history/dues',
  '/history/reimbursements',
  '/history/reconciliations',
  '/members',
  '/dues',
  '/settings',
]

test.describe('the ceiling figure fits', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('seed ceiling-sized figures: Beranda shows them whole', async ({ page }) => {
    await logIn(page)
    await seedCeilingFigures(page.context().request)
    await openScreen(page, '/')
    await expect(page.getByRole('region', { name: copy.home.balanceHeading })).toContainText(widest)
    await expect(page.getByRole('button', { name: /^Titipan Uji Lebar/ })).toContainText(formatIDR(-ceiling))
    await expectFiguresFit(page, '/ at 390px')
  })

  test('every main screen holds the ceiling at 390px', async ({ page }) => {
    await logIn(page)
    expect(page.viewportSize()?.width).toBe(390)
    for (const path of screens) {
      await openScreen(page, path)
      await expectFiguresFit(page, `${path} at 390px`)
    }
  })

  test('every main screen holds the ceiling at 800px', async ({ page }) => {
    await logIn(page)
    await page.setViewportSize({ width: 800, height: 1000 })
    for (const path of screens) {
      await openScreen(page, path)
      await expectFiguresFit(page, `${path} at 800px`)
    }
    await page.setViewportSize({ width: 390, height: 844 })
  })

  test('an entry, a Cek kas and the envelope open at the ceiling', async ({ page }) => {
    await logIn(page)
    await openScreen(page, '/history/transactions')
    await page
      .getByRole('button', { name: /^Lihat rincian/ })
      .first()
      .click()
    await expect(page.getByRole('dialog')).toContainText(widest)
    await expectFiguresFit(page, 'entry detail at 390px')

    await openScreen(page, '/history/reconciliations')
    await page.locator('ul > li button').first().click()
    await expect(page.getByRole('dialog')).toContainText('Rp')
    await expectFiguresFit(page, 'Cek kas detail at 390px')
    await openScreen(page, '/')
    await page.getByRole('button', { name: /^Amplop Raksasa/ }).click()
    await expect(page.getByText(widest).first()).toBeVisible()
    await page.waitForLoadState('networkidle')
    await expectFiguresFit(page, 'envelope at 390px')
  })

  test('every amount input takes twelve digits and shows them whole', async ({ page }) => {
    await logIn(page)
    const forms = ['/record', '/record?type=dues', '/record?fronted=1', '/reconcile', '/settings?edit=incidental:new']
    for (const path of forms) {
      await openScreen(page, path)
      const inputs = page.locator('input[inputmode="numeric"]')
      const count = await inputs.count()
      for (let i = 0; i < count; i++) {
        await inputs.nth(i).fill('999999999999')
        // Typing: the bare digits fit; blurred: "Rp 999.999.999.999" does.
        await expectFiguresFit(page, `${path} input ${i} while typing`)
        await inputs.nth(i).blur()
        await expect(inputs.nth(i)).toHaveValue(widest)
      }
      await expectFiguresFit(page, `${path} after typing`)
    }
  })

  test('the public report holds the ceiling at 390px', async ({ page }) => {
    await logIn(page)
    const fund = await api<{ report_slug: string }>(page.context().request, 'GET', '/api/fund')
    await page.goto(`/report/${fund.report_slug}`)
    await expect(page.getByRole('region', { name: copy.home.balanceHeading })).toContainText('Rp')
    await page.waitForLoadState('networkidle')
    await expectFiguresFit(page, 'report at 390px')
  })
})
