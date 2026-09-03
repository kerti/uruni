import { expect, test } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.12: the dues status roster (PRD §7.3). The seeded fixture
// (cmd/uruni/seed_e2e.go) creates two members - "Warga Satu" and "Warga
// Dua" - on one dues tier with a current rate, and no other spec in this
// suite ever posts a dues payment for them, so both are reliably `unpaid`
// for whatever period the selector defaults to. That is enough to prove
// the period view loads and renders a real row without this spec needing
// to derive or post anything itself - the four-status rendering, the
// tier-less-member exclusion and the "belum bayar" filter are already
// covered per-case by the vitest suite (Status.test.tsx), which can stub
// every status directly.
test.describe('dues status', () => {
  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'

  test('loads the period view and shows the seeded roster', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('button', { name: copy.dues.entryLink }).click()
    await expect(page.getByRole('heading', { name: copy.dues.heading })).toBeVisible()

    // Both seeded members owe this period's rate and neither has ever paid.
    await expect(page.getByText('Warga Satu')).toBeVisible()
    await expect(page.getByText('Warga Dua')).toBeVisible()
    await expect(page.getByText(copy.dues.statuses.unpaid).first()).toBeVisible()

    await page.getByRole('button', { name: copy.dues.back }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
  })
})
