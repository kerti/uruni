import { copy } from '../src/copy/id'
import { expect, test, logIn } from './fixtures'
import { chooseOption } from './popper'

// The settings screen's locations section (PRD section 7.1: a location can be
// added later).
//
// This walks the one path that has to work in a real browser - home ->
// settings -> add a location -> see it in the list - and hands the rest to the
// vitest suite (Locations.test.tsx), which can stub a 409, a retired row and a
// rename without a real server. The delete at the end is part of the journey
// and the only place the delete path runs against the real server.
test.describe('settings', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  // Not "Tunai" or "Bank Uji Coba" - the fixture's two locations, which the
  // test below asserts are already listed.
  const locationName = 'Kotak Uji Pengaturan'

  test('adds a location from the settings screen, then removes it again', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

    // The footer nav is how every screen is reached.
    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()

    // The fixture's locations are already listed. Asserted on the bank one, not
    // "Tunai": that string is also the cash kind's label, so it appears on the
    // row's badge and in the add form's kind trigger too.
    await expect(page.getByText('Bank Uji Coba')).toBeVisible()

    // Add is a dialog (ADR-032), addressed by ?edit=location:new.
    await page.getByRole('button', { name: copy.settings.locations.add }).click()
    await expect(page).toHaveURL(/[?&]edit=location%3Anew/)
    const addDialog = page.getByRole('dialog', { name: copy.settings.locations.add })
    await expect(addDialog).toBeVisible()

    // 'Tunai' is already the default kind, but choosing it explicitly proves the
    // themed Select works in a real browser - vitest proves it only in jsdom.
    await chooseOption(page, addDialog.getByRole('combobox', { name: copy.settings.locations.kindLabel }), copy.settings.locations.kindCash)
    await addDialog.getByLabel(copy.settings.locations.nameLabel).fill(locationName)
    await addDialog.getByRole('button', { name: copy.settings.locations.add }).click()

    // A successful add closes the dialog and drops the param.
    await expect(addDialog).toBeHidden()
    const row = page.getByRole('button', { name: copy.settings.locations.editAria(locationName) })
    await expect(row).toBeVisible()

    // Rename and retire/delete live inside the edit dialog it opens.
    await row.click()
    await expect(page).toHaveURL(/[?&]edit=location%3A\d+/)
    const editDialog = page.getByRole('dialog', { name: copy.settings.locations.editTitle })
    await expect(editDialog).toBeVisible()

    // Back closes the sheet and leaves her on Pengaturan, not the screen before
    // it. Android's back gesture is this same history step.
    await page.goBack()
    await expect(editDialog).toBeHidden()
    await expect(page).toHaveURL(/\/settings$/)
    await row.click()
    await expect(editDialog).toBeVisible()

    // Never used, so the server allows the delete (a location with history
    // answers 409 and the dialog shows "nonaktifkan, bukan hapus" - covered in
    // vitest). Delete asks for the inline confirm first, never window.confirm().
    await editDialog.getByRole('button', { name: copy.settings.locations.delete }).click()
    await editDialog.getByRole('button', { name: copy.settings.locations.deleteConfirmAction }).click()
    await expect(editDialog).toBeHidden()
    await expect(page.getByText(locationName)).toHaveCount(0)
  })
})
