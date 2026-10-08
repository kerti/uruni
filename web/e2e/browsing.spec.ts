import type { APIRequestContext, Page } from '@playwright/test'

import { copy } from '../src/copy/id'
import { expect, logIn, test } from './fixtures'

// M8.7 (#438): finding things again. Riwayat's four tabs (search, the
// Transaksi filter dialog and its chips, paging), the roster (search, paging,
// the arrears badge, edit, retire, reinstate) and the locations in Pengaturan
// (edit, retire, reinstate).
//
// Every list pages at 25 rows (the server's *PageSize constants), so the
// rows are seeded over the API rather than typed in - this spec is about
// browsing them, and the forms that write them have journeys of their own.
// Serial, one story on the seeded fixture: the edits come last, after
// everything that searches by the seeded names.

const PAGE = 25
const envelope = 'Amplop Uji Laporan'

type Named = { id: number; name: string }

// Today in Asia/Jakarta, the one clock the app reads (internal/tz).
function jakartaToday(): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Jakarta' }).format(new Date())
}

async function api<T>(request: APIRequestContext, method: 'GET' | 'POST', path: string, data?: unknown): Promise<T> {
  const response = method === 'GET' ? await request.get(path) : await request.post(path, { data })
  expect(response.ok(), `${method} ${path} answered ${response.status()}: ${await response.text()}`).toBe(true)
  return (await response.json()) as T
}

async function byName(request: APIRequestContext, path: string, key: string, name: string): Promise<number> {
  // Some list routes answer a bare array, the paged ones an envelope.
  const body = await api<Named[] | Record<string, Named[]>>(request, 'GET', path)
  const rows = Array.isArray(body) ? body : body[key]
  const row = rows.find((r) => r.name === name)
  if (!row) throw new Error(`${name} not found in GET ${path}`)
  return row.id
}

// Scoped to main: the footer's five tabs are list items too.
function rows(page: Page) {
  return page.getByRole('main').getByRole('listitem')
}
// A Transaksi row by its note, which is an element of its own in the row.
function noteRow(page: Page, note: string) {
  return rows(page).filter({ has: page.getByText(note, { exact: true }) })
}

async function openHistoryTab(page: Page, tab: string) {
  await page.getByRole('link', { name: copy.shell.nav.history }).click()
  await page.getByRole('link', { name: tab }).click()
}

test.describe('browsing Riwayat, the roster and the locations', () => {
  test.describe.configure({ mode: 'serial' })
  test.beforeAll(({ instance }) => instance.reset())

  test('seed more than a page of every list', async ({ page }) => {
    await logIn(page)
    const request = page.context().request
    const today = jakartaToday()
    const tunai = await byName(request, '/api/accounts', 'accounts', 'Tunai')
    const main = await byName(request, '/api/purposes', 'purposes', 'Kas Utama')
    const amplop = await byName(request, '/api/purposes', 'purposes', envelope)
    const wargaSatu = await byName(request, '/api/members', 'members', 'Warga Satu')
    const wargaDua = await byName(request, '/api/members', 'members', 'Warga Dua')

    for (let i = 1; i <= PAGE + 5; i++) {
      await api(request, 'POST', '/api/transactions', {
        account_id: tunai,
        purpose_id: main,
        direction: 'out',
        amount: 1_000 + i,
        occurred_on: today,
        note: `Belanja ${i}`,
      })
    }
    await api(request, 'POST', '/api/transactions', {
      account_id: tunai,
      purpose_id: amplop,
      direction: 'in',
      amount: 77_000,
      occurred_on: today,
      note: 'Sumbangan halal bihalal',
      member_id: wargaDua,
    })

    // Dues: one payment a month for Warga Satu from 2024-01, and one for
    // Warga Dua, at the fixture's Rp 50.000 rate.
    for (let i = 0; i <= PAGE; i++) {
      const period = `${2024 + Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, '0')}`
      await api(request, 'POST', '/api/dues-payments', {
        account_id: tunai,
        member_id: wargaSatu,
        occurred_on: today,
        note: null,
        periods: [{ dues_period: period, amount: 50_000 }],
      })
    }
    await api(request, 'POST', '/api/dues-payments', {
      account_id: tunai,
      member_id: wargaDua,
      occurred_on: today,
      note: null,
      periods: [{ dues_period: '2024-01', amount: 50_000 }],
    })

    for (let i = 1; i <= PAGE + 1; i++) {
      await api(request, 'POST', '/api/reimbursements', {
        member_id: wargaSatu,
        purpose_id: main,
        amount: 2_000 + i,
        incurred_on: today,
        note: `Talangan ${i}`,
      })
    }
    await api(request, 'POST', '/api/reimbursements', {
      member_id: wargaDua,
      purpose_id: main,
      amount: 9_000,
      incurred_on: today,
      note: 'Beli galon',
    })

    // Cek kas: nothing moves from here on, so every count matches the
    // balances as they now stand.
    const balances = await api<{ accounts: { id: number; balance: number }[] }>(request, 'GET', '/api/balances')
    const counts = balances.accounts.map((a) => ({ account_id: a.id, actual_amount: a.balance, resolution: 'matched' }))
    for (let i = 0; i <= PAGE; i++) await api(request, 'POST', '/api/reconciliations', { note: `Hitung ${i}`, counts })

    for (let i = 1; i <= PAGE; i++) await api(request, 'POST', '/api/members', { name: `Anggota ${String(i).padStart(2, '0')}` })
  })

  test('Riwayat > Transaksi: search, the filter dialog and its chips, paging', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await openHistoryTab(page, copy.history.tabs.transactions)

    // Paging: the oldest row (Belanja 1, behind every dues payment) is not
    // on the first page, and "Muat lebih banyak" reaches it.
    const loadMore = page.getByRole('button', { name: copy.history.transactions.loadMore })
    await expect(loadMore).toBeVisible()
    await expect(rows(page)).toHaveCount(PAGE)
    await expect(noteRow(page, 'Belanja 1')).toHaveCount(0)
    // Several pages: tap again until it arrives. The button is briefly gone
    // while a page loads, so each tap is retried rather than counted.
    await expect(async () => {
      if ((await noteRow(page, 'Belanja 1').count()) === 0) await loadMore.click({ timeout: 1_000 })
      await expect(noteRow(page, 'Belanja 1')).toBeVisible({ timeout: 1_000 })
    }).toPass()
    await expect(loadMore).toBeHidden()

    // Search narrows to the one note, and says so when nothing matches.
    const search = page.getByRole('searchbox', { name: copy.history.transactions.searchLabel })
    await search.fill('Belanja 7')
    await expect(noteRow(page, 'Belanja 7')).toBeVisible()
    await expect(rows(page)).toHaveCount(1)
    await search.fill('tidak ada yang begini')
    await expect(page.getByText(copy.history.transactions.noResults('tidak ada yang begini'))).toBeVisible()
    await search.fill('')

    // The filter dialog: Pos, Anggota and Jenis together find the one
    // contribution, and each shows as a chip.
    await page.getByRole('button', { name: copy.history.transactions.filterButton, exact: true }).click()
    const dialog = page.getByRole('dialog', { name: copy.history.transactions.filterHeading })
    for (const [label, option] of [
      [copy.history.transactions.filterPurpose, envelope],
      [copy.history.transactions.filterMember, 'Warga Dua'],
      [copy.record.directionLabel, copy.record.directionIn],
    ]) {
      await dialog.getByRole('combobox', { name: label }).click()
      await page.getByRole('listbox').getByRole('option', { name: option }).click()
    }
    await dialog.getByRole('button', { name: copy.history.transactions.filterApply }).click()

    await expect(noteRow(page, 'Sumbangan halal bihalal')).toBeVisible()
    await expect(rows(page)).toHaveCount(1)
    await expect(page.getByRole('button', { name: copy.history.transactions.filterButtonActive(3) })).toBeVisible()
    const purposeChip = copy.history.transactions.purposeFilterLabel(envelope)
    await expect(page.getByText(purposeChip)).toBeVisible()
    await expect(page.getByText(copy.history.transactions.memberFilterLabel('Warga Dua'))).toBeVisible()

    // A chip clears its own filter only.
    await page.getByRole('button', { name: copy.history.transactions.filterClear(purposeChip) }).click()
    await expect(page.getByText(purposeChip)).toBeHidden()
    await expect(page.getByRole('button', { name: copy.history.transactions.filterButtonActive(2) })).toBeVisible()

    // "Hapus semua filter" clears the rest.
    await page.getByRole('button', { name: copy.history.transactions.filterButtonActive(2) }).click()
    await dialog.getByRole('button', { name: copy.history.transactions.filterReset }).click()
    await expect(page.getByRole('button', { name: copy.history.transactions.filterButton, exact: true })).toBeVisible()
    await expect(rows(page)).toHaveCount(PAGE)

    // Bulan is a query parameter like the others: a deep link applies it.
    await page.goto(`/history/transactions?month=${jakartaToday().slice(0, 7)}`)
    await expect(page.getByText(/^Bulan: /)).toBeVisible()
    await expect(rows(page)).toHaveCount(PAGE)
  })

  test('Riwayat > Iuran, Talangan and Cek kas: search and paging', async ({ page }) => {
    await logIn(page)
    await page.goto('/')

    // Iuran: 26 payments from Warga Satu and one from Warga Dua.
    await openHistoryTab(page, copy.history.tabs.dues)
    const duesMore = page.getByRole('button', { name: copy.history.dues.loadMore })
    await expect(duesMore).toBeVisible()
    await duesMore.click()
    await expect(duesMore).toBeHidden()
    await page.getByRole('searchbox', { name: copy.history.dues.searchLabel }).fill('Warga Dua')
    await expect(page.getByText('Warga Dua').first()).toBeVisible()
    await expect(page.getByText('Warga Satu')).toHaveCount(0)

    // Talangan: 26 outstanding claims from Warga Satu and one from Warga Dua.
    await openHistoryTab(page, copy.history.tabs.reimbursements)
    const claimsMore = page.getByRole('button', { name: copy.history.reimbursements.loadMore })
    await expect(claimsMore).toBeVisible()
    await claimsMore.click()
    await expect(claimsMore).toBeHidden()
    await page.getByRole('searchbox', { name: copy.history.reimbursements.searchLabel }).fill('galon')
    await expect(page.getByText('Beli galon')).toBeVisible()
    await expect(page.getByText(/^Talangan \d+$/)).toHaveCount(0)

    // Cek kas: 26 counts, no search.
    await openHistoryTab(page, copy.reconciliation.heading)
    const countsMore = page.getByRole('button', { name: copy.history.reconciliations.loadMore })
    await expect(countsMore).toBeVisible()
    await countsMore.click()
    await expect(countsMore).toBeHidden()
  })

  test('the roster: search, paging, arrears, edit, retire and reinstate', async ({ page }) => {
    await logIn(page)
    await page.goto('/')
    await page.getByRole('link', { name: copy.shell.nav.members }).click()
    await expect(page.getByRole('heading', { name: copy.members.heading, exact: true })).toBeVisible()

    const text = copy.members.roster
    const loadMore = page.getByRole('button', { name: text.loadMore })
    await expect(loadMore).toBeVisible()
    await loadMore.click()
    await expect(loadMore).toBeHidden()

    const search = page.getByRole('searchbox', { name: text.searchLabel })
    await search.fill('tidak ada yang begini')
    await expect(page.getByText(text.noResults('tidak ada yang begini'))).toBeVisible()

    // Warga Dua paid one month since 2024-01, so the card carries arrears.
    // The search stays on for the edits below: unfiltered, Warga Dua sorts
    // after the 25 seeded members, onto the second page.
    await search.fill('Warga Dua')
    const card = page.getByRole('button', { name: text.editAria('Warga Dua') })
    await expect(card).toBeVisible()
    await expect(page.getByRole('button', { name: text.editAria('Warga Satu') })).toHaveCount(0)
    await expect(card).toContainText(/Tunggakan \d+ bulan/)

    // Edit: rename.
    await page.getByRole('button', { name: text.editAria('Warga Dua') }).click()
    let dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByLabel(text.nameLabel).fill('Warga Dua Baru')
    await dialog.getByRole('button', { name: text.save }).click()
    await expect(dialog).toBeHidden()
    const renamed = page.getByRole('button', { name: text.editAria('Warga Dua Baru') })
    await expect(renamed).toBeVisible()

    // Retire, confirmed in place: the card stays, marked inactive.
    await renamed.click()
    dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByRole('button', { name: text.deactivate }).click()
    await dialog.getByRole('button', { name: text.deactivateConfirmAction }).click()
    await expect(dialog).toBeHidden()
    await expect(renamed).toContainText(text.inactiveBadge)

    // Reinstate.
    await renamed.click()
    dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByRole('button', { name: text.reinstate }).click()
    await expect(dialog).toBeHidden()
    await expect(renamed).not.toContainText(text.inactiveBadge)
  })

  test('locations: edit, retire and reinstate', async ({ page }) => {
    await logIn(page)
    await page.goto('/settings')
    const text = copy.settings.locations

    await page.getByRole('button', { name: text.editAria('Bank Uji Coba') }).click()
    let dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByLabel(text.nameLabel).fill('Bank Uji Baru')
    await dialog.getByRole('button', { name: text.save }).click()
    await expect(dialog).toBeHidden()
    const row = page.getByRole('button', { name: text.editAria('Bank Uji Baru') })
    await expect(row).toBeVisible()

    await row.click()
    dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByRole('button', { name: text.deactivate }).click()
    await dialog.getByRole('button', { name: text.deactivateConfirmAction }).click()
    await expect(dialog).toBeHidden()
    await expect(row).toContainText(text.inactiveBadge)

    // Retired, it is not offered where money is recorded.
    await page.getByRole('link', { name: copy.shell.nav.record }).click()
    await page.getByRole('combobox', { name: copy.record.locationLabel }).click()
    await expect(page.getByRole('listbox').getByRole('option', { name: 'Bank Uji Baru' })).toHaveCount(0)
    await page.keyboard.press('Escape')

    await page.goto('/settings')
    await row.click()
    dialog = page.getByRole('dialog', { name: text.editTitle })
    await dialog.getByRole('button', { name: text.reinstate }).click()
    await expect(dialog).toBeHidden()
    await expect(row).not.toContainText(text.inactiveBadge)
  })
})
