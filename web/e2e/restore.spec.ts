import { expect, test, type Page } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.39/M6.40 (#325, #326, ADR-012): restore, driven through a real browser.
// vitest covers RestoreDialog's states against stubbed fetches; this spec
// proves the parts only the real server can - a zip downloaded from the
// Cadangan card goes back up through the hidden file input, the preview
// comes from the live database, the password is checked, every session is
// dropped, and the server-side list then offers the safety net the restore
// just wrote.
//
// Both restores here are round trips: the first restores a backup of the
// state the instance is already in, the second restores the safety net
// that first restore wrote - the same state again. Every spec in this suite
// shares one seeded instance (playwright.config.ts), so leaving the ledger
// exactly as it was found is the point, not a shortcut. The login survives
// both: a restore never touches the live user row (ADR-012).
//
// The server writes its dumps to E2E_BACKUP_DIR (Makefile), never the dev
// server's ./backups, so the list below holds only this run's own dumps.
test.describe('restore', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'
  const seedFundName = 'Kas RT Uji Coba'

  const backupText = copy.settings.backup
  const confirmText = copy.restoreConfirm

  async function logIn(page: Page) {
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
  }

  async function openSettings(page: Page) {
    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()
  }

  // The preview is read off the live database, so a round trip names the
  // seeded fund as kept with nothing lost. Confirming drops every session
  // and reloads, which lands on the login screen.
  async function confirmRoundTrip(page: Page) {
    const dialog = page.getByRole('dialog', { name: confirmText.heading })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText(confirmText.fundKeptSafe(seedFundName))).toBeVisible()

    await dialog.getByLabel(confirmText.passwordLabel).fill(seedPassword)
    await dialog.getByRole('button', { name: confirmText.confirm, exact: true }).click()

    await expect(page.getByRole('button', { name: copy.auth.login.submit })).toBeVisible()
  }

  test('restores an uploaded backup, then logs everyone out', async ({ page }) => {
    await page.goto('/')
    await logIn(page)
    await openSettings(page)

    // exact: every server-side row's own download control is labelled
    // "Unduh cadangan <kind> <date>", which a substring match also hits.
    const downloadEvent = page.waitForEvent('download')
    await page.getByRole('button', { name: backupText.download, exact: true }).click()
    const zipPath = await (await downloadEvent).path()

    // The visible button only clicks this hidden input; Playwright sets the
    // file on the input itself, as receipts.spec.ts does for photos.
    await page.getByLabel(backupText.restoreLabel).and(page.locator('input[type="file"]')).setInputFiles(zipPath)
    await confirmRoundTrip(page)
  })

  test('restores the safety net the previous restore wrote, from the server-side list', async ({ page }) => {
    await page.goto('/')
    await logIn(page)
    await openSettings(page)

    // The first test's restore wrote a pre-restore dump. The list is newest
    // first, so .first() is that one even on a rerun that skipped e2e-reset.
    const preRestorePrefix = backupText.restoreRowAria(backupText.kindPreRestore, '').trim()
    await page
      .getByRole('button', { name: new RegExp(`^${preRestorePrefix}`) })
      .first()
      .click()
    await confirmRoundTrip(page)

    // Still the same instance afterwards: the login works and home renders.
    // From / - the reload after confirm keeps /settings, and logging in
    // there lands back on Pengaturan, not Beranda.
    await page.goto('/')
    await logIn(page)
  })
})
