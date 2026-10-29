import { readdirSync } from 'node:fs'

import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, test, logIn } from './fixtures'
import { chooseMenuItem } from './popper'

// An at-record-time photo attach against the real backend: the multipart
// upload, and the row it attaches to, are what the vitest suite
// (RecordTransaction.test.tsx, stubbed fetch) cannot prove end to end.
//
// An 8x8 JPEG written by Go's image/jpeg encoder, inlined rather than a fixture
// file (setInputFiles takes a buffer), so the server's decoder
// (processReceiptImage) is guaranteed to accept it - a hand-made 1x1 one was
// rejected as unsupported_media_type.
const TINY_JPEG = Buffer.from(
  '/9j/2wCEAAYEBQYFBAYGBQYHBwYIChAKCgkJChQODwwQFxQYGBcUFhYaHSUfGhsjHBYWICwgIyYnKSopGR8tMC0oMCUoKSgBBwcHCggKEwoKEygaFhooKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKP/AABEIAAgACAMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/APNqKKK1PnT/2Q==',
  'base64',
)

/** The newest row's detail, then its Lihat nota: the row is one tap target and
 * every action lives in the detail (#739). */
async function openReceiptViewer(page: Page) {
  await page
    .getByRole('button', { name: /^Lihat rincian:/ })
    .first()
    .click()
  await page.getByRole('dialog', { name: copy.transactionDetail.heading }).getByRole('button', { name: copy.receipts.viewReceipt }).click()
  return page.getByRole('dialog', { name: copy.receipts.dialogHeading })
}

test.describe('receipt photos', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('records a transaction with an optional photo, uploaded after the row posts', async ({ page, instance }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await expect(page.getByRole('heading', { name: copy.record.headingOut })).toBeVisible()

    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill('12000')

    // The optional photo field - a hidden <input type="file"> named "Tambah foto
    // nota" by aria-label. Row buttons share that name, so the match is narrowed
    // to the file input.
    await page.getByLabel(copy.receipts.addFromRow).and(page.locator('input[type="file"]')).setInputFiles({
      name: 'nota.jpg',
      mimeType: 'image/jpeg',
      buffer: TINY_JPEG,
    })
    await expect(page.getByText('nota.jpg')).toBeVisible()

    await page.getByRole('button', { name: copy.record.submit }).click()

    // The ordinary success line, not the "belum terunggah" one: the upload must
    // have succeeded against the real backend for this message to render
    // (App.tsx shows it only when photoFailed is false).
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    await expect(page.getByText(copy.receipts.transactionPhotoFailed)).not.toBeVisible()
    // The photo landed in this instance's uploads directory, never the dev one
    // the binary would default to.
    expect(readdirSync(instance.paths.uploadsDir, { recursive: true }).length).toBeGreaterThan(0)

    // The row now carries a photo - Riwayat's Transaksi tab is where an
    // after-the-fact viewer lives, and this is the row just posted (Rp12.000,
    // the newest in the list).
    await page.getByRole('link', { name: copy.shell.nav.history }).click()
    await expect(page.getByRole('button', { name: new RegExp(copy.transactionDetail.receiptCountAria(1)) }).first()).toBeVisible()

    // Opens the row's detail, then the viewer from its Lihat nota (#739) and shows a real photo read back from GET
    // /api/receipts/{id} - proving the round trip, not only the upload. Ganti
    // foto/Hapus foto live behind the per-photo "more" menu, so opening it
    // proves the photo and its actions are there.
    const dialog = await openReceiptViewer(page)
    await dialog.getByRole('button', { name: copy.receipts.photoMenuAria }).click()
    await expect(page.getByRole('menuitem', { name: copy.receipts.change })).toBeVisible()

    // Tapping the photo itself opens the full-screen viewer layered above
    // this dialog.
    await page.keyboard.press('Escape')
    await dialog.getByRole('button', { name: copy.receipts.zoomAria }).click()
    await expect(page.getByRole('button', { name: copy.common.close }).last()).toBeVisible()
  })

  // The photo on a posted row can be replaced and deleted - the row is
  // immutable, the photo is not. Continues from the row the test above posted.
  test('replaces the photo on a posted row, then deletes it', async ({ page, instance }) => {
    await logIn(page)
    await page.goto('/history')

    const dialog = await openReceiptViewer(page)
    const photo = dialog.getByRole('button', { name: copy.receipts.zoomAria }).locator('img')
    await expect(photo).toHaveCount(1)
    const before = await photo.getAttribute('src')

    // Ganti foto: a replace is a delete then a fresh upload, so the photo comes
    // back under a new id - and the row still has exactly one.
    await chooseMenuItem(page, dialog.getByRole('button', { name: copy.receipts.photoMenuAria }), copy.receipts.change)
    // The replace form takes the add picker's place - wait for it, or the file
    // lands in the add picker still on screen.
    const replace = dialog.getByRole('button', { name: copy.receipts.change })
    await expect(replace).toBeVisible()
    await expect(dialog.locator('input[type="file"]')).toHaveCount(1)
    await dialog.locator('input[type="file"]').setInputFiles({ name: 'nota-baru.jpg', mimeType: 'image/jpeg', buffer: TINY_JPEG })
    await replace.click()
    // The replace form closes once the new photo is in.
    await expect(replace).toBeHidden()
    await expect(photo).toHaveCount(1)
    await expect(photo).not.toHaveAttribute('src', before ?? '')
    expect(readdirSync(instance.paths.uploadsDir, { recursive: true, withFileTypes: true }).filter((e) => e.isFile())).toHaveLength(1)

    // Hapus foto: confirmed in place, and the row is back to having none.
    await chooseMenuItem(page, dialog.getByRole('button', { name: copy.receipts.photoMenuAria }), copy.receipts.delete)
    await expect(dialog.getByText(copy.receipts.deleteConfirm)).toBeVisible()
    await dialog.getByRole('button', { name: copy.receipts.delete }).click()
    await expect(dialog.getByText(copy.receipts.emptyTransaction)).toBeVisible()
    await expect(photo).toHaveCount(0)
    expect(readdirSync(instance.paths.uploadsDir, { recursive: true, withFileTypes: true }).filter((e) => e.isFile())).toHaveLength(0)

    // Closed, the row's detail offers to add a photo again.
    await page.keyboard.press('Escape')
    await page
      .getByRole('button', { name: /^Lihat rincian:/ })
      .first()
      .click()
    const detail = page.getByRole('dialog', { name: copy.transactionDetail.heading })
    await expect(detail.getByRole('button', { name: copy.receipts.addFromRow })).toBeVisible()
    await expect(detail.getByRole('button', { name: copy.receipts.viewReceipt })).toHaveCount(0)
  })
})
