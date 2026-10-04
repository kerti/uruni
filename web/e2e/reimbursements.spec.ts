import { expect, test, type Page } from '@playwright/test'

import { copy } from '../src/copy/id'

// Records a claim the way she does: Catat -> Keluar -> "Pengeluaran ini
// ditalangi" -> member and amount -> Simpan (#368). Lands on Beranda with
// the confirmation. Returns the first member's name.
async function recordClaim(page: Page, amount: string): Promise<string> {
  await page.getByRole('link', { name: copy.shell.nav.record }).click()
  await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()
  await page.getByRole('button', { name: copy.record.directionOut }).click()
  // click(), not check(): the box mirrors ?fronted=1, which the router
  // commits a tick after the click, and check() reads the state at once.
  const fronted = page.getByLabel(copy.record.frontedLabel)
  await fronted.click()
  await expect(fronted).toBeChecked()
  await expect(page).toHaveURL(/\/record\?fronted=1$/)
  // No location while a member's own money paid: nothing leaves the kas yet.
  await expect(page.getByLabel(copy.record.locationLabel, { exact: true })).toHaveCount(0)
  await page.getByRole('combobox', { name: copy.reimbursements.record.memberLabel }).click()
  const firstOption = page.getByRole('option').first()
  const name = (await firstOption.textContent())?.trim() ?? ''
  await firstOption.click()
  await page.getByLabel(copy.record.amountLabel).fill(amount)
  await page.getByRole('button', { name: copy.record.submit }).click()
  await expect(page.getByText(copy.record.successFronted)).toBeVisible()
  return name
}

async function openTalangan(page: Page) {
  await page.getByRole('link', { name: copy.shell.nav.history }).click()
  await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
  await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()
}

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

  // Set by the first test below and read by the second (serial mode, same
  // shared database): the label a settlement now carries (#257) is just
  // the claim's own member name, so the settle spec needs to know which
  // member the claim spec actually picked - "the first option" is not a
  // fixed name.
  let firstMemberName = ''

  test('record a claim and verify it appears in the outstanding list', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    firstMemberName = await recordClaim(page, '10000')

    // Recording lands on Beranda, which now shows what the fund owes (#406).
    // Visible only: the seed may hold other open claims, so the total is not
    // this claim's amount alone.
    await expect(page.getByRole('button', { name: new RegExp(copy.home.owedToMembers) })).toBeVisible()

    // Then Riwayat's Talangan tab (#226, ADR-032), where the claim lives.
    await openTalangan(page)
    // The claim should now be in the outstanding list
    await expect(page.getByText('Rp 10.000')).toBeVisible()
  })

  test('settle the claim and verify it disappears from outstanding', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to Riwayat's Talangan tab (#226, ADR-032).
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
    await expect(page.getByRole('button', { name: copy.reimbursements.outstandingTab })).toBeVisible()

    // There should be at least one outstanding claim from the previous test
    // (serial mode). Open settle form. `exact: true` is required: name
    // matching is a case-insensitive substring search, so "Bayar" otherwise
    // also matches the "Belum dibayar" tab button.
    await page.getByRole('button', { name: copy.reimbursements.actions.settle, exact: true }).first().click()
    await expect(page).toHaveURL(/\/reimbursements\/settle\?id=\d+$/)
    await expect(page.getByRole('heading', { name: copy.reimbursements.settle.heading })).toBeVisible()

    // Pick where the money is paid from - the account has no default.
    await page.getByRole('combobox', { name: copy.reimbursements.settle.accountLabel }).click()
    await page.getByRole('option').first().click()

    // Submit settle (date defaults to today)
    await page.getByRole('button', { name: copy.reimbursements.settle.submit, exact: true }).click()
    await expect(page).toHaveURL(/\/history\/reimbursements$/)
    await expect(page.getByText(copy.reimbursements.settle.success)).toBeVisible()

    // The settled claim is no longer owed, so it leaves the outstanding list
    await expect(page.getByText('Rp 10.000')).not.toBeVisible()

    // The payout row carries a label built from the claim it settled (#257:
    // a display label, never stored text) - the member's own name, with the
    // HandHelping icon making it read as a Talangan repayment rather than a
    // bare amount.
    await page.getByRole('link', { name: copy.shell.nav.home }).click()
    await expect(page.getByText(copy.rowLabels.settlement.text(firstMemberName)).first()).toBeVisible()
  })

  test('correct an outstanding claim from its own screen', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The earlier claim was settled - record a fresh one to correct (Rp 30.000).
    await recordClaim(page, '30000')
    await openTalangan(page)
    await expect(page.getByText('Rp 30.000')).toBeVisible()

    // Perbaiki sits in the row's "more" menu and opens the claim's own screen.
    await page.locator('li', { hasText: 'Rp 30.000' }).getByRole('button', { name: copy.reimbursements.actions.menuAria }).click()
    await page.getByRole('menuitem', { name: copy.reimbursements.actions.correct, exact: true }).click()
    await expect(page).toHaveURL(/\/reimbursements\/correct\?id=\d+$/)
    await expect(page.getByRole('heading', { name: copy.reimbursements.correct.heading })).toBeVisible()

    // Emptied before it is filled: a single fill() over the pre-filled
    // amount concatenates the two runs of digits (see dues.spec.ts).
    const amount = page.getByLabel(copy.reimbursements.record.amountLabel)
    await amount.fill('')
    await amount.fill('35000')
    await expect(amount).toHaveValue('35000')
    await page.getByRole('button', { name: copy.reimbursements.correct.submit }).click()

    // Back on the tab with the confirmation and the corrected amount.
    await expect(page).toHaveURL(/\/history\/reimbursements$/)
    await expect(page.getByText(copy.reimbursements.correct.success)).toBeVisible()
    await expect(page.getByText('Rp 35.000')).toBeVisible()
    await expect(page.getByText('Rp 30.000')).not.toBeVisible()

    // Settle it so the next test starts from an empty outstanding list.
    await page.getByRole('button', { name: copy.reimbursements.actions.settle, exact: true }).first().click()
    await page.getByRole('combobox', { name: copy.reimbursements.settle.accountLabel }).click()
    await page.getByRole('option').first().click()
    await page.getByRole('button', { name: copy.reimbursements.settle.submit, exact: true }).click()
    await expect(page.getByText(copy.reimbursements.settle.success)).toBeVisible()
  })

  test('waive a fresh claim, then un-waive it from the all tab', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The earlier claim was settled, leaving the outstanding list empty -
    // record a fresh claim to waive. First member again, Rp 20.000.
    await recordClaim(page, '20000')
    await openTalangan(page)
    await expect(page.getByText('Rp 20.000')).toBeVisible()

    // Waive it: the row leaves the outstanding list and feedback confirms.
    // Putihkan sits in the row's "more" menu (#314), behind the one plain Bayar.
    await page.locator('li', { hasText: 'Rp 20.000' }).getByRole('button', { name: copy.reimbursements.actions.menuAria }).click()
    await page.getByRole('menuitem', { name: copy.reimbursements.actions.waive, exact: true }).click()
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
