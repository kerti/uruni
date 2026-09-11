import { expect, test } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.18's own e2e spec: record a reimbursement claim, verify it appears in
// the outstanding list, settle it and verify it disappears, then waive and
// un-waive a fresh claim across the two tabs. All three are one continuous
// story on a shared seeded database.
//
// Serial, like golden-path.spec.ts: this spec shares one seeded database
// and reads it as a continuous story. The seeded instance has members and
// purposes from the e2e fixture (cmd/uruni/seed_e2e.go).
test.describe('reimbursements', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'

  test('record a claim and verify it appears in the outstanding list', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to Riwayat's Penggantian tab (#226, ADR-032) - the reimbursements
    // screen's own former home-screen entry point moved here.
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
    await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()

    // Open record form
    await page.getByRole('button', { name: copy.reimbursements.record.heading }).click()
    await expect(page.getByText(copy.reimbursements.record.heading)).toBeVisible()

    // Pick the first member and fill amount
    await page.getByRole('combobox', { name: copy.reimbursements.record.memberLabel }).click()
    await page.getByRole('option').first().click()
    await page.getByLabel(copy.reimbursements.record.amountLabel).fill('10000')

    // Submit
    await page.getByRole('button', { name: copy.reimbursements.record.submit }).click()
    await expect(page.getByText(copy.reimbursements.record.success)).toBeVisible()

    // The claim should now be in the outstanding list
    await expect(page.getByText('Rp 10.000')).toBeVisible()
  })

  test('settle the claim and verify it disappears from outstanding', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to Riwayat's Penggantian tab (#226, ADR-032).
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
    await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()

    // There should be at least one outstanding claim from the previous test
    // (serial mode). Open settle form. `exact: true` is required: name
    // matching is a case-insensitive substring search, so "Bayar" otherwise
    // also matches the "Belum dibayar" tab button.
    await page.getByRole('button', { name: copy.reimbursements.actions.settle, exact: true }).first().click()
    await expect(page.getByText(copy.reimbursements.settle.heading)).toBeVisible()

    // Pick where the money is paid from - the account has no default.
    await page.getByRole('combobox', { name: copy.reimbursements.settle.accountLabel }).click()
    await page.getByRole('option').first().click()

    // Submit settle (date defaults to today)
    await page.getByRole('button', { name: copy.reimbursements.settle.submit, exact: true }).click()
    await expect(page.getByText(copy.reimbursements.settle.success)).toBeVisible()

    // The settled claim is no longer owed, so it leaves the outstanding list
    await expect(page.getByText('Rp 10.000')).not.toBeVisible()

    // The payout row carries a composed description (member + the claim's own
    // note), so recent activity reads the repayment instead of a bare amount.
    await page.getByRole('link', { name: copy.shell.nav.home }).click()
    await expect(page.getByText(/Penggantian - /)).toBeVisible()
  })

  test('waive a fresh claim, then un-waive it from the all tab', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to Riwayat's Penggantian tab (#226, ADR-032).
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
    await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()

    // The earlier claim was settled, leaving the outstanding list empty -
    // record a fresh claim to waive. First member again, Rp 20.000.
    await page.getByRole('button', { name: copy.reimbursements.record.heading }).click()
    await page.getByRole('combobox', { name: copy.reimbursements.record.memberLabel }).click()
    await page.getByRole('option').first().click()
    await page.getByLabel(copy.reimbursements.record.amountLabel).fill('20000')
    await page.getByRole('button', { name: copy.reimbursements.record.submit }).click()
    await expect(page.getByText(copy.reimbursements.record.success)).toBeVisible()
    await expect(page.getByText('Rp 20.000')).toBeVisible()

    // Waive it: the row leaves the outstanding list and feedback confirms.
    await page.getByRole('button', { name: copy.reimbursements.actions.waive, exact: true }).click()
    await expect(page.getByText(copy.reimbursements.waive.success)).toBeVisible()
    await expect(page.getByText('Rp 20.000')).not.toBeVisible()

    // The all tab carries the waived row, with "Batalkan pemutihan" where
    // the outstanding tab (which filters waived rows out) can never reach.
    // The row-scoped badge locators avoid the tab button's identical label.
    await page.getByRole('button', { name: copy.reimbursements.allTab }).click()
    const row = page.locator('li', { hasText: 'Rp 20.000' })
    await expect(row.getByText(copy.reimbursements.status.waived)).toBeVisible()
    await expect(page.getByRole('button', { name: copy.reimbursements.actions.unwaive, exact: true })).toBeVisible()

    // Un-waive: the claim is owed again and its own badge flips back.
    await page.getByRole('button', { name: copy.reimbursements.actions.unwaive, exact: true }).click()
    await expect(page.getByText(copy.reimbursements.unwaive.success)).toBeVisible()
    await expect(row.getByText(copy.reimbursements.status.outstanding)).toBeVisible()
  })
})
