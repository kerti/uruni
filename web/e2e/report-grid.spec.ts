import type { Page } from '@playwright/test'

import { expect, test, logIn } from './fixtures'
import { expectAccessible } from './a11y'

// The twelve-month dues grid on the public report (ADR-044, #619). The seeded
// fixture (cmd/uruni/seed_e2e.go) has two members, "Warga Satu" and "Warga Dua",
// on a tier whose rate runs from 2024-01 and who joined that month, so both owe
// every one of the last twelve months and have paid none: twelve columns of
// Belum bayar for two rows, whatever today's date is. The cell shapes, the
// two-month floor and the PDF page are covered in Go (report_grid_test.go,
// report_pdf_test.go); this spec is the journey a neighbour takes - the grid
// opens the Iuran section, no filter changes it, and a past month says what day
// its status is read on.
//
// The report is server-rendered Go, so its strings are not in copy/id.ts; each
// literal below names the Text field in internal/report/report_copy.go it
// mirrors. Pages under /report are public; logging in only reads the slug.
test.describe('report, twelve months of dues', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  // reportText.GridHeading / DuesUnpaid / GridNone
  const gridHeading = 'Status iuran 12 bulan'
  const unpaid = 'Belum bayar'
  const none = 'Tidak ada iuran'
  // By name, as every member list reads (ADR-044), so Dua before Satu.
  const members = ['Warga Dua', 'Warga Satu']

  let reportPath = ''

  async function reportPathFor(page: Page) {
    await logIn(page)
    const response = await page.request.get('/api/fund')
    expect(response.ok()).toBe(true)
    const fund = (await response.json()) as { report_slug: string }
    return `/report/${fund.report_slug}`
  }

  // The fund's previous month, read in Asia/Jakarta: the seeded fund is WIB.
  function previousMonth(): string {
    const [year, month] = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Jakarta', year: 'numeric', month: '2-digit' })
      .format(new Date())
      .split('-')
      .map(Number)
    const d = new Date(Date.UTC(year, month - 2, 1))
    return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`
  }

  const duesSection = (page: Page) => page.locator('#dues')
  const grid = (page: Page) => duesSection(page).locator('.grid')

  async function expectWholeGrid(page: Page) {
    const table = grid(page).getByRole('table')
    await expect(table.getByRole('columnheader')).toHaveCount(12)
    await expect(table.getByRole('rowheader')).toHaveText(members)
    for (const name of members) {
      const row = table.getByRole('row').filter({ has: page.getByRole('rowheader', { name }) })
      await expect(row.getByText(new RegExp(`: ${unpaid}$`))).toHaveCount(12)
    }
  }

  test('opens the Iuran section with twelve months for every member', async ({ page }) => {
    reportPath = await reportPathFor(page)
    await page.goto(reportPath)

    await expect(grid(page).getByRole('heading', { name: gridHeading })).toBeVisible()
    await expectWholeGrid(page)

    // Above the status filter and the month's table, which stay as they were.
    const order = await duesSection(page).evaluate((section) => {
      const g = section.querySelector('.grid')
      const f = section.querySelector('form.filters')
      return g && f ? g.compareDocumentPosition(f) & Node.DOCUMENT_POSITION_FOLLOWING : 0
    })
    expect(order).toBeTruthy()

    // The legend's four words; status only - no figure anywhere in the grid.
    for (const word of ['Lunas', 'Bayar sebagian', unpaid, none]) {
      await expect(grid(page).locator('ul.legend li').filter({ hasText: word })).toBeVisible()
    }
    await expect(grid(page)).not.toContainText('Rp')

    // The running month carries no as-of caption: its status is today's anyway.
    // reportText.GridAsOf
    await expect(grid(page).getByText(/^Status per /)).toHaveCount(0)

    await expectAccessible(page)
  })

  test('no filter changes the grid', async ({ page }) => {
    // dues=paid empties the month's table here (nobody has paid), and the grid
    // still shows every member: no filter selects members by their grid.
    await page.goto(`${reportPath}?dues=paid`)
    await expectWholeGrid(page)

    await page.goto(`${reportPath}?dir=in`)
    await expectWholeGrid(page)
  })

  test('a past month ends the grid there and says what day its status is read on', async ({ page }) => {
    // The report offers months from the first transaction on, and the fixture's
    // only row is this month's opening balance; one row dated last month makes
    // last month selectable.
    const month = previousMonth()
    await logIn(page)
    const request = page.context().request
    const id = async (path: string, key: string, name: string) => {
      const response = await request.get(path)
      expect(response.ok()).toBe(true)
      const body = (await response.json()) as Record<string, { id: number; name: string }[]> | { id: number; name: string }[]
      const rows = Array.isArray(body) ? body : body[key]
      const row = rows.find((r) => r.name === name)
      if (!row) throw new Error(`${name} not found in GET ${path}`)
      return row.id
    }
    const posted = await request.post('/api/transactions', {
      data: {
        account_id: await id('/api/accounts', 'accounts', 'Tunai'),
        purpose_id: await id('/api/purposes', 'purposes', 'Kas Utama'),
        direction: 'in',
        amount: 10_000,
        occurred_on: `${month}-01`,
        note: 'Bulan lalu',
      },
    })
    expect(posted.ok(), await posted.text()).toBe(true)

    await page.goto(`${reportPath}?month=${month}`)
    await expect(page.locator('header p.muted')).not.toContainText('Bulan berjalan')

    await expectWholeGrid(page)
    // The last column is the selected month, never the running one.
    const heads = grid(page).getByRole('columnheader')
    const [year, mm] = month.split('-').map(Number)
    const lastFull = new Intl.DateTimeFormat('id-ID', { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(
      new Date(Date.UTC(year, mm - 1, 1)),
    )
    await expect(heads.last()).toContainText(lastFull)
    await expect(grid(page).getByText(/^Status per \d{1,2} \S+ \d{4}$/)).toBeVisible()
  })
})
