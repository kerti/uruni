import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, logIn, seedEmail, seedPassword, test } from './fixtures'

// The treasurer changes her own login from Pengaturan (#386): the password,
// then the email, each asking for the current password and each signing every
// other device out. vitest covers the dialogs against stubbed fetches
// (User.test.tsx) and Go covers the routes, the rate limit and the session
// rows; this proves what only a real browser against the real server can - the
// new login works through the actual form, the old one stops, and a second
// browser that was signed in is really sent back to Login.
//
// One story over this file's own instance (reset in beforeAll): the second test
// starts from the password the first one left. The seeded login is never needed
// by another file - every spec resets to the seeded template first.
test.describe('user', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  const text = copy.settings.user
  const newPassword = 'kata-sandi-yang-baru'
  const newEmail = 'bendahara.baru@e2e.uruni.test'

  // The login form, typed for real. The password field is matched exactly: the
  // peek button's label also contains "kata sandi".
  async function submitLogin(page: Page, email: string, password: string) {
    await page.getByLabel(copy.auth.login.emailLabel).fill(email)
    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(password)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
  }

  async function logOut(page: Page) {
    await page.getByRole('button', { name: copy.shell.logout, exact: true }).click()
    await expect(page.getByText(copy.auth.login.heading)).toBeVisible()
  }

  async function openSettings(page: Page) {
    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await expect(page.getByRole('heading', { name: copy.settings.heading })).toBeVisible()
    await expect(page.getByRole('heading', { name: text.heading })).toBeVisible()
  }

  test('change the password: the old one stops working, the new one works, and another device is signed out', async ({
    page,
    browser,
    instance,
  }) => {
    // A second browser, signed in before the change.
    const otherContext = await browser.newContext({ baseURL: instance.url, viewport: { width: 390, height: 844 } })
    try {
      const other = await otherContext.newPage()
      await logIn(other)
      await other.goto('/')
      await expect(other.getByText(copy.home.balanceHeading)).toBeVisible()

      await logIn(page)
      await page.goto('/')
      await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
      await openSettings(page)
      await expect(page.getByText(text.body(seedEmail))).toBeVisible()

      await page.getByRole('button', { name: text.changePassword }).click()
      const dialog = page.getByRole('dialog', { name: text.password.title })
      await expect(dialog).toBeVisible()
      await expect(page).toHaveURL(/[?&]edit=user%3Apassword/)

      // A wrong current password is refused in place, with its own sentence, and
      // nothing changes.
      await dialog.getByLabel(text.password.currentLabel, { exact: true }).fill('bukan-kata-sandinya')
      await dialog.getByLabel(text.password.newLabel, { exact: true }).fill(newPassword)
      await dialog.getByLabel(text.password.repeatLabel, { exact: true }).fill(newPassword)
      await dialog.getByRole('button', { name: text.password.save }).click()
      await expect(dialog.getByText(copy.common.errors.wrong_current_password)).toBeVisible()

      // The right one saves: the dialog closes and the page says so.
      await dialog.getByLabel(text.password.currentLabel, { exact: true }).fill(seedPassword)
      await dialog.getByRole('button', { name: text.password.save }).click()
      await expect(dialog).toBeHidden()
      await expect(page.getByText(text.password.success)).toBeVisible()
      // This session stays signed in.
      await expect(page.getByRole('heading', { name: text.heading })).toBeVisible()

      // The other device is gone: its next load lands on Login.
      await other.reload()
      await expect(other.getByText(copy.auth.login.heading)).toBeVisible()
      await expect(other.getByText(copy.home.balanceHeading)).toBeHidden()

      // Signing out and back in: the old password is refused, the new one works.
      await logOut(page)
      await submitLogin(page, seedEmail, seedPassword)
      await expect(page.getByText(copy.auth.login.invalidCredentials)).toBeVisible()
      await submitLogin(page, seedEmail, newPassword)
      await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
    } finally {
      await otherContext.close()
    }
  })

  test('change the email: the new address logs in, the old one does not, and another device is signed out', async ({
    page,
    browser,
    instance,
  }) => {
    const otherContext = await browser.newContext({ baseURL: instance.url, viewport: { width: 390, height: 844 } })
    try {
      // The login the previous test left: the seeded email, the new password.
      const other = await otherContext.newPage()
      await other.goto('/')
      await submitLogin(other, seedEmail, newPassword)
      await expect(other.getByText(copy.home.balanceHeading)).toBeVisible()

      await page.goto('/')
      await submitLogin(page, seedEmail, newPassword)
      await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()
      await openSettings(page)

      await page.getByRole('button', { name: text.changeEmail }).click()
      const dialog = page.getByRole('dialog', { name: text.email.title })
      await expect(dialog).toBeVisible()
      await dialog.getByLabel(text.email.newLabel, { exact: true }).fill(newEmail)
      await dialog.getByLabel(text.email.currentLabel, { exact: true }).fill(newPassword)
      await dialog.getByRole('button', { name: text.email.save }).click()

      await expect(dialog).toBeHidden()
      await expect(page.getByText(text.email.success)).toBeVisible()
      // The card now names the new address, and she is still signed in.
      await expect(page.getByText(text.body(newEmail))).toBeVisible()

      await other.reload()
      await expect(other.getByText(copy.auth.login.heading)).toBeVisible()

      await logOut(page)
      await submitLogin(page, seedEmail, newPassword)
      await expect(page.getByText(copy.auth.login.invalidCredentials)).toBeVisible()
      await submitLogin(page, newEmail, newPassword)
      await expect(page.getByText(copy.home.balanceHeading)).toBeVisible()

      // And a reload still shows the new address: it is read from the server.
      await openSettings(page)
      await expect(page.getByText(text.body(newEmail))).toBeVisible()
    } finally {
      await otherContext.close()
    }
  })
})
