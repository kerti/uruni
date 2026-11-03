import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, test, logIn } from './fixtures'
import { chooseOption } from './popper'
import { expectAccessible } from './a11y'

// Open an envelope, contribute to it through the real record form (reused, not
// duplicated), then close it and verify the (honest, possibly zero) rollover.
// One continuous story, so serial, on the file's own seeded instance.

test.describe('incidentals', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const occasion = 'Halal bihalal RT E2E'

  // The envelope's row in Beranda's purpose breakdown, anchored to the start of
  // its name: recent activity also lists this envelope's entries, and each
  // card's detail button names its pos ("Lihat rincian: <occasion>, ..."), so
  // an unanchored match finds those too.
  function berandaRow(page: Page) {
    return page.getByRole('button', { name: new RegExp(`^${occasion}`) })
  }

  // @smoke on this one too: the smoke tier's "record money in" is the next test
  // and contributes to the envelope this one opens.
  test('open an envelope from Beranda and verify it appears there', { tag: '@smoke' }, async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // An envelope is opened where the money is (ADR-047): Buka amplop sits
    // under Beranda's Saldo per pos. Add is a dialog (ADR-032), addressed by
    // ?edit=incidental:new.
    await page.getByRole('button', { name: copy.incidentals.open.heading, exact: true }).click()
    await expect(page).toHaveURL(/[?&]edit=incidental%3Anew/)
    const openDialog = page.getByRole('dialog', { name: copy.incidentals.open.heading })
    await expect(openDialog).toBeVisible()
    await expectAccessible(page)

    // Fill occasion and target amount, leave date at today's default
    await openDialog.getByLabel(copy.incidentals.open.occasionLabel).fill(occasion)
    await openDialog.getByLabel(copy.incidentals.open.targetLabel).fill('100000')

    // Exact: the date field is a button too, named by its label "Tanggal
    // dibuka" - which contains "buka" for a substring match. The close form's
    // "Tutup" has the same trap in "Tanggal ditutup".
    await openDialog.getByRole('button', { name: copy.incidentals.open.submit, exact: true }).click()
    await expect(openDialog).not.toBeVisible()
    // Opening it is confirmed (#743). The toast hides itself after ~4s, so this
    // is asserted right after the action, before anything else.
    await expect(page.getByText(copy.incidentals.opened)).toBeVisible()

    // Opening lands on the new envelope itself (#745), whose way back is
    // Beranda - where it is now a row of Saldo per pos.
    await expect(page.getByRole('heading', { level: 1, name: occasion })).toBeVisible()
    await expect(page).toHaveURL(/\/incidentals\?purpose=\d+$/)
    await page.getByRole('button', { name: copy.incidentals.detail.backToHome }).click()
    await expect(berandaRow(page)).toBeVisible()
  })

  test(
    'contribute to the envelope through the real record form, and verify the collected total updates',
    { tag: '@smoke' },
    async ({ page }) => {
      await logIn(page)
      await page.goto('/')
      await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

      // The envelope opened in the previous test is still open, so it renders as
      // its own row in Beranda's purpose breakdown - tapping it opens that
      // envelope's detail, no separate list screen first.
      await berandaRow(page).click()
      await expect(page.getByText(copy.incidentals.detail.collectedLabel)).toBeVisible()

      // "Catat" hands off to the real record form with this envelope's
      // purpose chosen via /record?purpose=<id>. exact: the participation list
      // gives every member row a "Catat sumbangan dari <name>" control, which a
      // substring match also hits.
      await page.getByRole('button', { name: copy.incidentals.actions.record, exact: true }).click()
      await expect(page.getByRole('heading', { name: copy.record.headingOut })).toBeVisible()

      // A contribution is money in - the record form's direction toggle, not a
      // choice this screen makes. Location is hers to name; purpose is already
      // the envelope's.
      await page.getByRole('button', { name: copy.record.directionIn }).click()
      await chooseOption(page, copy.record.locationLabel, 'Tunai')
      await page.getByLabel(copy.record.amountLabel).fill('60000')
      await page.getByRole('button', { name: copy.record.submit }).click()
      await expect(page.getByText(copy.record.successIn)).toBeVisible()

      // Saving returns to the envelope it was recorded from (#745), whose way
      // back is Beranda (ADR-047), and the total reflects it.
      await expect(page).toHaveURL(/\/incidentals\?purpose=\d+$/)
      await expect(page.getByRole('button', { name: copy.incidentals.detail.backToHome })).toBeVisible()
      // Anchored to the collected row, not the page: this amount appears three
      // times - Beranda's breakdown row, the recent-five entry for the
      // contribution, and the detail view this means; anchoring keeps the
      // match on the detail view whatever screen a navigation tears down.
      // toContainText retries, so this waits for the detail view.
      const collectedRow = page.locator(`#root div:has(> dt:text-is(${JSON.stringify(copy.incidentals.detail.collectedLabel)}))`)
      await expect(collectedRow).toContainText('Rp60.000')
      await expectAccessible(page)
    },
  )

  test('close the envelope and verify the rollover is shown honestly', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Still open, still its own row on Beranda - tap it into the detail.
    await berandaRow(page).click()
    await expect(page.getByText(copy.incidentals.detail.collectedLabel)).toBeVisible()

    // Open the close dialog (ADR-046)
    await page.getByRole('button', { name: copy.incidentals.actions.close, exact: true }).click()
    const closeDialog = page.getByRole('dialog', { name: copy.incidentals.close.heading })
    await expect(closeDialog).toBeVisible()

    // Pick the location (it opens on a default; picking is explicit), submit (date defaults to today)
    await chooseOption(page, copy.incidentals.close.accountLabel)
    // The roll is a transfer nobody asked for directly - the note stops it
    // reading as two unexplained rows in the ledger.
    await page.getByLabel(copy.incidentals.close.noteLabel).fill('Sisa halal bihalal')

    await closeDialog.getByRole('button', { name: copy.incidentals.close.submit, exact: true }).click()
    await expect(page.getByText(copy.incidentals.close.success)).toBeVisible()

    // The rollover (Rp60.000 collected, nothing disbursed - all of it rolls
    // out) is rendered, never hidden even if zero. rolled_amount is signed
    // (ADR-031): 60.000 is positive, so the "rolled out" sentence must show.
    // The double-close 409 is covered at the unit level (Incidentals.test.tsx);
    // once closed, this screen offers reopen, not a second close.
    await expect(page.getByText(copy.incidentals.close.rolledLabel(60_000))).toBeVisible()

    // Once closed, the envelope leaves Beranda's breakdown for the
    // closed-envelopes screen behind its one row there - one tap away - and
    // its card there opens the detail again (ADR-047).
    await page.getByRole('button', { name: copy.incidentals.detail.backToHome }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(berandaRow(page)).toBeHidden()
    await page.getByRole('button', { name: new RegExp(copy.incidentals.closedRow) }).click()
    await expect(page.getByRole('heading', { name: copy.incidentals.closedRow })).toBeVisible()
    await expectAccessible(page)
    await page.getByRole('button', { name: new RegExp(occasion) }).click()
    await expect(page.getByRole('button', { name: copy.incidentals.actions.reopen })).toBeVisible()
  })
})
