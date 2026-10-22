import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, test } from './fixtures'

// The fund's time zone (ADR-041), end to end: chosen in Setup's first step,
// shown in Pengaturan's "Kas ini" section, changed there, and still the server's
// after a reload. What a zone does to a day or a month is exact in Go
// (ledger/time_zone_test.go, http/time_zone_test.go, backup/time_zone_test.go)
// and Vitest (lib/dates.test.ts, App.test.tsx); this walks only what needs a
// real browser against the real server: the themed Select, the dialog on the
// URL, and the round trip through POST /api/setup and PATCH /api/fund.

const email = 'bendahara-zona@e2e.uruni.test'
const password = 'kata-sandi-zona'
const fundName = 'Kas RT Zona Waktu'

const zones = copy.timeZone

/** Opens the zone field and taps one option, by the start of its label (an
 * option's accessible name carries its example line too). Scoped to the open
 * listbox: Radix also renders a hidden native select whose <option> elements
 * carry the same role and text. */
async function chooseZone(page: Page, zone: keyof typeof zones.options) {
  await page.getByRole('combobox', { name: zones.label }).click()
  await page
    .getByRole('listbox')
    .getByRole('option', { name: new RegExp(`^${zones.options[zone].label}`) })
    .click()
}

test.describe("the fund's time zone", () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset({ seed: false }))

  test('is chosen in Setup and shown in Pengaturan', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.register.emailLabel).fill(email)
    await page.getByLabel(copy.auth.register.passwordLabel, { exact: true }).fill(password)
    await page.getByRole('button', { name: copy.auth.register.submit }).click()

    await expect(page.getByText(copy.setup.stepLabel(1))).toBeVisible()
    await page.getByLabel(copy.setup.fund.nameLabel).fill(fundName)
    await expect(page.getByText(zones.setupHint)).toBeVisible()
    await chooseZone(page, 'Asia/Makassar')
    await expect(page.getByRole('combobox', { name: zones.label })).toHaveText(zones.options['Asia/Makassar'].label)
    await page.getByRole('button', { name: copy.setup.next }).click()

    await expect(page.getByText(copy.setup.stepLabel(2))).toBeVisible()
    await page.getByRole('button', { name: copy.setup.next }).click()
    await expect(page.getByText(copy.setup.stepLabel(3))).toBeVisible()
    await page.getByRole('button', { name: copy.setup.next }).click()
    await expect(page.getByText(copy.setup.stepLabel(4))).toBeVisible()
    await page.getByRole('button', { name: copy.setup.roster.skip }).click()
    await expect(page.getByRole('heading', { level: 1, name: fundName })).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await expect(page.getByRole('heading', { name: copy.settings.fund.heading })).toBeVisible()
    await expect(page.getByRole('button', { name: copy.settings.fund.editAria(fundName) })).toBeVisible()
    await expect(page.getByRole('button', { name: copy.settings.fund.zone.editAria(zones.options['Asia/Makassar'].label) })).toBeVisible()
  })

  test('is changed from Pengaturan, with no confirm step, and survives a reload', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel(copy.auth.login.emailLabel).fill(email)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(password)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(page.getByRole('heading', { level: 1, name: fundName })).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await page.getByRole('button', { name: copy.settings.fund.zone.editAria(zones.options['Asia/Makassar'].label) }).click()

    await expect(page).toHaveURL(/[?&]edit=fund%3Atime_zone/)
    const dialog = page.getByRole('dialog', { name: copy.settings.fund.zone.editTitle })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText(copy.settings.fund.zone.body)).toBeVisible()

    await expect(dialog.getByRole('button', { name: copy.settings.fund.save })).toBeDisabled()
    await chooseZone(page, 'Asia/Jayapura')
    await dialog.getByRole('button', { name: copy.settings.fund.save }).click()
    await expect(dialog).toBeHidden()

    const witCard = page.getByRole('button', { name: copy.settings.fund.zone.editAria(zones.options['Asia/Jayapura'].label) })
    await expect(witCard).toBeVisible()

    // The server's zone, not only the screen's: a fresh load says it too, and
    // setting the old one back is the same two taps.
    await page.reload()
    await expect(witCard).toBeVisible()
    await witCard.click()
    await chooseZone(page, 'Asia/Makassar')
    await page
      .getByRole('dialog', { name: copy.settings.fund.zone.editTitle })
      .getByRole('button', { name: copy.settings.fund.save })
      .click()
    await expect(page.getByRole('button', { name: copy.settings.fund.zone.editAria(zones.options['Asia/Makassar'].label) })).toBeVisible()
  })
})
