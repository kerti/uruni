import { copy } from '../src/copy/id'
import { expect, test, logIn } from './fixtures'
import { expectAccessible } from './a11y'

// Waiving a member's dues (ADR-050): the group lets a month go. The seeded
// fixture (cmd/uruni/seed_e2e.go) has "Warga Satu" and "Warga Dua" owing every
// month since 2024-01, so this month is owed by both. One serial story on its
// own instance: waive Warga Satu's month from Status iuran, read it on the
// public report (Diputihkan, never the note), then take the waiver back.
const text = copy.dues.waiver
const note = 'Rumah kebakaran, keputusan rapat'

test.describe('dues waiver', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('Putihkan waives this month from Status iuran', { tag: '@smoke' }, async ({ page }) => {
    await logIn(page)
    await page.goto('/dues')
    const card = page.locator('main li').filter({ hasText: 'Warga Satu' })
    await card.getByRole('button', { name: text.actionAria('Warga Satu') }).click()

    const dialog = page.getByRole('dialog', { name: text.heading })
    await expect(dialog.getByText('Warga Satu')).toBeVisible()
    // The card's month comes ticked; the arrears wait for a tap.
    await expect(dialog.getByRole('checkbox', { checked: true })).toHaveCount(1)
    await dialog.getByLabel(text.noteLabel).fill(note)
    await expectAccessible(page)
    await dialog.getByRole('button', { name: text.submit }).click()

    await expect(dialog).toBeHidden()
    await expect(page.getByText(text.success)).toBeVisible()
    await expect(card.getByText(copy.dues.statuses.waived)).toBeVisible()
    await expect(card.getByText(text.noteLine(note))).toBeVisible()
    await expect(card.getByRole('button', { name: copy.dues.recordForMemberAria('Warga Satu') })).toHaveCount(0)
  })

  test('the public report reads Diputihkan and never the note', async ({ page }) => {
    await logIn(page)
    const fund = (await (await page.request.get('/api/fund')).json()) as { report_url: string }
    await page.goto(new URL(fund.report_url, 'http://x').pathname)

    const row = page.locator('#dues ul.dues li').filter({ hasText: 'Warga Satu' })
    await expect(row).toContainText(copy.dues.statuses.waived)
    await expect(page.locator('body')).not.toContainText(note)
  })

  test('Batalkan pemutihan confirms, then the month is owed again', async ({ page }) => {
    await logIn(page)
    await page.goto('/dues')
    const card = page.locator('main li').filter({ hasText: 'Warga Satu' })
    await card.getByRole('button', { name: text.undoAria('Warga Satu') }).click()

    const dialog = page.getByRole('dialog', { name: text.undoHeading })
    await expect(dialog.getByText(note)).toBeVisible()
    await dialog.getByRole('button', { name: text.undoConfirm }).click()

    await expect(dialog).toBeHidden()
    await expect(page.getByText(text.undoSuccess)).toBeVisible()
    await expect(card.getByText(copy.dues.statuses.unpaid)).toBeVisible()
    await expect(card.getByRole('button', { name: copy.dues.recordForMemberAria('Warga Satu') })).toBeVisible()
  })
})
