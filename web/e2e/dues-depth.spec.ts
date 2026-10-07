import type { APIRequestContext, Locator, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatPeriod } from '../src/lib/dates'
import { formatIDR } from '../src/lib/money'
import { expect, logIn, test } from './fixtures'

// M8.8 (#439): dues past the one multi-month payment dues.spec.ts walks.
// Paying a month ahead, paying part of a month, the belum-bayar filter, a
// backdated join date that carries arrears onto the roster, and golongan and
// tarif in Pengaturan.
//
// Three members join this month on the fixture's Reguler tier (Rp 50.000),
// added over the API so each owes exactly the current month and nothing
// before it: what this file shows about them is then down to what it does.
// Serial, one story on the seeded fixture.

const rate = 50_000
const part = 20_000

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

async function openStatus(page: Page) {
  await page.getByRole('link', { name: copy.shell.nav.history }).click()
  await page.getByRole('link', { name: copy.history.tabs.dues }).click()
  await page.getByRole('button', { name: copy.dues.entryLink }).click()
  await expect(page.getByLabel(copy.dues.periodLabel)).toBeVisible()
}

// One member's card on the Iuran status view.
function statusCard(page: Page, name: string): Locator {
  return page
    .getByRole('main')
    .getByRole('listitem')
    .filter({ has: page.getByText(name, { exact: true }) })
}

// From the status view: the payment form for one member, with the given
// periods ticked at the given amounts.
async function pay(page: Page, member: string, periods: { period: string; amount: number }[], monthsAhead = 0) {
  await page.getByRole('button', { name: copy.dues.recordLink }).click()
  await expect(page.getByRole('heading', { name: copy.dues.payment.heading })).toBeVisible()
  await page.getByRole('combobox', { name: copy.dues.payment.memberLabel }).click()
  await page.getByRole('listbox').getByRole('option', { name: member }).click()
  for (let i = 0; i < monthsAhead; i++) {
    await page.getByRole('button', { name: copy.dues.payment.addMonth(formatPeriod(jakartaMonth(i + 1))) }).click()
  }
  for (const { period, amount } of periods) {
    await page.getByRole('checkbox', { name: formatPeriod(period) }).check()
    // Emptied first: the field shows the formatted rate when blurred, and a
    // fill straight over it concatenates (see dues.spec.ts).
    const field = page.getByLabel(copy.dues.payment.amountLabel(formatPeriod(period)))
    await field.fill('')
    await field.fill(String(amount))
  }
  await page.getByRole('button', { name: copy.dues.payment.submit }).click()
  await expect(page.getByText(copy.dues.payment.success)).toBeVisible()
}

test.describe('dues in depth', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('pay this month and next, and the card reads paid through next month', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const tiers = await api<{ id: number; name: string }[] | { dues_tiers: { id: number; name: string }[] }>(
      request,
      'GET',
      '/api/dues-tiers',
    )
    const tierId = (Array.isArray(tiers) ? tiers : tiers.dues_tiers).find((t) => t.name === 'Reguler')?.id
    const joinedOn = `${jakartaMonth()}-01`
    for (const name of ['Warga Muka', 'Warga Sebagian', 'Warga Baru']) {
      await api(request, 'POST', '/api/members', { name, tier_id: tierId, joined_on: joinedOn })
    }

    await page.goto('/')
    await openStatus(page)
    await expect(statusCard(page, 'Warga Muka')).toContainText(copy.dues.statuses.unpaid)

    await pay(
      page,
      'Warga Muka',
      [
        { period: jakartaMonth(), amount: rate },
        { period: jakartaMonth(1), amount: rate },
      ],
      1,
    )

    // Paid ahead with a known end (#357): the badge is a plain "Lunas" and
    // the month it is paid through has a line of its own.
    const card = statusCard(page, 'Warga Muka')
    await expect(card).toContainText(copy.dues.statuses.paid)
    await expect(card).toContainText(copy.dues.paidThrough(formatPeriod(jakartaMonth(1))))
  })

  test('pay part of a month, and the card reads "Bayar sebagian"', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openStatus(page)

    await pay(page, 'Warga Sebagian', [{ period: jakartaMonth(), amount: part }])

    const card = statusCard(page, 'Warga Sebagian')
    await expect(card).toContainText(copy.dues.statuses.partial)
    await expect(card).toContainText(`${copy.dues.paidLabel}: ${formatIDR(part)}`)
  })

  test('the belum-bayar filter keeps only who still owes this month', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openStatus(page)

    await page.getByRole('checkbox', { name: copy.dues.unpaidFilterLabel }).check()
    await expect(statusCard(page, 'Warga Muka')).toHaveCount(0)
    // Part-paid still owes, so it stays.
    await expect(statusCard(page, 'Warga Sebagian')).toBeVisible()
    await expect(statusCard(page, 'Warga Baru')).toBeVisible()

    await page.getByRole('checkbox', { name: copy.dues.unpaidFilterLabel }).uncheck()
    await expect(statusCard(page, 'Warga Muka')).toBeVisible()
  })

  test('backdating a join date carries arrears onto the roster', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await page.getByRole('link', { name: copy.shell.nav.members }).click()
    const text = copy.members.roster
    const card = page.getByRole('button', { name: text.editAria('Warga Baru') })

    // Joined this month: nothing before it is owed, so no badge.
    await expect(card).toBeVisible()
    await expect(card).not.toContainText(/Tunggakan/)

    // Mulai ikut moves back three months, through the calendar's own month
    // and year dropdowns, to the first of that month.
    const joined = jakartaMonth(-3)
    const [year, month] = joined.split('-').map(Number)
    await card.click()
    const dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByLabel(text.joinedOnLabel).click()
    const calendar = page.locator('[data-radix-popper-content-wrapper]').last()
    const monthName = new Intl.DateTimeFormat('id-ID', { month: 'long', timeZone: 'UTC' }).format(new Date(Date.UTC(year, month - 1, 1)))
    await calendar.locator('select').nth(1).selectOption(String(year))
    await calendar.locator('select').nth(0).selectOption({ label: monthName })
    await calendar.getByRole('gridcell').getByRole('button').filter({ hasText: /^1$/ }).first().click()
    await dialog.getByRole('button', { name: text.save }).click()
    await expect(dialog).toBeHidden()

    // The badge counts the months before this one (ADR-032): three.
    await expect(card).toContainText(text.arrearsBadge(3))
  })

  test('golongan and tarif: add a tier, add a rate, correct it, delete the rate and the tier', async ({ page }) => {
    await logIn(page)
    await page.goto('/settings')
    const text = copy.settings.tiers

    await page.getByRole('button', { name: text.add }).click()
    const addDialog = page.getByRole('dialog', { name: text.add })
    await addDialog.getByLabel(text.nameLabel).fill('Madya')
    await addDialog.getByRole('button', { name: text.add }).click()
    await expect(addDialog).toBeHidden()

    // A new tier has no rate yet; its own screen adds one from a month (the
    // form's default, this month).
    const tierCard = page.getByRole('button', { name: text.cardAria('Madya') })
    await expect(tierCard).toContainText(text.noRates)
    await tierCard.click()
    const addRate = page.getByRole('form', { name: text.addRate })
    await addRate.getByLabel(text.rateAmountLabel).fill('75000')
    await addRate.getByRole('button', { name: text.addRate }).click()
    const rateRow = page
      .getByRole('main')
      .getByRole('listitem')
      .filter({ hasText: text.effectiveFrom(formatPeriod(jakartaMonth())) })
    await expect(rateRow).toContainText(formatIDR(75_000))

    // Perbaiki nominal: the amount changes, the month never does. While it
    // is open the row is an editor without its "Mulai" line, so the editor
    // is found by its own field.
    const editor = page
      .getByRole('main')
      .getByRole('listitem')
      .filter({ has: page.locator('[id^="rate-amount-"]') })
    await rateRow.getByRole('button', { name: text.editRate }).click()
    const amount = editor.getByLabel(text.rateAmountLabel)
    await amount.fill('')
    await amount.fill('80000')
    await editor.getByRole('button', { name: text.saveRate }).click()
    await expect(rateRow).toContainText(formatIDR(80_000))
    await expect(rateRow).toContainText(text.effectiveFrom(formatPeriod(jakartaMonth())))

    // Hapus tarif, from the same editor.
    await rateRow.getByRole('button', { name: text.editRate }).click()
    await editor.getByRole('button', { name: text.deleteRate }).click()
    await expect(page.getByText(text.noRates)).toBeVisible()

    // Hapus golongan: no member is on it, so it goes.
    await page.getByRole('button', { name: text.delete }).click()
    await page.getByRole('button', { name: text.deleteConfirmAction }).click()
    await expect(page.getByRole('button', { name: text.cardAria('Madya') })).toHaveCount(0)
  })
})
