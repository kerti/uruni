import { expect, test } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.16 + M6.17: the roster and the dues tiers, on their own screen.
//
// One path in a real browser - home → Anggota → add a member → see it in
// the list - and everything else left to the vitest suites, which can stub a
// 409, a retired row and the partial-update semantics without touching the
// shared database.
//
// Like settings.spec.ts, this file cleans up after itself. A member left
// behind would change what dues.spec.ts's roster reads, and the suite runs
// against one seeded instance (playwright.config.ts, workers: 1).
test.describe('members', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'

  // Not "Warga Satu"/"Warga Dua" - those are the fixture's own members, and
  // dues.spec.ts asserts on them by name.
  const memberName = 'Warga Uji Anggota'

  test('adds a member from the roster screen, then removes them again', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.members }).click()
    // exact: true - Playwright matches an accessible name by substring, and
    // "Anggota" is inside the "Daftar anggota" section heading below it.
    await expect(page.getByRole('heading', { name: copy.members.heading, exact: true })).toBeVisible()

    // The fixture's own roster is already listed.
    await expect(page.getByText('Warga Satu')).toBeVisible()

    const addForm = page.getByRole('form', { name: copy.members.roster.add })
    await addForm.getByLabel(copy.members.roster.nameLabel).fill(memberName)
    await addForm.getByRole('button', { name: copy.members.roster.add }).click()

    const row = page.getByRole('listitem').filter({ hasText: memberName })
    await expect(row).toBeVisible()

    // Never referenced by a transaction, so the delete is allowed - a member
    // with history answers 409 and the screen says "nonaktifkan, bukan
    // hapus" instead, which is covered in vitest.
    await row.getByRole('button', { name: copy.members.roster.delete }).click()
    await expect(page.getByText(memberName)).toHaveCount(0)
  })

  // The header's fund name is the app's second way home (M6.16). Proven
  // here rather than only in jsdom because it is a real navigation.
  test('the fund name in the header navigates home', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.members }).click()
    // exact: true - Playwright matches an accessible name by substring, and
    // "Anggota" is inside the "Daftar anggota" section heading below it.
    await expect(page.getByRole('heading', { name: copy.members.heading, exact: true })).toBeVisible()

    await page.getByRole('banner').getByRole('link').click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
  })
})
