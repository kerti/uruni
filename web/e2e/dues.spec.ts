import { copy } from '../src/copy/id'
import { expect, test, logIn } from './fixtures'

// M6.12: the dues status roster (PRD section 7.3). The seeded fixture
// (cmd/uruni/seed_e2e.go) creates two members - "Warga Satu" and "Warga
// Dua" - on one dues tier whose rate runs from 2024-01, and they join in
// the same month, so every month since is outstanding for both. That is
// enough to prove the period view loads and renders a real row without
// this spec deriving anything itself - the four-status rendering, the
// tier-less-member exclusion and the "belum bayar" filter are already
// covered per-case by the vitest suite (Status.test.tsx), which can stub
// every status directly.
//
// M6.13's payment spec below posts real rows, and the reversal spec after it
// undoes one, so the file is serial: each test reads what the one before
// changed. The instance is this file's own (reset in beforeAll).
test.describe('dues status', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('loads the period view and shows the seeded roster', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.dues }).click()
    // The tab is the payment history; the matrix is its own screen, one
    // row away (#228).
    await expect(page.getByRole('heading', { name: copy.history.dues.heading })).toBeVisible()
    await page.getByRole('button', { name: copy.dues.entryLink }).click()
    await expect(page.getByLabel(copy.dues.periodLabel)).toBeVisible()

    // Both seeded members owe this period's rate and neither has ever paid.
    await expect(page.getByText('Warga Satu')).toBeVisible()
    await expect(page.getByText('Warga Dua')).toBeVisible()
    await expect(page.getByText(copy.dues.statuses.unpaid).first()).toBeVisible()

    await page.getByRole('button', { name: copy.dues.back }).click()
    await expect(page.getByRole('heading', { name: copy.history.dues.heading })).toBeVisible()
  })

  // M6.13: one member paying two months in the same sitting. The seeded
  // members joined 2024-01 on a rate effective from the same month and have
  // never paid, so the payment form is guaranteed to offer at least two
  // outstanding periods, oldest first - which two they are depends on
  // today's date, so this spec ticks the first two checkboxes rather than
  // naming months.
  test('records a multi-period dues payment', { tag: '@smoke' }, async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.dues }).click()
    await page.getByRole('button', { name: copy.dues.entryLink }).click()
    await page.getByRole('button', { name: copy.dues.recordLink }).click()
    await expect(page.getByRole('heading', { name: copy.dues.payment.heading })).toBeVisible()

    // The themed Select (M6.15) is a button plus a portalled listbox, not a
    // native <select>, so picking is open-then-click.
    await page.getByRole('combobox', { name: copy.dues.payment.memberLabel }).click()
    // Scoped to the open listbox - Radix's hidden native select carries the
    // same option text.
    await page.getByRole('listbox').getByRole('option', { name: 'Warga Satu' }).click()

    const periods = page.getByRole('checkbox')
    await expect(periods.first()).toBeVisible()
    // Captured before checking, for the label assertion below (#257): the
    // oldest outstanding period is never a fixed month (see the file
    // header), so the label text this spec expects on home has to be read
    // off the same form it just paid, not hardcoded.
    const firstPeriodText = (await periods.nth(0).evaluate((el) => el.closest('label')?.textContent ?? '')).trim()
    await periods.nth(0).check()
    await periods.nth(1).check()

    // Each amount arrives pre-filled with the period's own rate and is
    // edited down here: paying part of a month is a real path (M6.13), and
    // it is what leaves the reversal spec below a partial status to read.
    // Derived from the copy itself (ADR-014), never retyped: the label is
    // "<prefix><month>", so the prefix with an empty month is what every
    // period's amount field starts with.
    const amountPrefix = copy.dues.payment.amountLabel('')
    const amounts = page.getByLabel(new RegExp(`^${amountPrefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`))

    // Emptied before it is filled, never filled straight over the pre-filled
    // rate: this field renders "Rp 50.000" when blurred and the bare digits
    // when focused (AmountInput), and a single fill() over that lands as the
    // two runs of digits concatenated - 50000 then 12345 posted as
    // Rp 5.000.012.345. Clearing first puts the field in the same empty
    // state golden-path.spec.ts fills, which is deterministic.
    for (const [index, value] of [
      [0, '20000'],
      [1, '30000'],
    ] as const) {
      await amounts.nth(index).fill('')
      await amounts.nth(index).fill(value)
      // Still focused, so the field shows the bare digits it holds - proof
      // the value is what this spec asked for and not a concatenation.
      await expect(amounts.nth(index)).toHaveValue(value)
    }

    await page.getByRole('button', { name: copy.dues.payment.submit }).click()

    // Back on the roster, refreshed, with the confirmation for this one
    // navigation.
    await expect(page.getByLabel(copy.dues.periodLabel)).toBeVisible()
    await expect(page.getByText(copy.dues.payment.success)).toBeVisible()

    // Back returns to Riwayat's Iuran tab (#228), not home.
    await page.getByRole('button', { name: copy.dues.back }).click()
    await expect(page.getByRole('heading', { name: copy.history.dues.heading })).toBeVisible()

    // Every posted row says whose dues it was: home's recent activity shows
    // the label, so a dues payment never reads there as a bare amount
    // (#257 - built at display time, never stored text).
    await page.getByRole('link', { name: copy.shell.nav.home }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.rowLabels.dues.text(firstPeriodText, 'Warga Satu')).first()).toBeVisible()
  })

  // M6.14: undoing one of the payments the spec above just posted. Serial
  // mode (top of file) is what guarantees there is one to undo. The oldest
  // outstanding period is 2024-01 for both seeded members - they join that
  // month on a rate effective from it - so that is the period the payment
  // spec's first checkbox pays, and the one this spec reverses.
  test('reverses a dues payment', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.dues }).click()
    await page.getByRole('button', { name: copy.dues.entryLink }).click()
    // The period is a MonthField (#197): open it, step back to 2024, tap
    // January - the same taps she makes.
    await page.getByLabel(copy.dues.periodLabel).click()
    const picker = page.getByRole('dialog')
    while ((await picker.getByText(/^\d{4}$/).textContent()) !== '2024') {
      await picker.getByRole('button', { name: copy.dateField.previousYear }).click()
    }
    await picker.getByRole('button', { name: 'Januari 2024' }).click()

    // Warga Satu paid part of this month, so the roster shows the partial
    // status and the history behind it holds the payment to reverse.
    const card = page.locator('li').filter({ hasText: 'Warga Satu' })
    await expect(card.getByText(copy.dues.statuses.partial)).toBeVisible()
    await card.getByRole('button', { name: copy.dues.history.title }).click()

    await card.getByRole('button', { name: copy.dues.history.reverse }).first().click()
    await card.getByRole('button', { name: copy.dues.history.confirm }).click()

    // The reversal is its own row - the payment is still listed, now marked
    // as undone, and never offers the action a second time. The roster
    // above refreshes itself: with nothing paid, the month reads unpaid.
    await expect(card.getByText(copy.dues.history.reversedBadge)).toBeVisible()
    await expect(card.getByRole('button', { name: copy.dues.history.reverse })).toBeHidden()
    await expect(card.getByText(copy.dues.statuses.unpaid)).toBeVisible()
  })
})
