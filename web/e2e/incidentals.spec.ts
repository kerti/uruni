import { expect, test } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.19's own e2e spec: open an envelope, contribute to it through the real
// record form (M6.8, reused rather than duplicated), then close it and
// verify the (honest, possibly zero) rollover is shown. One continuous story
// on a shared seeded database, same idiom as reimbursements.spec.ts.
//
// Serial, like golden-path.spec.ts and reimbursements.spec.ts: this spec
// shares one seeded database and reads it as a continuous story. The seeded
// instance has an account from the e2e fixture (cmd/uruni/seed_e2e.go).
test.describe('incidentals', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'
  const occasion = 'Halal bihalal RT E2E'

  test('open an envelope and verify it appears in the open list', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to incidentals from home
    await page.getByRole('button', { name: copy.home.incidentalLink }).click()
    await expect(page.getByRole('heading', { name: copy.incidentals.heading })).toBeVisible()

    // Open the "open envelope" form
    await page.getByRole('button', { name: copy.incidentals.open.heading }).click()
    await expect(page.getByText(copy.incidentals.open.heading)).toBeVisible()

    // Fill occasion and target amount, leave date at today's default
    await page.getByLabel(copy.incidentals.open.occasionLabel).fill(occasion)
    await page.getByLabel(copy.incidentals.open.targetLabel).fill('100000')

    // Submit
    await page.getByRole('button', { name: copy.incidentals.open.submit }).click()
    await expect(page.getByText(copy.incidentals.open.success)).toBeVisible()

    // The envelope should now be in the open list
    await expect(page.getByText(occasion)).toBeVisible()
  })

  test('contribute to the envelope through the real record form, and verify the collected total updates', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to incidentals and open the envelope's detail
    await page.getByRole('button', { name: copy.home.incidentalLink }).click()
    await page.getByRole('button', { name: new RegExp(occasion) }).click()
    await expect(page.getByText(copy.incidentals.detail.collectedLabel)).toBeVisible()

    // "Catat transaksi" hands off to the real record form (M6.8), with this
    // envelope's purpose already chosen via /record?purpose=<id> - no
    // separate contribution form of this screen's own.
    await page.getByRole('button', { name: copy.incidentals.actions.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()

    // A contribution is money in - the record form's own direction toggle,
    // not a choice this screen makes. Location keeps its remembered/default
    // account; purpose is already the envelope's.
    await page.getByRole('button', { name: copy.record.directionIn }).click()
    await page.getByLabel(copy.record.amountLabel).fill('60000')
    await page.getByRole('button', { name: copy.record.submit }).click()
    await expect(page.getByText(copy.record.successIn)).toBeVisible()

    // Back on incidentals, the collected total now reflects the contribution.
    await page.getByRole('button', { name: copy.home.incidentalLink }).click()
    await page.getByRole('button', { name: new RegExp(occasion) }).click()
    await expect(page.getByText('Rp 60.000')).toBeVisible()
  })

  test('close the envelope and verify the rollover is shown honestly', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // Navigate to incidentals and open the envelope's detail
    await page.getByRole('button', { name: copy.home.incidentalLink }).click()
    await page.getByRole('button', { name: new RegExp(occasion) }).click()
    await expect(page.getByText(copy.incidentals.detail.collectedLabel)).toBeVisible()

    // Open the close form
    await page.getByRole('button', { name: copy.incidentals.actions.close }).click()
    await expect(page.getByText(copy.incidentals.close.heading)).toBeVisible()

    // Pick where the leftover rolls to, submit (date defaults to today)
    await page.getByRole('combobox', { name: copy.incidentals.close.accountLabel }).click()
    await page.getByRole('option').first().click()
    // The roll is a transfer nobody asked for directly (#210) - the note is
    // what stops it reading as two unexplained rows in the ledger.
    await page.getByLabel(copy.incidentals.close.noteLabel).fill('Sisa halal bihalal')

    await page.getByRole('button', { name: copy.incidentals.close.submit }).click()
    await expect(page.getByText(copy.incidentals.close.success)).toBeVisible()

    // The rollover (Rp 60.000 collected, nothing disbursed) is rendered,
    // never hidden even if it turns out to be zero. The double-close 409
    // refusal itself is covered at the unit level (Incidentals.test.tsx) -
    // once closed, this screen hides "Catat transaksi"/"Tutup amplop" for
    // the envelope entirely, so a second close is not reachable from the UI.
    await expect(page.getByText(copy.incidentals.close.rolledLabel)).toBeVisible()

    // The envelope now shows closed on the all tab, and its open-only
    // actions are gone.
    await page.getByRole('button', { name: copy.incidentals.detail.backToList }).click()
    await page.getByRole('button', { name: copy.incidentals.allTab }).click()
    const row = page.locator('li', { hasText: occasion })
    await expect(row.getByText(copy.incidentals.status.closed)).toBeVisible()
  })
})
