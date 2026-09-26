import { expect, test } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.21/#154's own e2e spec: an at-record-time photo attach against the
// real backend (M6.20) - the multipart upload itself, and the row it
// attaches to, are the one thing the vitest suite (RecordTransaction.test.tsx,
// stubbed fetch) cannot prove end to end.
//
// A minimal, valid 1x1 JPEG, inlined rather than a fixture file on disk -
// playwright's setInputFiles takes a buffer directly, and this is the
// smallest input the server's own processReceiptImage (internal/http) will
// still decode and re-encode as a real photo.
const ONE_PIXEL_JPEG = Buffer.from(
  '/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8QEBD/2wBDAQMDAwQDBAgEBAgQCwkLEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBD/wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAj/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFQEBAQAAAAAAAAAAAAAAAAAAAAX/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIRAxEAPwCdABmX/9k=',
  'base64',
)

test.describe('receipt photos', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'

  test('records a transaction with an optional photo, uploaded after the row posts', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.heading })).toBeVisible()

    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill('12000')

    // The optional photo field (#154) - a hidden <input type="file">, named
    // by ReceiptPicker.tsx's own label, same as every other field on this
    // form.
    await page.getByLabel(copy.receipts.fieldLabel).setInputFiles({
      name: 'nota.jpg',
      mimeType: 'image/jpeg',
      buffer: ONE_PIXEL_JPEG,
    })
    await expect(page.getByText('nota.jpg')).toBeVisible()

    await page.getByRole('button', { name: copy.record.submit }).click()

    // The ordinary success line, not the "belum terunggah" one: the upload
    // itself must have succeeded against the real backend for this message
    // to be the one that renders (App.tsx only shows it when photoFailed is
    // false).
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    await expect(page.getByText(copy.receipts.transactionPhotoFailed)).not.toBeVisible()

    // The row now carries a photo - Riwayat's Transaksi tab is where an
    // after-the-fact viewer lives, and this is the same row this test just
    // posted (Rp 12.000, freshly recorded, so the newest row in the list).
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await expect(page.getByRole('button', { name: copy.receipts.viewReceipt }).first()).toBeVisible()

    // Opens the viewer and shows a real photo read back from GET
    // /api/receipts/{id} - proving the round trip, not only the upload.
    await page.getByRole('button', { name: copy.receipts.viewReceipt }).first().click()
    await expect(page.getByRole('dialog').getByRole('button', { name: copy.receipts.change })).toBeVisible()
  })
})
