import { copy } from '../src/copy/id'
import { expect, test, logIn } from './fixtures'
import { expectAccessible } from './a11y'

// The twelve-month view of the dues status screen (ADR-044, #619). The seeded
// fixture (cmd/uruni/seed_e2e.go) has two members, "Warga Satu" and "Warga Dua",
// on a tier whose rate runs from 2024-01 and who joined that month, so both owe
// every one of the last twelve months and have paid none: the grid is twelve
// columns of Belum bayar for two rows, whatever today's date is. The four cell
// shapes and every derivation rule are covered below the browser (Go for the
// ledger call, Vitest for the drawing); this spec is the journey - the switch,
// the URL, the legend, and that a cell is not a control.
//
// Serial: the second test reloads the view the first one opened. The instance is
// this file's own (reset in beforeAll).
test.describe('dues status, twelve months', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('switches to the grid, reads twelve months for each member, and has no controls in it', async ({ page }) => {
    await logIn(page)
    await page.goto('/dues')
    // The one-month list is the default view, with its own controls.
    await expect(page.getByLabel(copy.dues.periodLabel)).toBeVisible()

    await page.getByRole('button', { name: copy.dues.grid.viewTwelveMonths }).click()
    await expect(page).toHaveURL(/[?&]view=grid\b/)
    await expect(page.getByRole('heading', { name: copy.dues.grid.heading })).toBeVisible()
    // The month field and the "belum bayar" toggle belong to the list.
    await expect(page.getByLabel(copy.dues.periodLabel)).toBeHidden()
    await expect(page.getByLabel(copy.dues.unpaidFilterLabel)).toBeHidden()

    const table = page.getByRole('table')
    await expect(table).toBeVisible()
    // Twelve month heads (the corner cell is not a column head).
    await expect(table.getByRole('columnheader')).toHaveCount(12)

    // Rows in the API's order; each row carries twelve labelled cells, none paid.
    await expect(table.getByRole('rowheader')).toHaveText(['Warga Satu', 'Warga Dua'])
    for (const name of ['Warga Satu', 'Warga Dua']) {
      const row = table.getByRole('row').filter({ has: page.getByRole('rowheader', { name }) })
      await expect(row.getByText(new RegExp(`: ${copy.dues.statuses.unpaid}$`))).toHaveCount(12)
    }

    // Legend under the grid, in the four words.
    for (const word of [copy.dues.statuses.paid, copy.dues.statuses.partial, copy.dues.statuses.unpaid, copy.dues.grid.none]) {
      await expect(page.getByRole('listitem').filter({ hasText: word })).toBeVisible()
    }

    // Cells are not controls; acting stays on the list and Catat pembayaran.
    await expect(table.getByRole('button')).toHaveCount(0)
    await expect(table.getByRole('link')).toHaveCount(0)
    await expect(page.getByRole('button', { name: copy.dues.recordLink })).toBeVisible()

    await expectAccessible(page)
  })

  test('keeps the view across a reload and goes back to the list', async ({ page }) => {
    await logIn(page)
    await page.goto('/dues?view=grid')
    await expect(page.getByRole('heading', { name: copy.dues.grid.heading })).toBeVisible()

    await page.reload()
    await expect(page.getByRole('heading', { name: copy.dues.grid.heading })).toBeVisible()

    await page.getByRole('button', { name: copy.dues.grid.viewOneMonth }).click()
    await expect(page).not.toHaveURL(/view=grid/)
    await expect(page.getByLabel(copy.dues.periodLabel)).toBeVisible()
    await expect(page.getByText('Warga Satu')).toBeVisible()
  })
})
