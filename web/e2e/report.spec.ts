import { expect, test, type Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'

// M7's e2e spec (#377, ADR-015's "the public report renders"): reach the
// report from the Laporan publik card in Pengaturan, read its sections, use
// a filter, meet the 404 page, then make a new link and see the old one die.
//
// The report is server-rendered Go, so its strings are not in copy/id.ts;
// each literal below names the reportText field in internal/http/report_copy.go
// it mirrors. The fixture (cmd/uruni/seed_e2e.go) supplies what it reads: the
// opening balance (a "Saldo awal" row), two members on a tier (dues rows) and
// one open envelope, all dated the month the fixture was seeded.
//
// Serial, and the new-link test is last: it replaces the fund's slug, which
// every test before it reads. Pages under /report are public, so only the
// two Pengaturan tests log in.
test.describe('report', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'
  const seedFundName = 'Kas RT Uji Coba'
  const seedEnvelope = 'Amplop Uji Laporan'

  // reportText.NotFoundTitle / NotFoundBody
  const notFoundTitle = 'Laporan tidak ditemukan'
  const notFoundBody = 'Tautan laporan ini tidak berlaku. Minta tautan terbaru ke bendahara.'

  // The path of the report link, set by the first test and read by the rest.
  let reportPath = ''

  async function openReportCard(page: Page) {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()

    // The card is a plain <section> with an <h2>; scoped to it, since
    // Pengaturan has a dozen buttons and other sections' headings.
    const card = page.locator('section').filter({ has: page.getByRole('heading', { name: copy.settings.report.heading }) })
    await expect(card.getByRole('link', { name: copy.settings.report.open })).toBeVisible()
    return card
  }

  // The card's "Buka laporan" link, as a path. The link is absolute and opens
  // a new tab; the path is navigated in this tab so the test does not depend
  // on URUNI_BASE_URL naming the e2e server's own origin.
  async function reportPathFrom(card: Awaited<ReturnType<typeof openReportCard>>) {
    const open = card.getByRole('link', { name: copy.settings.report.open })
    await expect(open).toHaveAttribute('target', '_blank')
    const href = await open.getAttribute('href')
    expect(href).toMatch(/\/report\/[A-Za-z0-9]{22,}$/)
    return new URL(href ?? '').pathname
  }

  test('opens the report from Pengaturan and reads its sections', async ({ page }) => {
    const card = await openReportCard(page)
    reportPath = await reportPathFrom(card)

    const response = await page.goto(reportPath)
    expect(response?.status()).toBe(200)

    // The header carries the fund's name; the heading is the page's h1.
    await expect(page.locator('header').getByRole('heading', { level: 1 })).toHaveText(seedFundName)
    // The bare link opens the running month, and says so (ADR-037).
    await expect(page.locator('header p.muted')).toContainText('Bulan berjalan')

    // Transactions: the fixture's opening balance, as its "Saldo awal" row.
    // reportText.TransactionsLabel / RowOpening
    const transactions = page.locator('#transactions')
    await expect(transactions.getByRole('heading', { name: 'Transaksi' })).toBeVisible()
    const opening = transactions.locator('ul.txns li').filter({ hasText: 'Saldo awal' })
    await expect(opening).toHaveCount(1)
    await expect(opening).toContainText(formatIDR(1_000_000))

    // Dues: a member who owes this month has a row with the tier.
    // reportText.DuesLabel / DuesTier
    const dues = page.locator('#dues')
    await expect(dues.getByRole('heading', { name: 'Status iuran' })).toBeVisible()
    const member = dues.locator('ul.dues li').filter({ hasText: 'Warga Satu' })
    await expect(member).toHaveCount(1)
    await expect(member).toContainText('Golongan Reguler')

    // Envelopes: the open one, with its status badge.
    // reportText.EnvelopesLabel / EnvelopeOpen
    const envelopes = page.locator('#envelopes')
    await expect(envelopes.getByRole('heading', { name: 'Amplop' })).toBeVisible()
    const envelope = envelopes.locator('details.envelope').filter({ hasText: seedEnvelope })
    await expect(envelope).toHaveCount(1)
    await expect(envelope.locator('summary .badge')).toHaveText('Berjalan')
  })

  test('a filter is a GET query on the report URL', async ({ page }) => {
    await page.goto(reportPath)

    // Jenis (reportText.FilterDirection) submits its form on change.
    const transactions = page.locator('#transactions')
    await transactions.getByLabel('Jenis').selectOption({ value: 'in' })
    await expect(page).toHaveURL(/[?&]dir=in(&|#|$)/)

    // The page rendered from that URL keeps the choice and still shows the
    // opening balance, which is money in.
    await expect(transactions.getByLabel('Jenis')).toHaveValue('in')
    await expect(transactions.locator('ul.txns li').filter({ hasText: 'Saldo awal' })).toHaveCount(1)
  })

  test('an unknown slug is a 404 page', async ({ page }) => {
    const response = await page.goto('/report/' + 'x'.repeat(32))
    expect(response?.status()).toBe(404)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(notFoundTitle)
    await expect(page.locator('main > p')).toHaveText(notFoundBody)
  })

  test('a new link kills the old one', async ({ page }) => {
    const card = await openReportCard(page)
    expect(await reportPathFrom(card)).toBe(reportPath)

    await card.getByRole('button', { name: copy.settings.report.renew }).click()
    await expect(page).toHaveURL(/[?&]edit=report%3Anew/)
    const dialog = page.getByRole('dialog', { name: copy.settings.report.renew })
    await expect(dialog.getByText(copy.settings.report.renewConfirm)).toBeVisible()
    await dialog.getByRole('button', { name: copy.settings.report.renewConfirmAction }).click()
    await expect(dialog).not.toBeVisible()

    // The card now holds a different link.
    await expect(async () => {
      expect(await reportPathFrom(card)).not.toBe(reportPath)
    }).toPass()
    const newPath = await reportPathFrom(card)

    const old = await page.goto(reportPath)
    expect(old?.status()).toBe(404)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(notFoundTitle)

    const fresh = await page.goto(newPath)
    expect(fresh?.status()).toBe(200)
    await expect(page.locator('header').getByRole('heading', { level: 1 })).toHaveText(seedFundName)
  })
})
