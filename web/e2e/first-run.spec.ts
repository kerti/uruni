import type { Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { formatIDR } from '../src/lib/money'
import { expect, test } from './fixtures'

// M8.5 (#436): the treasurer's first hour, on an instance with nothing in it -
// register, the four-step setup wizard, a first record - then the access
// paths around it: a reload keeps her signed in, the fund can be renamed, a
// wrong password gets its own warm message, and repeated failures get the
// rate-limit one. Every other spec starts from the seeded fixture, which
// already has an account and a fund, so Register and Setup are reachable
// only here (`instance.reset({ seed: false })`).
//
// Serial, one story: each test continues from the state the one before left.
// The rate-limit test is last on purpose - the limiter blocks this browser's
// IP as well as the email, so nothing can log in after it until the next
// file's reset restarts the server.

const email = 'bendahara-baru@e2e.uruni.test'
const password = 'kata-sandi-pertama'
const fundName = 'Kas RT Pertama'
const renamedFund = 'Kas RT Pertama Sekali'
const opening = 500_000
const spent = 10_000

function hero(page: Page) {
  return page.getByRole('region', { name: copy.home.balanceHeading })
}

async function submitLogin(page: Page, withPassword: string) {
  await page.getByLabel(copy.auth.login.emailLabel).fill(email)
  await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(withPassword)
  await page.getByRole('button', { name: copy.auth.login.submit }).click()
}

test.describe('first run and access', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset({ seed: false }))

  test('register, set up the fund in four steps, and record a first transaction', async ({ page }) => {
    await page.goto('/')

    // No account yet, so the app opens on Register, not Login.
    await expect(page.getByText(copy.auth.register.heading)).toBeVisible()
    await page.getByLabel(copy.auth.register.emailLabel).fill(email)
    await page.getByLabel(copy.auth.register.passwordLabel, { exact: true }).fill(password)
    await page.getByRole('button', { name: copy.auth.register.submit }).click()

    // Step 1: the fund's name.
    await expect(page.getByText(copy.setup.stepLabel(1))).toBeVisible()
    await page.getByLabel(copy.setup.fund.nameLabel).fill(fundName)
    await page.getByRole('button', { name: copy.setup.next }).click()

    // Step 2: locations. The wizard offers a Tunai and a Bank row; both stay.
    await expect(page.getByText(copy.setup.stepLabel(2))).toBeVisible()
    await expect(page.getByLabel(copy.setup.locations.nameLabel)).toHaveCount(2)
    await page.getByRole('button', { name: copy.setup.next }).click()

    // Step 3: opening balances - Tunai has money, the bank is left blank,
    // which posts nothing for it.
    await expect(page.getByText(copy.setup.stepLabel(3))).toBeVisible()
    await page.getByLabel(copy.setup.balances.amountLabel('Tunai')).fill(String(opening))
    await page.getByRole('button', { name: copy.setup.next }).click()

    // Step 4: a dues tier with its rate, and one member.
    await expect(page.getByText(copy.setup.stepLabel(4))).toBeVisible()
    await page.getByLabel(copy.setup.roster.tierNameLabel).fill('Reguler')
    await page.getByLabel(copy.setup.roster.rateAmountLabel).fill('25000')
    await page.getByRole('button', { name: copy.setup.roster.addMember }).click()
    await page.getByLabel(copy.setup.roster.memberNameLabel).fill('Warga Pertama')
    await page.getByRole('button', { name: copy.setup.roster.finish }).click()

    // Beranda: the fund's name in the header and the opening balance as the
    // whole fund. The opening entry is a ledger row like any other.
    await expect(page.getByRole('heading', { level: 1, name: fundName })).toBeVisible()
    await expect(hero(page)).toContainText(formatIDR(opening))

    // The roster step created the member, on the tier.
    await page.getByRole('link', { name: copy.shell.nav.members }).click()
    await expect(page.getByText('Warga Pertama')).toBeVisible()

    // A first record, through Catat, moves the balance.
    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await page.getByRole('button', { name: copy.record.directionOut }).click()
    await page.getByLabel(copy.record.amountLabel).fill(String(spent))
    await page.getByRole('button', { name: copy.record.submit }).click()
    await expect(page.getByText(copy.record.successOut)).toBeVisible()
    await expect(hero(page)).toContainText(formatIDR(opening - spent))
  })

  test('a reload keeps her signed in', async ({ page }) => {
    await page.goto('/')
    await submitLogin(page, password)
    await expect(hero(page)).toContainText(formatIDR(opening - spent))

    await page.reload()
    await expect(hero(page)).toContainText(formatIDR(opening - spent))
    await expect(page.getByText(copy.auth.login.heading)).toBeHidden()
  })

  test('rename the fund from Pengaturan, and the header follows', async ({ page }) => {
    await page.goto('/')
    await submitLogin(page, password)
    await expect(page.getByRole('heading', { level: 1, name: fundName })).toBeVisible()

    await page.getByRole('link', { name: copy.shell.nav.settings }).click()
    await page.getByRole('button', { name: copy.settings.fund.editAria(fundName) }).click()
    await page.getByRole('textbox', { name: copy.settings.fund.nameLabel }).fill(renamedFund)
    await page.getByRole('button', { name: copy.settings.fund.save }).click()

    await expect(page.getByRole('heading', { level: 1, name: renamedFund })).toBeVisible()
    // And it is the server's name, not just the screen's: a fresh load says it too.
    await page.reload()
    await expect(page.getByRole('heading', { level: 1, name: renamedFund })).toBeVisible()
  })

  test('a wrong password gets its own message, and the right one still works', async ({ page }) => {
    await page.goto('/')
    await submitLogin(page, 'bukan-kata-sandinya')
    await expect(page.getByText(copy.auth.login.invalidCredentials)).toBeVisible()

    await page.getByLabel(copy.auth.login.passwordLabel, { exact: true }).fill(password)
    await page.getByRole('button', { name: copy.auth.login.submit }).click()
    await expect(hero(page)).toBeVisible()
  })

  test('repeated failures get the rate-limit message', async ({ page }) => {
    // The limiter's threshold is the server's (internal/http/rate_limiter.go),
    // so this fails over the API until it answers 429 rather than restating
    // the number; then one more attempt through the form shows the message.
    let status = 0
    for (let attempt = 0; attempt < 20 && status !== 429; attempt++) {
      const response = await page.context().request.post('/api/login', { data: { email, password: 'salah' } })
      status = response.status()
    }
    expect(status).toBe(429)

    await page.goto('/')
    await submitLogin(page, password)
    await expect(page.getByText(copy.auth.login.tooManyRequests)).toBeVisible()
    await expect(hero(page)).toBeHidden()
  })
})
