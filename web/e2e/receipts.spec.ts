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
// An 8x8 JPEG written by Go's own image/jpeg encoder, so the server's
// decoder is guaranteed to accept it (a hand-made 1x1 one was rejected as
// unsupported_media_type).
const TINY_JPEG = Buffer.from(
  '/9j/2wCEAAYEBQYFBAYGBQYHBwYIChAKCgkJChQODwwQFxQYGBcUFhYaHSUfGhsjHBYWICwgIyYnKSopGR8tMC0oMCUoKSgBBwcHCggKEwoKEygaFhooKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKP/AABEIAAgACAMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/APNqKKK1PnT/2Q==',
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

    // The optional photo field (#154) - a hidden <input type="file"> named
    // "Tambah foto nota" by aria-label. Row buttons share that name, so
    // the match is narrowed to the file input itself.
    await page.getByLabel(copy.receipts.addFromRow).and(page.locator('input[type="file"]')).setInputFiles({
      name: 'nota.jpg',
      mimeType: 'image/jpeg',
      buffer: TINY_JPEG,
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

    // Opens the dialog and shows a real photo read back from GET
    // /api/receipts/{id} - proving the round trip, not only the upload.
    // Ganti foto/Hapus foto now live behind the per-photo "more" menu on
    // the photo itself (#154 follow-up), not a button row under it, so
    // opening that menu is what proves the photo - and its actions -
    // are really there.
    await page.getByRole('button', { name: copy.receipts.viewReceipt }).first().click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('button', { name: copy.receipts.photoMenuAria }).click()
    await expect(page.getByRole('menuitem', { name: copy.receipts.change })).toBeVisible()

    // Tapping the photo itself opens the full-screen viewer layered above
    // this dialog.
    await page.keyboard.press('Escape')
    await dialog.getByRole('button', { name: copy.receipts.zoomAria }).click()
    await expect(page.getByRole('button', { name: copy.common.close }).last()).toBeVisible()
  })
})
