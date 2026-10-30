import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/components/toast/ToastProvider'
import Reimbursements from '@/screens/History/Reimbursements'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { Purpose } from '@/lib/purposes'
import type { ReceiptUploadResult } from '@/lib/receipts'
import type { Reimbursement } from '@/lib/reimbursements'
import type { Account, Member } from '@/lib/setup'

const text = copy.reimbursements
const searchText = copy.history.reimbursements

afterEach(() => {
  vi.unstubAllGlobals()
})

const members = [
  {
    id: 1,
    name: 'Jane',
    tier_id: 1,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
  {
    id: 2,
    name: 'John',
    tier_id: 2,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
] satisfies Member[]

const purposes = [
  { id: 10, kind: 'pass_through', name: 'Seragam', created_at: 1 },
  { id: 11, kind: 'main', name: 'Kas Utama', created_at: 1 },
] satisfies Purpose[]

const accounts = [{ id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 }] satisfies Account[]

function claim(id: number, overrides: Partial<Reimbursement> = {}): Reimbursement {
  return {
    id,
    member_id: id === 2 ? 2 : 1,
    purpose_id: 11,
    amount: id === 2 ? 25_000 : 15_000,
    incurred_on: '2026-09-01',
    waived_on: null,
    settled: false,
    note: id === 1 ? 'Parkir' : null,
    created_at: id,
    ...overrides,
  }
}

/** Wraps rows in GET /api/reimbursements's envelope. */
function page(rows: Reimbursement[], nextCursor: string | null = null) {
  return { reimbursements: rows, next_cursor: nextCursor }
}

const outstandingClaims: Reimbursement[] = [claim(1)]

const allClaims: Reimbursement[] = [claim(1), claim(2, { settled: true })]

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

type Handler = { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }

/** Opens the first claim row's "more" menu and picks an item from it:
 * Ubah, Putihkan and Hapus live there, behind the row's one plain Bayar
 * control. */
async function chooseFromRowMenu(name: string) {
  await userEvent.click(screen.getAllByRole('button', { name: text.actions.menuAria })[0])
  await userEvent.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name }))
}

/** Routes a stubbed fetch by method + path substring, recording every call */
function routedFetch(handlers: Handler[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

/** The default GET handlers backing every test: the reimbursements list
 * (outstanding vs all variants) plus the members/purposes/accounts form data
 * fetched when a record/settle/correct form is opened. */
function getHandlers(opts: { outstanding?: Reimbursement[]; all?: Reimbursement[] } = {}) {
  const outstanding = opts.outstanding ?? outstandingClaims
  const all = opts.all ?? allClaims
  return [
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
      handle: () => Promise.resolve(jsonResponse(page(outstanding))),
    },
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('outstanding'),
      handle: () => Promise.resolve(jsonResponse(page(all))),
    },
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
      handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
    },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
  ]
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.search}</output>
}

/** Stands in for the three Talangan screens: this file only needs to know
 * where a tap on the tab landed. */
function ScreenProbe() {
  const location = useLocation()
  return <output data-testid="screen">{location.pathname + location.search}</output>
}

function renderAt(entry: string | { pathname: string; state: unknown } = '/history/reimbursements') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <ToastProvider>
        <Routes>
          <Route
            path="/history/reimbursements"
            element={
              <>
                <Reimbursements />
                <LocationProbe />
              </>
            }
          />
          <Route path="/record" element={<ScreenProbe />} />
          <Route path="/reimbursements/correct" element={<ScreenProbe />} />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('Reimbursements tab', () => {
  it('shows outstanding claims by default', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt()

    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByRole('checkbox', { name: text.outstandingFilterLabel })).toBeChecked()
    expect(screen.getByText('Parkir')).toBeInTheDocument()
    expect(screen.getByText(formatIDR(15_000))).toBeInTheDocument()
    // The outstanding view's own filter already says "belum dibayar", so its
    // cards do not repeat it (the all view does, below).
    expect(screen.queryByText(text.status.outstanding, { selector: 'span' })).not.toBeInTheDocument()
  })

  it('puts the photo control under the amount and Bayar with the menu at the bottom right', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    const card = screen.getByText('Jane').closest('li')!
    const amount = within(card).getByText(formatIDR(15_000))
    const photo = within(card).getByRole('button', { name: copy.receipts.rowControlAria(false) })
    expect(amount.parentElement).toContainElement(photo)
    // The bottom line holds Bayar and the menu, not the photo control.
    const pay = within(card).getByRole('button', { name: text.actions.settle })
    expect(pay.parentElement).not.toContainElement(photo)
    expect(pay.parentElement).toContainElement(within(card).getByRole('button', { name: text.actions.menuAria }))
  })

  it('opens Catat with the claim ticked, and the correct screen instead of expanding a form', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    const first = renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.record.heading }))
    expect(screen.getByTestId('screen')).toHaveTextContent('/record?fronted=1')
    first.unmount()

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    await chooseFromRowMenu(text.actions.correct)
    expect(screen.getByTestId('screen')).toHaveTextContent('/reimbursements/correct?id=1')
  })

  it('shows the confirmation the correct screen handed back', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt({ pathname: '/history/reimbursements', state: { reimbursementDone: 'corrected' } })
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByText(text.correct.success)).toBeInTheDocument()
  })

  it('a tap puts the confirmation a screen handed back away', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt({ pathname: '/history/reimbursements', state: { reimbursementDone: 'corrected' } })
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByText(text.correct.success)).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.correct.success }))
    expect(screen.queryByText(text.correct.success)).not.toBeInTheDocument()
  })

  it('waive is reversible: un-waive is reachable from the full list', async () => {
    // The outstanding list never contains a waived claim, so after a waive the
    // row leaves it; the full list shows the waived row with "Batalkan
    // pemutihan", which un-waives it straight back. The outstanding GET can
    // never present a row with that affordance.
    let waived = false
    const initial = [claim(1)]
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
          handle: () => Promise.resolve(jsonResponse(page(waived ? [] : initial))),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('outstanding'),
          handle: () => Promise.resolve(jsonResponse(page(waived ? [claim(1, { waived_on: '2026-09-02' })] : initial))),
        },
        {
          match: (m: string, u: string) => m === 'PATCH' && u.includes('/api/reimbursements/1'),
          handle: () => {
            waived = !waived
            return Promise.resolve(jsonResponse(waived ? claim(1, { waived_on: '2026-09-02' }) : claim(1)))
          },
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
          handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
          handle: () => Promise.resolve(jsonResponse(purposes)),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
          handle: () => Promise.resolve(jsonResponse(accounts)),
        },
      ]),
    )

    renderAt()
    await waitFor(() => expect(screen.getAllByRole('button', { name: text.actions.menuAria })[0]).toBeInTheDocument())

    // Waive: the row leaves the outstanding list and a feedback says so.
    await chooseFromRowMenu(text.actions.waive)
    await waitFor(() => expect(screen.getByText(text.waive.success)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: text.actions.unwaive })).not.toBeInTheDocument()

    // The full list shows the waived row, where the un-waive affordance lives.
    await userEvent.click(screen.getByRole('checkbox', { name: text.outstandingFilterLabel }))
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.unwaive })).toBeInTheDocument())
    expect(screen.getByText(text.status.waived, { selector: 'span' })).toBeInTheDocument()

    // Un-waive: the claim is owed again, badge flips back.
    await userEvent.click(screen.getByRole('button', { name: text.actions.unwaive }))
    await waitFor(() => expect(screen.getByText(text.status.outstanding, { selector: 'span' })).toBeInTheDocument())
    expect(screen.getByText(text.unwaive.success)).toBeInTheDocument()
  })

  it('the full list is honest history: Dibayar only when the ledger settled it', async () => {
    // The full list mixes a settled row (id 2) and a still-owed row (id 1). The
    // badges must reflect the flag: never "Dibayar" on an unsettled claim, and
    // no action buttons at all.
    vi.stubGlobal('fetch', routedFetch(getHandlers({ outstanding: [], all: allClaims })))
    renderAt()

    await waitFor(() => expect(screen.getByRole('checkbox', { name: text.outstandingFilterLabel })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('checkbox', { name: text.outstandingFilterLabel }))

    await waitFor(() => expect(screen.getByText(formatIDR(25_000))).toBeInTheDocument())
    expect(screen.getByText(text.status.settled, { selector: 'span' })).toBeInTheDocument()
    expect(screen.getByText(text.status.outstanding, { selector: 'span' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.settle })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.menuAria })).not.toBeInTheDocument()
  })

  it('delete calls DELETE and re-fetches', async () => {
    // The outstanding list shows Jane (id 1); deleting her empties it.
    let deleted = false
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
          handle: () => Promise.resolve(jsonResponse(page(deleted ? [] : [claim(1)]))),
        },
        {
          match: (m: string, u: string) => m === 'DELETE' && u.includes('/api/reimbursements/1'),
          handle: () => {
            deleted = true
            return Promise.resolve(new Response(null, { status: 204 }))
          },
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
          handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
          handle: () => Promise.resolve(jsonResponse(purposes)),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
          handle: () => Promise.resolve(jsonResponse(accounts)),
        },
      ]),
    )

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Hapus opens its dialog, addressed by the URL; the confirm is in it.
    await chooseFromRowMenu(text.actions.delete)
    const dialog = await screen.findByRole('dialog', { name: text.delete.dialogTitle })
    expect(screen.getByTestId('location')).toHaveTextContent('?edit=delete-claim%3A1')
    expect(within(dialog).getByText(text.delete.body)).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: text.delete.confirm }))

    // After deletion the list refreshes (the claim disappears) and feedback
    // confirms the write landed.
    await waitFor(() => expect(screen.queryByText('Jane')).not.toBeInTheDocument())
    expect(screen.getByText(text.delete.success)).toBeInTheDocument()
  })

  it('Hapus closes on Tutup and on a tap outside, deleting nothing: it posts nothing', async () => {
    const fetchMock = routedFetch(getHandlers())
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    await chooseFromRowMenu(text.actions.delete)
    const dialog = await screen.findByRole('dialog', { name: text.delete.dialogTitle })
    // The claim it deletes, named the way Bayar names it (ADR-046).
    const summary = within(dialog).getAllByRole('definition')[0].closest('dl')!
    expect(summary).toHaveTextContent(`${text.record.memberLabel}Jane`)
    expect(summary).toHaveTextContent(`${text.record.amountLabel}Rp15.000`)
    // The footer's Tutup comes before the X in the DOM.
    await userEvent.click(within(dialog).getAllByRole('button', { name: copy.common.close })[0])
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    await chooseFromRowMenu(text.actions.delete)
    await screen.findByRole('dialog', { name: text.delete.dialogTitle })
    await new Promise((resolve) => setTimeout(resolve, 10))
    const overlay = document.querySelector('[data-slot="dialog-overlay"]')!
    fireEvent.pointerDown(overlay)
    fireEvent.pointerUp(overlay)
    fireEvent.click(overlay)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')).toBe(false)
  })

  it('strips a delete-claim param naming a claim this list does not hold', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt('/history/reimbursements?edit=delete-claim:99')

    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('edit='))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  describe('Bayar dialog (ADR-046)', () => {
    const oneClaim = (settled = false): Handler => ({
      match: (m, u) => m === 'GET' && /\/api\/reimbursements\/1$/.test(u),
      handle: () => Promise.resolve(jsonResponse(claim(1, { settled }))),
    })

    it('opens from Bayar at ?edit=settle:<id>, shows the claim, pays from the remembered location and toasts', async () => {
      let settled = false
      const fetchMock = routedFetch([
        oneClaim(),
        {
          match: (m, u) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
          handle: () => {
            settled = true
            return Promise.resolve(jsonResponse({ id: 50 }, 201))
          },
        },
        {
          match: (m, u) => m === 'GET' && u.includes('/api/reimbursements') && !/\/api\/reimbursements\/1$/.test(u),
          handle: () => Promise.resolve(jsonResponse(page(settled ? [] : [claim(1)]))),
        },
        ...getHandlers().filter(
          (h) => h.match('GET', '/api/members') || h.match('GET', '/api/purposes') || h.match('GET', '/api/accounts'),
        ),
      ])
      vi.stubGlobal('fetch', fetchMock)
      renderAt()
      await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

      await userEvent.click(screen.getByRole('button', { name: text.actions.settle }))

      const dialog = await screen.findByRole('dialog', { name: text.settle.heading })
      expect(screen.getByTestId('location')).toHaveTextContent('?edit=settle%3A1')
      const summary = within(dialog).getAllByRole('definition')[0].closest('dl')!
      expect(summary).toHaveTextContent(`${text.record.memberLabel}Jane`)
      expect(summary).toHaveTextContent(`${text.record.amountLabel}Rp15.000`)
      expect(within(dialog).getByRole('combobox', { name: text.settle.accountLabel })).toHaveTextContent('Tunai')
      // An edit dialog's way out is the X.
      expect(within(dialog).queryByRole('button', { name: copy.common.back })).not.toBeInTheDocument()

      await userEvent.click(within(dialog).getByRole('button', { name: text.settle.submit }))

      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      expect(screen.getByText(text.settle.success)).toBeInTheDocument()
      expect(await screen.findByText(text.emptyOutstanding)).toBeInTheDocument()
      const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')
      expect(JSON.parse(String(post?.[1]?.body))).toMatchObject({ account_id: 1 })
      expect(window.localStorage.getItem('uruni:record:last-account-id')).toBe('1')
    })

    it('opens straight from a deep link, and strips the param for a claim that is already settled', async () => {
      vi.stubGlobal('fetch', routedFetch([oneClaim(true), ...getHandlers()]))
      renderAt('/history/reimbursements?edit=settle:1')

      await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('edit='))
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
  })

  it('reads ?q= from the URL into the field and sends it to the server', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('q=Budi'),
          handle: () => Promise.resolve(jsonResponse(page([claim(1, { note: 'Budi bayar parkir' })]))),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
          handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
          handle: () => Promise.resolve(jsonResponse(purposes)),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
          handle: () => Promise.resolve(jsonResponse(accounts)),
        },
      ]),
    )
    renderAt('/history/reimbursements?q=Budi')

    expect(await screen.findByText('Budi bayar parkir')).toBeInTheDocument()
    expect(screen.getByLabelText(searchText.searchLabel)).toHaveValue('Budi')
  })

  it('?show=all opens on the all view, and the toggle writes it while keeping ?q=', async () => {
    const requests: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/members') return jsonResponse({ members, next_cursor: null })
        if (url.pathname === '/api/purposes') return jsonResponse(purposes)
        if (url.pathname === '/api/accounts') return jsonResponse(accounts)
        if (url.pathname === '/api/reimbursements') {
          requests.push(url)
          return jsonResponse(page([claim(1, { note: 'Parkir' })]))
        }
        return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
      }),
    )
    const user = userEvent.setup()
    renderAt('/history/reimbursements?q=parkir&show=all')

    expect(await screen.findByText('Parkir')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: text.outstandingFilterLabel })).not.toBeChecked()
    expect(requests.at(-1)?.searchParams.has('outstanding')).toBe(false)

    await user.click(screen.getByRole('checkbox', { name: text.outstandingFilterLabel }))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('?q=parkir'))
    await waitFor(() => expect(requests.at(-1)?.searchParams.get('outstanding')).toBe('true'))
    expect(requests.at(-1)?.searchParams.get('q')).toBe('parkir')

    await user.click(screen.getByRole('checkbox', { name: text.outstandingFilterLabel }))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('?q=parkir&show=all'))
  })

  it('the checkbox follows the click at once, before the URL change commits', async () => {
    // The router applies a location change inside startTransition, so a
    // checkbox driven by the URL alone snaps back for a render after the click
    // (Playwright's uncheck() fails on exactly that). A raw click - outside
    // act(), which would flush the transition - sees the box as the browser
    // does, the moment the event ends.
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    const box = screen.getByRole<HTMLInputElement>('checkbox', { name: text.outstandingFilterLabel })

    box.click()
    expect(box.checked).toBe(false)
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('?show=all'))
    expect(box.checked).toBe(false)
  })

  it('writes a typed search to the URL once, after the pause, and refetches with it', async () => {
    const requests: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/members') return jsonResponse({ members, next_cursor: null })
        if (url.pathname === '/api/purposes') return jsonResponse(purposes)
        if (url.pathname === '/api/accounts') return jsonResponse(accounts)
        if (url.pathname === '/api/reimbursements') {
          requests.push(url)
          return jsonResponse(
            url.searchParams.get('q') === 'parkir' ? page([claim(1, { note: 'Parkir' })]) : page([claim(2, { note: 'Beli kabel' })]),
          )
        }
        return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
      }),
    )
    const user = userEvent.setup()
    renderAt()

    expect(await screen.findByText('Beli kabel')).toBeInTheDocument()
    await user.type(screen.getByLabelText(searchText.searchLabel), 'parkir')

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('?q=parkir'))
    expect(await screen.findByText('Parkir')).toBeInTheDocument()
    // Debounced: no request for "p", "pa", ... - only the finished word.
    expect(requests.filter((url) => url.searchParams.has('q')).map((url) => url.searchParams.get('q'))).toEqual(['parkir'])
  })

  it('says nothing matched when a search comes back empty', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
          handle: () => Promise.resolve(jsonResponse(page([]))),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
          handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
          handle: () => Promise.resolve(jsonResponse(purposes)),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
          handle: () => Promise.resolve(jsonResponse(accounts)),
        },
      ]),
    )
    renderAt('/history/reimbursements?q=xyz')

    expect(await screen.findByText(searchText.noResults('xyz'))).toBeInTheDocument()
  })

  it('loads the next page from the cursor and drops the button on the last page', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('cursor=c1'),
          handle: () => Promise.resolve(jsonResponse(page([claim(1)]))),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('cursor'),
          handle: () => Promise.resolve(jsonResponse(page([claim(2)], 'c1'))),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
          handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
          handle: () => Promise.resolve(jsonResponse(purposes)),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
          handle: () => Promise.resolve(jsonResponse(accounts)),
        },
      ]),
    )
    const user = userEvent.setup()
    renderAt()

    await screen.findByText('John')
    await user.click(screen.getByRole('button', { name: searchText.loadMore }))

    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByText('John')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: searchText.loadMore })).not.toBeInTheDocument()
  })

  it('combines the outstanding filter and search in the same request', async () => {
    const requests: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/members') return jsonResponse({ members, next_cursor: null })
        if (url.pathname === '/api/purposes') return jsonResponse(purposes)
        if (url.pathname === '/api/accounts') return jsonResponse(accounts)
        if (url.pathname === '/api/reimbursements') {
          requests.push(url)
          return jsonResponse(page([claim(1)]))
        }
        return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
      }),
    )
    renderAt('/history/reimbursements?q=parkir')
    await screen.findByText('Jane')

    const last = requests.at(-1)
    expect(last?.searchParams.get('outstanding')).toBe('true')
    expect(last?.searchParams.get('q')).toBe('parkir')
  })

  // The optional photo, at record time and after the fact.
  describe('receipt photos', () => {
    const receiptsText = copy.receipts

    it('attaches a photo after the fact from the claim row, and the row control then opens the viewer', async () => {
      let attached = false
      vi.stubGlobal(
        'fetch',
        routedFetch([
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
            handle: () => Promise.resolve(jsonResponse(page([claim(1, { receipt_ids: attached ? [9] : [] })]))),
          },
          {
            match: (m: string, u: string) => m === 'POST' && u.includes('/api/reimbursements/1/receipts'),
            handle: () => {
              attached = true
              return Promise.resolve(jsonResponse({ id: 9, uploaded_at: 1 } satisfies ReceiptUploadResult, 201))
            },
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
            handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
            handle: () => Promise.resolve(jsonResponse(purposes)),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
            handle: () => Promise.resolve(jsonResponse(accounts)),
          },
        ]),
      )
      renderAt()
      await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

      // No photo yet: the row control's own accessible name is "Tambah foto nota".
      await userEvent.click(screen.getByRole('button', { name: receiptsText.addFromRow }))
      const dialog = await screen.findByRole('dialog')
      await userEvent.upload(
        within(dialog).getByLabelText(receiptsText.addFromRow, { selector: 'input[type="file"]' }),
        new File(['fake-bytes'], 'nota.jpg', { type: 'image/jpeg' }),
      )
      await userEvent.click(within(dialog).getByRole('button', { name: receiptsText.addFromRow }))

      // The dialog shows the new photo once the refetch resolves - the
      // per-photo "more" menu only renders once a receipt exists (the photo is
      // alt="" - decorative, no accessible "img" role). Radix hides the row
      // underneath from the accessibility tree while the dialog stays open, so
      // the row's renamed control is only checked after closing it.
      await waitFor(() => expect(within(dialog).getByRole('button', { name: receiptsText.photoMenuAria })).toBeInTheDocument())
      await userEvent.click(within(dialog).getAllByRole('button', { name: copy.common.close })[0])

      // The list refetch now answers with a receipt attached, so the row's
      // control renames itself to "Lihat nota".
      await waitFor(() => expect(screen.getByRole('button', { name: receiptsText.viewReceipt })).toBeInTheDocument())
    })

    it('deletes a photo from the viewer, after the confirm', async () => {
      let deleted = false
      vi.stubGlobal(
        'fetch',
        routedFetch([
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
            handle: () => Promise.resolve(jsonResponse(page([claim(1, { receipt_ids: deleted ? [] : [9] })]))),
          },
          {
            match: (m: string, u: string) => m === 'DELETE' && u.includes('/api/receipts/9'),
            handle: () => {
              deleted = true
              return Promise.resolve(new Response(null, { status: 204 }))
            },
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
            handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
            handle: () => Promise.resolve(jsonResponse(purposes)),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
            handle: () => Promise.resolve(jsonResponse(accounts)),
          },
        ]),
      )
      renderAt()
      await waitFor(() => expect(screen.getByRole('button', { name: receiptsText.viewReceipt })).toBeInTheDocument())

      await userEvent.click(screen.getByRole('button', { name: receiptsText.viewReceipt }))
      const dialog = await screen.findByRole('dialog')

      // Hapus foto lives inside the per-photo "more" menu, not a footer
      // button under the image.
      await userEvent.click(within(dialog).getByRole('button', { name: receiptsText.photoMenuAria }))
      await userEvent.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: receiptsText.delete }))
      expect(within(dialog).getByText(receiptsText.deleteConfirm)).toBeInTheDocument()
      await userEvent.click(within(dialog).getByRole('button', { name: receiptsText.delete }))

      // As in the attach test: check the dialog's state (no photo left - the
      // "more" menu renders per receipt) before closing it and reading the
      // row's control, since the row is hidden while the dialog is open.
      await waitFor(() => expect(within(dialog).queryByRole('button', { name: receiptsText.photoMenuAria })).not.toBeInTheDocument())
      await userEvent.click(within(dialog).getAllByRole('button', { name: copy.common.close })[0])

      await waitFor(() => expect(screen.getByRole('button', { name: receiptsText.addFromRow })).toBeInTheDocument())
    })

    it('refuses to delete a claim that still has a receipt, with the specific copy', async () => {
      vi.stubGlobal(
        'fetch',
        routedFetch([
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
            handle: () => Promise.resolve(jsonResponse(page([claim(1, { receipt_ids: [9] })]))),
          },
          {
            match: (m: string, u: string) => m === 'DELETE' && u.includes('/api/reimbursements/1'),
            handle: () => Promise.resolve(jsonResponse({ error: { code: 'referenced_by_other_records', message: 'nope' } }, 409)),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'),
            handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'),
            handle: () => Promise.resolve(jsonResponse(purposes)),
          },
          {
            match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'),
            handle: () => Promise.resolve(jsonResponse(accounts)),
          },
        ]),
      )
      renderAt()
      await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

      await chooseFromRowMenu(text.actions.delete)
      const dialog = await screen.findByRole('dialog', { name: text.delete.dialogTitle })
      await userEvent.click(within(dialog).getByRole('button', { name: text.delete.confirm }))

      // The refusal is said inside the dialog, which stays open.
      await waitFor(() => expect(within(dialog).getByRole('alert').textContent).toBe(text.errors.referenced_by_other_records))
      expect(screen.getByRole('dialog', { name: text.delete.dialogTitle })).toBeInTheDocument()
    })
  })
})
