import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import { expect, test, logIn } from './fixtures'
import { chooseOption } from './popper'

// Records a claim the way she does: Catat -> Keluar -> "Pengeluaran ini
// ditalangi" -> member and amount -> Simpan. Lands on Beranda with the
// confirmation. Returns the first member's name.
async function recordClaim(page: Page, amount: string): Promise<string> {
  await page.getByRole('link', { name: copy.shell.nav.record }).click()
  await expect(page.getByRole('heading', { name: copy.record.headingOut })).toBeVisible()
  await page.getByRole('button', { name: copy.record.directionOut }).click()
  // click(), not check(): the box mirrors ?fronted=1, which the router
  // commits a tick after the click, and check() reads the state at once.
  const fronted = page.getByLabel(copy.record.frontedLabel)
  await fronted.click()
  await expect(fronted).toBeChecked()
  await expect(page).toHaveURL(/\/record\?fronted=1$/)
  // No location while a member's own money paid: nothing leaves the kas yet.
  await expect(page.getByLabel(copy.record.locationLabel, { exact: true })).toHaveCount(0)
  const name = await chooseOption(page, copy.reimbursements.record.memberLabel)
  await page.getByLabel(copy.record.amountLabel).fill(amount)
  await page.getByRole('button', { name: copy.record.submit }).click()
  await expect(page.getByText(copy.record.successFronted)).toBeVisible()
  return name
}

async function openTalangan(page: Page) {
  await page.getByRole('link', { name: copy.shell.nav.history }).click()
  await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
  await expect(page.getByRole('checkbox', { name: copy.reimbursements.outstandingFilterLabel })).toBeChecked()
}

// A claim's card is one tap target that opens its detail (#768), and every
// action lives there. `amount` picks the card by its figure; none picks the
// first. Returns the detail dialog, for the caller to act in.
async function openClaim(page: Page, amount?: string) {
  const cards = page.getByRole('main').getByRole('listitem')
  const card = amount ? cards.filter({ hasText: amount }) : cards
  await card
    .first()
    .getByRole('button', { name: /^Lihat rincian talangan/ })
    .click()
  const detail = page.getByRole('dialog', { name: copy.reimbursements.detail.heading })
  await expect(detail).toBeVisible()
  return detail
}

// Record a reimbursement claim, verify it appears in the outstanding list,
// settle it and verify it disappears, then waive and un-waive a fresh claim
// across the two tabs. One continuous story, so serial, on the file's seeded
// instance (members and purposes from cmd/uruni/seed_e2e.go).
test.describe('reimbursements', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  // Set by the first test and read by the second (serial mode): a settlement's
  // label is the claim's member name, so the settle spec needs to know which
  // member the claim spec picked - "the first option" is not a fixed name.
  let firstMemberName = ''

  test('record a claim and verify it appears in the outstanding list', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    firstMemberName = await recordClaim(page, '10000')

    // Recording lands on Beranda, which shows what the fund owes.
    await expect(page.getByRole('button', { name: new RegExp(copy.home.owedToMembers) })).toBeVisible()

    // Then Riwayat's Talangan tab, where the claim lives.
    await openTalangan(page)
    // The claim should now be in the outstanding list
    await expect(page.getByText('Rp10.000')).toBeVisible()
  })

  test('settle the claim and verify it disappears from outstanding', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to Riwayat's Talangan tab.
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await page.getByRole('link', { name: copy.history.tabs.reimbursements }).click()
    await expect(page.getByRole('checkbox', { name: copy.reimbursements.outstandingFilterLabel })).toBeChecked()

    // There is at least one outstanding claim from the previous test (serial
    // mode). Open settle form. `exact: true` is required: name matching is a
    // case-insensitive substring search, so "Bayar" otherwise also matches the
    // "Belum dibayar" row badge.
    const detail = await openClaim(page)
    await detail.getByRole('button', { name: copy.reimbursements.actions.settle, exact: true }).click()
    // Bayar opens a dialog on the tab (ADR-046), addressed by the URL.
    await expect(page).toHaveURL(/\/history\/reimbursements\?edit=settle(%3A|:)\d+$/)
    const settleDialog = page.getByRole('dialog', { name: copy.reimbursements.settle.heading })
    await expect(settleDialog).toBeVisible()

    // Pick where the money is paid from (it opens on a default; picking is explicit).
    await chooseOption(page, copy.reimbursements.settle.accountLabel)

    // Submit settle (date defaults to today)
    await settleDialog.getByRole('button', { name: copy.reimbursements.settle.submit, exact: true }).click()
    await expect(page).toHaveURL(/\/history\/reimbursements$/)
    await expect(page.getByText(copy.reimbursements.settle.success)).toBeVisible()

    // The settled claim is no longer owed, so it leaves the outstanding list
    await expect(page.getByText('Rp10.000')).not.toBeVisible()

    // The payout row carries a label built from the claim it settled (a display
    // label, never stored text) - the member's name, with the HandHelping icon
    // making it read as a Talangan repayment rather than a bare amount.
    await page.getByRole('link', { name: copy.shell.nav.home }).click()
    await expect(page.getByText(copy.rowLabels.settlement.text(firstMemberName)).first()).toBeVisible()
  })

  test('correct an outstanding claim from its own screen', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The earlier claim was settled - record a fresh one to correct (Rp30.000).
    await recordClaim(page, '30000')
    await openTalangan(page)
    await expect(page.getByText('Rp30.000')).toBeVisible()

    // Ubah is in the claim's detail and opens the claim's screen.
    const detail = await openClaim(page, 'Rp30.000')
    await detail.getByRole('button', { name: copy.reimbursements.actions.correct, exact: true }).click()
    await expect(page).toHaveURL(/\/reimbursements\/correct\?id=\d+$/)
    await expect(page.getByRole('heading', { name: copy.reimbursements.correct.heading })).toBeVisible()

    // Emptied before it is filled: one fill() over the pre-filled amount
    // concatenates the two runs of digits (see dues.spec.ts).
    const amount = page.getByLabel(copy.reimbursements.record.amountLabel)
    await amount.fill('')
    await amount.fill('35000')
    await expect(amount).toHaveValue(formatIDR(35_000))
    await page.getByRole('button', { name: copy.reimbursements.correct.submit }).click()

    // Back on the tab with the confirmation and the corrected amount.
    await expect(page).toHaveURL(/\/history\/reimbursements$/)
    await expect(page.getByText(copy.reimbursements.correct.success)).toBeVisible()
    await expect(page.getByText('Rp35.000')).toBeVisible()
    await expect(page.getByText('Rp30.000')).not.toBeVisible()

    // Settle it so the next test starts from an empty outstanding list.
    const settleFrom = await openClaim(page)
    await settleFrom.getByRole('button', { name: copy.reimbursements.actions.settle, exact: true }).click()
    await chooseOption(page, copy.reimbursements.settle.accountLabel)
    await page
      .getByRole('dialog', { name: copy.reimbursements.settle.heading })
      .getByRole('button', { name: copy.reimbursements.settle.submit, exact: true })
      .click()
    await expect(page.getByText(copy.reimbursements.settle.success)).toBeVisible()
  })

  test('waive a fresh claim, then un-waive it from the full list', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The earlier claim was settled, leaving the outstanding list empty -
    // record a fresh claim to waive. First member again, Rp20.000.
    await recordClaim(page, '20000')
    await openTalangan(page)
    await expect(page.getByText('Rp20.000')).toBeVisible()

    // Waive it: the row leaves the outstanding list and feedback confirms.
    // Putihkan is in the claim's detail, which closes once it is done.
    const detail = await openClaim(page, 'Rp20.000')
    await detail.getByRole('button', { name: copy.reimbursements.actions.waive, exact: true }).click()
    await expect(page.getByText(copy.reimbursements.waive.success)).toBeVisible()
    await expect(page.getByText('Rp20.000')).not.toBeVisible()

    // The full list carries the waived row, with "Batalkan pemutihan" in its
    // detail where the outstanding filter (which drops waived rows) can never
    // reach. The row-scoped badge locators keep the match to that row.
    await page.getByRole('checkbox', { name: copy.reimbursements.outstandingFilterLabel }).uncheck()
    const row = page.locator('li', { hasText: 'Rp20.000' })
    await expect(row.getByText(copy.reimbursements.status.waived)).toBeVisible()
    const waived = await openClaim(page, 'Rp20.000')
    await expect(waived.getByRole('button', { name: copy.reimbursements.actions.unwaive, exact: true })).toBeVisible()

    // Un-waive: the claim is owed again and its own badge flips back.
    await waived.getByRole('button', { name: copy.reimbursements.actions.unwaive, exact: true }).click()
    await expect(page.getByText(copy.reimbursements.unwaive.success)).toBeVisible()
    await expect(row.getByText(copy.reimbursements.status.outstanding)).toBeVisible()
  })

  test('Catat from the tab returns to the tab, its filter intact (#745)', async ({ page }) => {
    await logIn(page)
    await page.goto('/history/reimbursements?q=Warga')
    await page.getByRole('button', { name: copy.reimbursements.record.heading, exact: true }).click()
    await expect(page.getByLabel(copy.record.frontedLabel)).toBeChecked()
    await chooseOption(page, copy.reimbursements.record.memberLabel)
    await page.getByLabel(copy.record.amountLabel).fill('30000')
    await page.getByRole('button', { name: copy.record.submit }).click()

    await expect(page.getByText(copy.record.successFronted)).toBeVisible()
    await expect(page).toHaveURL(/\/history\/reimbursements\?q=Warga$/)
    await expect(page.getByText('Rp30.000')).toBeVisible()
  })
})
