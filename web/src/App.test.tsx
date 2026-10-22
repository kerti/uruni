import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from '@/App'
import { selectedOptionName } from '@/test/select'
import { copy } from '@/copy/id'
import { resetFundTimeZone } from '@/lib/dates'
import type { SessionStatus } from '@/lib/auth'
import type { Balances } from '@/lib/balances'
import type { Incidental, IncidentalDetail } from '@/lib/incidentals'
import type { Purpose } from '@/lib/purposes'
import type { Account, Fund, Transaction } from '@/lib/setup'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  resetFundTimeZone()
})

/** A promise this test resolves by hand, to hold the session probe in flight. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function sessionResponse(body: SessionStatus) {
  return jsonResponse(body)
}

const fund = {
  id: 1,
  name: 'Kas RT 04',
  currency: 'IDR',
  time_zone: 'Asia/Jakarta',
  report_slug: 'kas-rt-04',
  report_url: '/report/kas-rt-04',
  created_at: 1234,
} satisfies Fund

const postedTransaction = {
  id: 1,
  account_id: 1,
  purpose_id: 11,
  direction: 'in',
  amount: 1000,
  occurred_on: '2026-09-01',
  kind: 'normal',
  member_id: null,
  dues_period: null,
  reimbursement_id: null,
  transfer_id: null,
  reverses_transaction_id: null,
  note: null,
  created_at: 1,
} satisfies Transaction

function fundFoundResponse() {
  return jsonResponse(fund)
}

function fundNotFoundResponse() {
  return jsonResponse({ error: { code: 'not_found', message: 'no fund' } }, 404)
}

/** Home's four GET routes, stubbed to the emptiest shape each answers with:
 * no balances, nothing open, never reconciled (404 not_found - a normal
 * first-run state), no transactions. Every test that reaches past auth needs
 * these, since Home is what renders there. */
const emptyHomeRoutes: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[] = [
  {
    match: (m, u) => m === 'GET' && u.includes('/api/balances'),
    handle: () =>
      Promise.resolve(
        jsonResponse({ fund_total: 0, accounts: [], purposes: [], owed_to_members: 0, month: { in: 0, out: 0 } } satisfies Balances),
      ),
  },
  // Home's purpose breakdown fetches the open envelopes list too - empty.
  { match: (m, u) => m === 'GET' && u.includes('/api/incidentals'), handle: () => Promise.resolve(jsonResponse([])) },
  {
    match: (m, u) => m === 'GET' && u.includes('/api/reconciliations/open-lines'),
    handle: () => Promise.resolve(jsonResponse([])),
  },
  {
    match: (m, u) => m === 'GET' && u.includes('/api/reconciliations/latest'),
    handle: () => Promise.resolve(jsonResponse({ error: { code: 'not_found', message: 'no reconciliation' } }, 404)),
  },
  {
    match: (m, u) => m === 'GET' && u.includes('/api/transactions'),
    handle: () => Promise.resolve(jsonResponse({ transactions: [], next_cursor: null })),
  },
]

/** Routes a stubbed fetch by method + url match, as RecordTransaction.test.tsx's
 * routedFetch does - a path substring can't tell GET /api/transactions (Home's
 * list) from POST /api/transactions (the record form's create). */
function routedFetch(handlers: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    // A GET .../participation is a suffix of the path a test's
    // '/api/incidentals/<id>' matcher already satisfies (ADR-034), so it is
    // resolved before the handler list, as Incidentals.test.tsx's routedFetch
    // documents.
    if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
    const handler = handlers.find((h) => h.match(method, url))
    if (handler) return handler.handle()
    // The "Dari siapa?" picker's roster (ADR-034) is fetched on every mount of
    // RecordTransaction; a test not about that field gets an empty roster
    // rather than an unstubbed rejection, which would trip the connectivity
    // watcher and show the offline banner for the whole app (apiFetch's catch
    // calls notifyConnectivity(false) on ANY thrown fetch).
    if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
    return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
  })
}

/** An authenticated session, wired to answer GET /api/fund with 200, plus
 * Home's routes so the home screen it lands on renders. */
function authenticatedWithFund() {
  return routedFetch([
    {
      match: (m, u) => m === 'GET' && u.includes('/api/session'),
      handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
    },
    { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundFoundResponse()) },
    ...emptyHomeRoutes,
  ])
}

describe('App (session probe)', () => {
  it('shows Loading while the session probe is in flight', async () => {
    const probe = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(probe.promise))
    render(<App />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  it('renders an error with retry when the session probe fails, and retry re-probes', async () => {
    // The first session probe fails, the retry succeeds. Register's footer
    // reads /healthz too, so that is answered separately rather than taking one
    // of the two session responses.
    const session = vi
      .fn()
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce(sessionResponse({ authenticated: false, has_account: false }))
    const fetchMock = vi.fn((input: RequestInfo | URL) =>
      String(input).includes('/healthz') ? Promise.resolve(new Response(null, { status: 404 })) : session(input),
    )
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(copy.common.errors.network_error)

    await userEvent.click(screen.getByRole('button', { name: copy.common.retry }))
    expect(await screen.findByText(copy.auth.register.heading)).toBeInTheDocument()
  })

  it('renders Register when the instance has no account yet', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sessionResponse({ authenticated: false, has_account: false })))
    render(<App />)
    expect(await screen.findByText(copy.auth.register.heading)).toBeInTheDocument()
  })

  it('renders Login when an account exists but the caller has no session', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sessionResponse({ authenticated: false, has_account: true })))
    render(<App />)
    expect(await screen.findByText(copy.auth.login.heading)).toBeInTheDocument()
  })

  it('renders home once authenticated and GET /api/fund answers 200', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)
    expect(await screen.findByText(copy.home.balanceHeading)).toBeInTheDocument()
    expect(await screen.findByText(copy.home.recentActivityEmpty)).toBeInTheDocument()
  })
})

describe('App (fund probe)', () => {
  // A GET /api/fund that answers 200 on any later load skips the wizard,
  // including a mid-wizard reload - there is no client-side "setup done"
  // flag; the fund's existence is the only source of truth.
  it('skips the wizard and renders home when GET /api/fund answers 200', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)
    expect(await screen.findByText(copy.home.balanceHeading)).toBeInTheDocument()
  })

  it('renders the setup wizard when GET /api/fund answers 404 (not_found)', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'GET' && u.includes('/api/session'),
          handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        },
        { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundNotFoundResponse()) },
      ]),
    )
    render(<App />)
    expect(await screen.findByText(copy.setup.fund.heading)).toBeInTheDocument()
  })
})

describe('App (app shell chrome)', () => {
  it('titles the shell with the fund name and puts the authed screen inside it', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)

    expect(await screen.findByRole('heading', { name: fund.name })).toBeInTheDocument()
    expect(await screen.findByText(copy.home.balanceHeading)).toBeInTheDocument()
  })

  it('logs out from the shell and lands back on Login, not Register', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'GET' && u.includes('/api/session'),
          handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        },
        { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundFoundResponse()) },
        { match: (m, u) => m === 'POST' && u.includes('/api/logout'), handle: () => Promise.resolve(new Response(null, { status: 204 })) },
        ...emptyHomeRoutes,
      ]),
    )
    render(<App />)
    await screen.findByText(copy.home.balanceHeading)

    await userEvent.click(screen.getByRole('button', { name: copy.shell.logout }))

    // has_account stays true through a logout - only the session is gone.
    expect(await screen.findByText(copy.auth.login.heading)).toBeInTheDocument()
    expect(screen.queryByText(copy.auth.register.heading)).not.toBeInTheDocument()
  })

  // Neither auth nor the wizard renders inside the shell: there is no fund to
  // name yet, and no session worth a logout.
  it('does not render the shell around Login or the setup wizard', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'GET' && u.includes('/api/session'),
          handle: () => Promise.resolve(sessionResponse({ authenticated: false, has_account: true })),
        },
      ]),
    )
    const { unmount } = render(<App />)
    await screen.findByText(copy.auth.login.heading)
    expect(screen.queryByRole('button', { name: copy.shell.logout })).not.toBeInTheDocument()
    unmount()

    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'GET' && u.includes('/api/session'),
          handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        },
        { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundNotFoundResponse()) },
      ]),
    )
    render(<App />)
    await screen.findByText(copy.setup.fund.heading)
    expect(screen.queryByRole('button', { name: copy.shell.logout })).not.toBeInTheDocument()
  })
})

describe('App (record loop)', () => {
  const accounts = [{ id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 }] satisfies Account[]
  const purposes = [
    { id: 11, kind: 'main', name: 'Kas utama', created_at: 1 },
    { id: 12, kind: 'incidental', name: 'Halal bihalal RT', created_at: 1 },
  ] satisfies Purpose[]

  function authenticatedWithRecordRoutes(timeZone = 'Asia/Jakarta') {
    return routedFetch([
      {
        match: (m, u) => m === 'GET' && u.includes('/api/session'),
        handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
      },
      {
        match: (m, u) => m === 'GET' && u.includes('/api/fund'),
        handle: () => Promise.resolve(jsonResponse({ ...fund, time_zone: timeZone })),
      },
      { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
      { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      {
        match: (m, u) => m === 'POST' && u.includes('/api/transactions'),
        handle: () => Promise.resolve(jsonResponse(postedTransaction, 201)),
      },
      ...emptyHomeRoutes,
    ])
  }

  // /record?purpose=<id> is how the incidentals screen reuses this form.
  it('pre-chooses the purpose named by ?purpose= on the record route', async () => {
    window.history.pushState({}, '', '/record?purpose=12')
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)

    await screen.findByRole('heading', { name: copy.record.heading })
    await waitFor(() => expect(selectedOptionName(copy.record.purposeLabel)).toBe('Halal bihalal RT'))
    window.history.pushState({}, '', '/')
  })

  // ADR-041: the zone reaches lib/dates inside getFund, before the Shell mounts,
  // so the first form's `useState(todayISODate)` already reads the fund's day: at
  // 00:30 WIT it must not open on yesterday. The instant is the same in all three
  // cases; only the fund's zone differs.
  it.each([
    ['Asia/Jakarta', '2026-09-30T22:30:00+07:00', '30 September 2026'],
    ['Asia/Makassar', '2026-09-30T23:30:00+07:00', '1 Oktober 2026'],
    ['Asia/Jayapura', '2026-09-30T22:30:00+07:00', '1 Oktober 2026'],
  ])("opens the record form on the fund's day for a %s fund at %s", async (zone, instant, want) => {
    // Only Date is faked: the fetches and userEvent keep their real timers.
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(instant))
    window.history.pushState({}, '', '/record')
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes(zone))
    render(<App />)

    await screen.findByRole('heading', { name: copy.record.heading })
    expect(screen.getByLabelText(copy.record.dateLabel)).toHaveTextContent(want)
    window.history.pushState({}, '', '/')
  })

  // The status matrix's Catat pembayaran opens Catat with Iuran already
  // chosen - one dues form, one address - and Batal goes back through the door
  // she came in by, not to Beranda.
  it('opens Catat on Iuran from the dues matrix, and Batal returns there', async () => {
    window.history.pushState({}, '', '/dues')
    const base = authenticatedWithRecordRoutes()
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === 'string' ? input : input.toString()
        if (url.includes('/api/dues-status')) return Promise.resolve(jsonResponse([]))
        if (url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
        return base(input, init)
      }),
    )
    render(<App />)

    await userEvent.click(await screen.findByRole('button', { name: copy.dues.recordLink }))
    expect(await screen.findByRole('heading', { name: copy.dues.payment.heading })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.record.directionDues })).toHaveAttribute('aria-pressed', 'true')

    await userEvent.click(await screen.findByRole('button', { name: copy.dues.payment.cancel }))
    expect(await screen.findByLabelText(copy.dues.periodLabel)).toBeInTheDocument()
    window.history.pushState({}, '', '/')
  })

  it('falls back to the main purpose when ?purpose= is malformed', async () => {
    // Number('') is 0 and finite, so an empty param must not read as a
    // purpose id; the default is the fund's main row, as if absent.
    window.history.pushState({}, '', '/record?purpose=')
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)

    await screen.findByRole('heading', { name: copy.record.heading })
    await waitFor(() => expect(selectedOptionName(copy.record.purposeLabel)).toBe('Kas utama'))
    window.history.pushState({}, '', '/')
  })

  // The /incidentals route and its hand-off into the record form: tapping the
  // envelope's record action must land on the real form with that envelope's
  // purpose chosen. The route is detail-only (its list moved to Pengaturan),
  // so it is reached directly with ?purpose=<id>.
  it("routes an envelope's record action into the record form with its purpose chosen", async () => {
    const envelope = {
      purpose_id: 12,
      occasion: 'Halal bihalal RT',
      target_amount: null,
      opened_on: '2026-09-01',
      closed_on: null,
      created_at: 1,
      minimum_per_member: null,
    } satisfies Incidental
    window.history.pushState({}, '', '/incidentals?purpose=12')
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'GET' && u.includes('/api/incidentals/12'),
          handle: () =>
            Promise.resolve(
              jsonResponse({
                ...envelope,
                collected_amount: 0,
                disbursed_amount: 0,
                balance_amount: 0,
                recipients: [],
              } satisfies IncidentalDetail),
            ),
        },
        {
          match: (m, u) => m === 'GET' && u.includes('/api/session'),
          handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        },
        { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundFoundResponse()) },
        { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
        { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
        ...emptyHomeRoutes,
      ]),
    )
    render(<App />)

    await screen.findByText(copy.incidentals.detail.collectedLabel)
    await userEvent.click(await screen.findByRole('button', { name: copy.incidentals.actions.record }))

    await screen.findByRole('heading', { name: copy.record.heading })
    await waitFor(() => expect(selectedOptionName(copy.record.purposeLabel)).toBe('Halal bihalal RT'))
    window.history.pushState({}, '', '/')
  })

  // /incidentals with no ?purpose= (or an unparseable one) has nothing to show
  // now that its list is retired - it redirects to Pengaturan.
  it('redirects a bare /incidentals visit to settings', async () => {
    window.history.pushState({}, '', '/incidentals')
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)

    await screen.findByRole('heading', { name: copy.settings.heading })
    window.history.pushState({}, '', '/')
  })

  it('reaches the form from the footer nav, posts, and confirms on home', async () => {
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)
    await screen.findByText(copy.home.balanceHeading)

    await userEvent.click(screen.getByRole('link', { name: copy.shell.nav.record }))
    await screen.findByRole('heading', { name: copy.record.heading })

    await userEvent.type(screen.getByLabelText(copy.record.amountLabel), '50000')
    await userEvent.click(screen.getByRole('button', { name: copy.record.submit }))

    expect(await screen.findByText(copy.record.successOut)).toBeInTheDocument()
    expect(await screen.findByText(copy.home.balanceHeading)).toBeInTheDocument()
  })

  // The confirmation belongs to the history entry the successful post
  // created, not to the session: opening the form again and coming back must
  // not re-show a message about a transaction recorded minutes ago.
  it('does not re-show the confirmation on a later visit to home', async () => {
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)
    await screen.findByText(copy.home.balanceHeading)

    await userEvent.click(screen.getByRole('link', { name: copy.shell.nav.record }))
    await userEvent.type(await screen.findByLabelText(copy.record.amountLabel), '50000')
    await userEvent.click(screen.getByRole('button', { name: copy.record.submit }))
    await screen.findByText(copy.record.successOut)

    await userEvent.click(screen.getByRole('link', { name: copy.shell.nav.record }))
    await screen.findByRole('heading', { name: copy.record.heading })
    await userEvent.click(screen.getByRole('button', { name: copy.record.cancel }))

    expect(await screen.findByText(copy.home.balanceHeading)).toBeInTheDocument()
    expect(screen.queryByText(copy.record.successOut)).not.toBeInTheDocument()
  })
})

// Riwayat (ADR-032) replaces Iuran's footer slot with a tab strip - these are
// the router-level half (Shell.test.tsx covers the footer bar and its active
// markers).
describe('App (Riwayat)', () => {
  function authenticatedWithHistoryRoutes() {
    return routedFetch([
      {
        match: (m, u) => m === 'GET' && u.includes('/api/session'),
        handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
      },
      { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundFoundResponse()) },
      { match: (m, u) => m === 'GET' && u.includes('/api/dues-status'), handle: () => Promise.resolve(jsonResponse([])) },
      {
        match: (m, u) => m === 'GET' && u.includes('/api/dues-payments'),
        handle: () => Promise.resolve(jsonResponse({ dues_payments: [], next_cursor: null })),
      },
      ...emptyHomeRoutes,
    ])
  }

  it('has a link from home into Riwayat', async () => {
    vi.stubGlobal('fetch', authenticatedWithHistoryRoutes())
    render(<App />)
    await screen.findByText(copy.home.balanceHeading)

    await userEvent.click(screen.getByRole('button', { name: copy.home.recentActivityViewAll }))

    const tab = await screen.findByRole('link', { name: copy.history.tabs.transactions })
    await waitFor(() => expect(tab).toHaveAttribute('aria-current', 'page'))
  })

  // The index route's <Navigate> fires as an effect, so the tab exists a tick
  // before it reads current - waitFor, not a single findByRole snapshot.
  it('redirects /history to the Transaksi tab', async () => {
    window.history.pushState({}, '', '/history')
    vi.stubGlobal('fetch', authenticatedWithHistoryRoutes())
    render(<App />)

    const tab = await screen.findByRole('link', { name: copy.history.tabs.transactions })
    await waitFor(() => expect(tab).toHaveAttribute('aria-current', 'page'))
    window.history.pushState({}, '', '/')
  })

  // The status matrix is its own screen at /dues, outside Riwayat's tab strip
  // - it never shares a page with the payment history.
  it('renders the dues status matrix on its own screen at /dues', async () => {
    window.history.pushState({}, '', '/dues')
    vi.stubGlobal('fetch', authenticatedWithHistoryRoutes())
    render(<App />)

    expect(await screen.findByLabelText(copy.dues.periodLabel)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: copy.history.tabs.dues })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: copy.history.dues.heading })).not.toBeInTheDocument()
    window.history.pushState({}, '', '/')
  })

  // Deep link and reload both land on the right tab (the rule Shell's footer
  // states). The tab is the payment history alone, with the way into the
  // matrix at its top.
  it('deep-links straight to /history/dues with that tab current', async () => {
    window.history.pushState({}, '', '/history/dues')
    vi.stubGlobal('fetch', authenticatedWithHistoryRoutes())
    render(<App />)

    const tab = await screen.findByRole('link', { name: copy.history.tabs.dues })
    await waitFor(() => expect(tab).toHaveAttribute('aria-current', 'page'))
    expect(await screen.findByRole('heading', { name: copy.history.dues.heading })).toBeInTheDocument()
    expect(screen.queryByLabelText(copy.dues.periodLabel)).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: copy.dues.entryLink }))
    expect(await screen.findByLabelText(copy.dues.periodLabel)).toBeInTheDocument()
    window.history.pushState({}, '', '/')
  })

  // Same rule for Cek kas: /history/reconciliations is reachable by deep link
  // with its tab reading current.
  it('deep-links straight to /history/reconciliations with that tab current', async () => {
    window.history.pushState({}, '', '/history/reconciliations')
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'GET' && u.includes('/api/session'),
          handle: () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        },
        { match: (m, u) => m === 'GET' && u.includes('/api/fund'), handle: () => Promise.resolve(fundFoundResponse()) },
        { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse([])) },
        {
          match: (m, u) => m === 'GET' && u.includes('/api/reconciliations'),
          handle: () => Promise.resolve(jsonResponse({ reconciliations: [], next_cursor: null })),
        },
        ...emptyHomeRoutes,
      ]),
    )
    render(<App />)

    const tab = await screen.findByRole('link', { name: copy.reconciliation.heading })
    await waitFor(() => expect(tab).toHaveAttribute('aria-current', 'page'))
    window.history.pushState({}, '', '/')
  })
})

describe('App (connectivity watcher)', () => {
  it('shows the offline banner when the browser goes offline, and clears it when connectivity returns', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)
    // Let the session probe's successful response settle first - it calls the
    // connectivity watcher (a 2xx counts as "online") and would otherwise race
    // the offline event dispatched below and win, masking it.
    await screen.findByText(copy.home.balanceHeading)
    expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument()

    act(() => {
      window.dispatchEvent(new Event('offline'))
    })
    expect(await screen.findByText(copy.common.offlineBanner)).toBeInTheDocument()

    act(() => {
      window.dispatchEvent(new Event('online'))
    })
    await waitFor(() => expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument())
  })
})
