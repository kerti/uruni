import type { APIRequestContext, Locator, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatPeriod } from '../src/lib/dates'
import { formatIDR } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'
import { chooseOption, popperContent } from './popper'
import { cardButton } from './card'

// ADR-040: a tier is stated "from a month", and a rate is corrected with its
// span named. One member is promoted from Reguler (Rp50.000) to Senior
// (Rp80.000) two months back; the month before keeps the old tier and amount
// on Iuran and on the public report, and then a Senior rate is corrected and
// the confirmation names the months it reprices.

const reguler = 50_000
const senior = 80_000
const seniorCorrected = 85_000
const seniorNow = 90_000

// The month the app reads everything in: Asia/Jakarta (internal/tz).
function jakartaMonth(offset = 0): string {
  const [y, m] = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Jakarta' }).format(new Date()).split('-').map(Number)
  const d = new Date(Date.UTC(y, m - 1 + offset, 1))
  return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`
}

async function api<T>(request: APIRequestContext, method: 'GET' | 'POST', path: string, data?: unknown): Promise<T> {
  const response = method === 'GET' ? await request.get(path) : await request.post(path, { data })
  expect(response.ok(), `${method} ${path} answered ${response.status()}: ${await response.text()}`).toBe(true)
  return (await response.json()) as T
}

// Opens a month grid by the label of its field, steps to the period's year and
// taps the month. Scoped to the popover, which opens over any dialog.
async function pickMonth(page: Page, field: Locator, period: string) {
  await field.click()
  const grid = popperContent(page)
  const target = Number(period.slice(0, 4))
  for (let guard = 0; guard < 40; guard++) {
    const shown = Number(await grid.getByText(/^\d{4}$/).textContent())
    if (shown === target) break
    await grid.getByRole('button', { name: shown > target ? copy.dateField.previousYear : copy.dateField.nextYear }).click()
  }
  await grid.getByRole('button', { name: formatPeriod(period) }).click()
}

function statusCard(page: Page, name: string): Locator {
  return page
    .getByRole('main')
    .getByRole('listitem')
    .filter({ has: page.getByText(name, { exact: true }) })
}

async function openStatus(page: Page) {
  await page.getByRole('link', { name: copy.shell.nav.history }).click()
  await page.getByRole('link', { name: copy.history.tabs.dues }).click()
  await page.getByRole('button', { name: copy.dues.entryLink }).click()
  await expect(page.getByLabel(copy.dues.periodLabel)).toBeVisible()
}

test.describe('a tier change and a rate correction', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const member = 'Warga Naik'
  const promoted = jakartaMonth(-2)
  const before = jakartaMonth(-3)
  let seniorId = 0

  test('seed: a member on Reguler for months, and a Senior tier with two rates', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const body = await api<{ id: number; name: string }[] | { dues_tiers: { id: number; name: string }[] }>(
      request,
      'GET',
      '/api/dues-tiers',
    )
    const tiers = Array.isArray(body) ? body : body.dues_tiers
    const regulerId = tiers.find((t) => t.name === 'Reguler')?.id
    const tier = await api<{ id: number }>(request, 'POST', '/api/dues-tiers', { name: 'Senior' })
    seniorId = tier.id
    await api(request, 'POST', `/api/dues-tiers/${seniorId}/rates`, { amount: senior, effective_from: '2024-01' })
    await api(request, 'POST', `/api/dues-tiers/${seniorId}/rates`, { amount: seniorNow, effective_from: jakartaMonth() })
    await api(request, 'POST', '/api/members', { name: member, tier_id: regulerId, joined_on: `${jakartaMonth(-5)}-01` })

    // The report offers the months from the first transaction on and reads any
    // other ?month= as the current one (ADR-035). The fixture's first is today's
    // opening, so one entry dated in the month before the promotion puts that
    // month on the report.
    type Named = { id: number; name: string }
    const listed = async (path: string, key: string) => {
      const b = await api<Named[] | Record<string, Named[]>>(request, 'GET', path)
      return Array.isArray(b) ? b : b[key]
    }
    const tunai = (await listed('/api/accounts', 'accounts')).find((a) => a.name === 'Tunai')?.id
    const kasUtama = (await listed('/api/purposes', 'purposes')).find((p) => p.name === 'Kas Utama')?.id
    await api(request, 'POST', '/api/transactions', {
      account_id: tunai,
      purpose_id: kasUtama,
      direction: 'in',
      amount: 10_000,
      occurred_on: `${before}-01`,
      note: 'Sisa kas lama',
    })
  })

  test('promote the member from a past month on the Anggota edit form', async ({ page }) => {
    await logIn(page)
    await page.goto('/members')
    const text = copy.members.roster
    await cardButton(page, text.editVerb, member).click()
    const dialog = page.getByRole('dialog', { name: text.editTitle })

    await expect(dialog.getByLabel(text.tierFromLabel)).toHaveCount(0)
    await chooseOption(page, dialog.getByRole('combobox', { name: text.tierLabel }), 'Senior')
    await expect(dialog.getByLabel(text.tierFromLabel)).toContainText(formatPeriod(jakartaMonth()))

    await pickMonth(page, dialog.getByLabel(text.tierFromLabel), promoted)
    await dialog.getByRole('button', { name: text.save }).click()
    await expect(dialog).toBeHidden()
    await expect(cardButton(page, text.editVerb, member)).toContainText('Senior')
  })

  test('Iuran: the earlier month keeps the old amount, the promoted month has the new one', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openStatus(page)
    const period = page.getByLabel(copy.dues.periodLabel)

    await pickMonth(page, period, before)
    await expect(statusCard(page, member)).toContainText(`${copy.dues.owedLabel}: ${formatIDR(reguler)}`)

    await pickMonth(page, period, promoted)
    await expect(statusCard(page, member)).toContainText(`${copy.dues.owedLabel}: ${formatIDR(senior)}`)
  })

  test('the public report shows the tier of the month it is showing', async ({ page }) => {
    await logIn(page)
    const fund = await api<{ report_slug: string }>(page.context().request, 'GET', '/api/fund')
    const row = (p: Page) => p.locator('#dues ul.dues li').filter({ hasText: member })

    // reportText.DuesTier is "Kategori".
    await page.goto(`/report/${fund.report_slug}?month=${before}`)
    await expect(row(page)).toContainText('Kategori Reguler')
    await page.goto(`/report/${fund.report_slug}?month=${promoted}`)
    await expect(row(page)).toContainText('Kategori Senior')
  })

  test('correcting a rate names the months it reprices, then reprices them', async ({ page }) => {
    await logIn(page)
    await page.goto(`/dues-tiers?tier=${seniorId}`)
    const text = copy.settings.tiers
    const rows = page.getByRole('main').getByRole('listitem')
    const first = rows.filter({ hasText: text.effectiveFrom(formatPeriod('2024-01')) })
    await first.getByRole('button', { name: text.editRate }).click()

    const editor = rows.filter({ has: page.locator('[id^="rate-amount-"]') })
    const amount = editor.getByLabel(text.rateAmountLabel)
    await amount.fill('')
    await amount.fill(String(seniorCorrected))
    await editor.getByRole('button', { name: text.saveRate }).click()

    // From the corrected month to the month before the next rate: the second
    // rate starts this month, so the span ends last month.
    const span = text.rateSpanRange(formatPeriod('2024-01'), formatPeriod(jakartaMonth(-1)))
    await expect(editor).toContainText(text.rateCorrectConfirm(formatIDR(senior), formatIDR(seniorCorrected), span))
    await editor.getByRole('button', { name: text.rateCorrectConfirmAction }).click()
    await expect(first).toContainText(formatIDR(seniorCorrected))

    await page.goto('/')
    await openStatus(page)
    await pickMonth(page, page.getByLabel(copy.dues.periodLabel), promoted)
    await expect(statusCard(page, 'Warga Naik')).toContainText(`${copy.dues.owedLabel}: ${formatIDR(seniorCorrected)}`)
  })
})
