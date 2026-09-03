import { expect, test } from '@playwright/test'

import { copy } from '../src/copy/id'

// M6.15: the settings screen's locations section (PRD §7.1, #78's "can a
// location be added later" answered yes).
//
// This spec walks the one path that has to work in a real browser - home →
// settings → add a location → see it in the list - and hands everything
// else to the vitest suite (Locations.test.tsx), which can stub a 409, a
// retired row and a rename without touching the shared database.
//
// It also cleans up after itself, deliberately: every spec here runs
// against one seeded instance, and reconcile (golden-path.spec.ts) will not
// submit until every *active* account has been counted. A location left
// behind by this file would fail that spec on the next run with no
// connection to anything it does. The suite is serial now
// (playwright.config.ts) so nothing overlaps mid-run, but a leftover row
// outlives the run entirely - hence the delete, which is also the only
// place the delete path gets exercised against the real server.
test.describe('settings', () => {
  test.describe.configure({ mode: 'serial' })

  const seedEmail = 'bendahara@e2e.uruni.test'
  const seedPassword = 'e2e-fixture-password'

  // Not "Tunai" or "Bank Uji Coba" - those are the fixture's own two
  // locations, which other specs assert on by name.
  const locationName = 'Kotak Uji Pengaturan'

  test('adds a location from the settings screen, then removes it again', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(seedEmail)
    await page.getByLabel(copy.auth.login.passwordLabel).fill(seedPassword)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The footer nav is how every screen is reached now (M6.15).
    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()

    // The fixture's own locations are already listed. Asserted on the bank
    // one, not "Tunai": that string is also the cash kind's label, so it
    // appears on the row's badge and in the add form's kind trigger as well
    // as on the location itself.
    await expect(page.getByText('Bank Uji Coba')).toBeVisible()

    const addForm = page.getByRole('form', { name: copy.settings.locations.add })
    // 'Tunai' is already the default kind, but choosing it explicitly is what
    // proves the themed Select works against a real browser - the vitest
    // suite can only prove it against jsdom.
    await addForm.getByRole('combobox', { name: copy.settings.locations.kindLabel }).click()
    // Scoped to the open listbox: Radix also renders a hidden native select
    // for form submission, whose <option> elements carry the same role and
    // the same text.
    await page.getByRole('listbox').getByRole('option', { name: copy.settings.locations.kindCash }).click()
    await addForm.getByLabel(copy.settings.locations.nameLabel).fill(locationName)
    await addForm.getByRole('button', { name: copy.settings.locations.add }).click()

    const row = page.getByRole('listitem').filter({ hasText: locationName })
    await expect(row).toBeVisible()

    // Never used, so the server allows the delete (a location with history
    // answers 409 and the screen says "nonaktifkan, bukan hapus" instead -
    // covered in vitest, where a used location can be stubbed).
    await row.getByRole('button', { name: copy.settings.locations.delete }).click()
    await expect(page.getByText(locationName)).toHaveCount(0)
  })
})
