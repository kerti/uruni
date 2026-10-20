import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Setup from '@/screens/Setup/Setup'
import { openSelect, selectedOptionName } from '@/test/select'
import { copy } from '@/copy/id'
import { resetFundTimeZone } from '@/lib/dates'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  vi.restoreAllMocks()
  resetFundTimeZone()
})

const text = copy.setup

const setupResult = {
  fund: {
    id: 1,
    name: 'Kas RT 04',
    currency: 'IDR',
    time_zone: 'Asia/Jakarta',
    report_slug: 'kas-rt-04',
    report_url: '/report/kas-rt-04',
    created_at: 1,
  },
  main_purpose_id: 1,
  accounts: [
    { id: 10, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
    { id: 11, kind: 'bank', name: 'Bank', inactive_on: null, created_at: 1 },
  ],
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** Routes a stubbed fetch by method + path substring. Every call is recorded on
 * the returned mock's `.mock.calls`, so a test can assert on what a route was
 * called with. */
function routedFetch(handlers: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

/** Calls made to `fetchMock` whose URL contains `pathFragment`. */
function callsTo(fetchMock: ReturnType<typeof vi.fn>, pathFragment: string) {
  return fetchMock.mock.calls.filter(([input]) => {
    const url = typeof input === 'string' ? input : (input as URL | Request).toString()
    return url.includes(pathFragment)
  }) as [string, RequestInit | undefined][]
}

async function goPastFundName(name = 'Kas RT 04') {
  await userEvent.type(screen.getByLabelText(text.fund.nameLabel), name)
  await userEvent.click(screen.getByRole('button', { name: text.next }))
}

async function goPastLocations() {
  await userEvent.click(screen.getByRole('button', { name: text.next }))
}

describe('Setup', () => {
  it('renders the fund name step first', () => {
    render(<Setup onDone={vi.fn()} />)
    expect(screen.getByText(text.fund.heading)).toBeInTheDocument()
  })

  it('cannot remove the last remaining location row', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    expect(screen.getByText(text.locations.heading)).toBeInTheDocument()
    const removeButtons = screen.getAllByRole('button', { name: text.locations.removeRow })
    expect(removeButtons).toHaveLength(2)

    await userEvent.click(removeButtons[0])
    // Down to one row: its remove button is disabled and the minimum-one-location
    // message is shown.
    const remaining = screen.getAllByRole('button', { name: text.locations.removeRow })
    expect(remaining).toHaveLength(1)
    expect(remaining[0]).toBeDisabled()
    expect(screen.getByText(text.locations.minOneLocation)).toBeInTheDocument()
  })

  it('sends no request on the locations step - only the balances step fires POST /api/setup', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('posts POST /api/setup exactly once from the balances step, with a renamed default location under its new name', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      {
        match: (m, u) => m === 'POST' && (u.includes('/api/dues-tiers') || u.includes('/api/members')),
        handle: () => Promise.resolve(jsonResponse({})),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    // Rename the seeded "Tunai" row: the row must post under the new name, not
    // get filtered out as a "default".
    const nameInputs = screen.getAllByLabelText(text.locations.nameLabel)
    await userEvent.clear(nameInputs[0])
    await userEvent.type(nameInputs[0], 'Kas Ketua RT')
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    await screen.findByText(text.roster.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as { name: string; accounts: { kind: string; name: string }[] }
    expect(body.name).toBe('Kas RT 04')
    expect(body.accounts.map((a) => a.name)).toEqual(['Kas Ketua RT', 'Bank'])
  })

  it('sends no opening_balance for a blank field, and skips the roster with no roster requests', async () => {
    const onDone = vi.fn()
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      {
        match: (m, u) => m === 'POST' && (u.includes('/api/dues-tiers') || u.includes('/api/members')),
        handle: () => Promise.resolve(jsonResponse({})),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={onDone} />)
    await goPastFundName()
    await goPastLocations()

    // Now on the balances step, every field left blank.
    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as { accounts: { opening_balance?: unknown }[] }
    expect(body.accounts.every((a) => a.opening_balance === undefined)).toBe(true)

    // On to the roster step - skip it entirely.
    expect(await screen.findByText(text.roster.heading)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: text.roster.skip }))

    expect(callsTo(fetchMock, '/api/dues-tiers')).toHaveLength(0)
    expect(callsTo(fetchMock, '/api/members')).toHaveLength(0)
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
  })

  it('sends an opening_balance only for the row whose field was filled in, on the single POST /api/setup', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    const amountField = screen.getByLabelText(text.balances.amountLabel('Tunai'))
    await userEvent.type(amountField, '50000')

    await userEvent.click(screen.getByRole('button', { name: text.next }))

    await screen.findByText(text.roster.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as {
      accounts: { name: string; opening_balance?: { amount: number; note?: string | null } }[]
    }
    const tunai = body.accounts.find((a) => a.name === 'Tunai')
    const bank = body.accounts.find((a) => a.name === 'Bank')
    // No note is typed in this wizard and nothing is generated onto the wire -
    // the row is read again months later in home's recent activity and the
    // public report through TransactionList's display label ("Saldo awal -
    // {lokasi}"), not stored text.
    expect(tunai?.opening_balance).toMatchObject({ amount: 50000 })
    expect(tunai?.opening_balance?.note).toBeUndefined()
    expect(bank?.opening_balance).toBeUndefined()
  })

  it('takes twelve digits in an opening balance and does not accept a thirteenth', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()
    await screen.findByText(text.balances.heading)

    const amountField = screen.getByLabelText(text.balances.amountLabel('Tunai')) as HTMLInputElement
    await userEvent.type(amountField, '999999999999')
    expect(amountField.value).toBe('999.999.999.999')

    await userEvent.type(amountField, '9')
    expect(amountField.value).toBe('999.999.999.999')
  })

  it('takes twelve digits in the first dues rate and does not accept a thirteenth', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      ]),
    )
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()
    await screen.findByText(text.balances.heading)
    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    await userEvent.type(screen.getByLabelText(text.roster.tierNameLabel), 'Rumah tinggal')
    const rateField = screen.getByLabelText(text.roster.rateAmountLabel) as HTMLInputElement
    await userEvent.type(rateField, '999999999999')
    expect(rateField.value).toBe('999.999.999.999')

    await userEvent.type(rateField, '9')
    expect(rateField.value).toBe('999.999.999.999')
  })

  it('going back from balances to locations and returning keeps names and amounts', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    const nameInputs = screen.getAllByLabelText(text.locations.nameLabel)
    await userEvent.clear(nameInputs[0])
    await userEvent.type(nameInputs[0], 'Kas Ketua RT')
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    const tunaiAmount = screen.getByLabelText(text.balances.amountLabel('Kas Ketua RT'))
    await userEvent.type(tunaiAmount, '75000')

    await userEvent.click(screen.getByRole('button', { name: text.back }))
    expect(await screen.findByText(text.locations.heading)).toBeInTheDocument()
    expect(screen.getAllByLabelText(text.locations.nameLabel)[0]).toHaveValue('Kas Ketua RT')

    await goPastLocations()
    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    expect(screen.getByLabelText(text.balances.amountLabel('Kas Ketua RT'))).toHaveValue('75.000')
  })

  it('removing a location after going back drops only its own amount - the rest stay on their own rows', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Tunai')), '10000')
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Bank')), '20000')

    await userEvent.click(screen.getByRole('button', { name: text.back }))
    expect(await screen.findByText(text.locations.heading)).toBeInTheDocument()

    // Remove the first row (Tunai) - Bank's amount must survive.
    await userEvent.click(screen.getAllByRole('button', { name: text.locations.removeRow })[0])
    await goPastLocations()

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    expect(screen.queryByLabelText(text.balances.amountLabel('Tunai'))).not.toBeInTheDocument()
    expect(screen.getByLabelText(text.balances.amountLabel('Bank'))).toHaveValue('20.000')
  })

  it('stays on the balances step when POST /api/setup fails, and a resubmit sends one complete request', async () => {
    let calls = 0
    const fetchMock = routedFetch([
      {
        match: (m, u) => m === 'POST' && u.includes('/api/setup'),
        handle: () => {
          calls += 1
          if (calls === 1) return Promise.resolve(jsonResponse({ error: { code: 'invalid_argument', message: 'bad' } }, 400))
          return Promise.resolve(jsonResponse(setupResult, 201))
        },
      },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Tunai')), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    // Still on balances, with the failure rendered - never advanced past a call
    // that did not succeed.
    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.invalid_argument)
    expect(screen.getByText(text.balances.heading)).toBeInTheDocument()
    expect(screen.queryByText(text.roster.heading)).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(2)
    const body = JSON.parse(setupCalls[1][1]?.body as string) as {
      accounts: { name: string; opening_balance?: { amount: number } }[]
    }
    expect(body.accounts.find((a) => a.name === 'Tunai')?.opening_balance).toMatchObject({ amount: 50000 })
  })

  // The roster step's writes: the tier, its first rate effective from the
  // current month (never backdated, so nobody owes a month before adoption),
  // then each named member joined today on that tier.
  it('finishes the roster with the tier, its first rate this month, and members joined today', async () => {
    // Only Date is faked, at an explicit instant, so "today" and "this month" are
    // the fund's (WIB until a zone is chosen) whatever zone this machine is in,
    // and userEvent's timers keep running.
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-03-15T09:30:00+07:00'))
    const onDone = vi.fn()
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      {
        match: (m, u) => m === 'POST' && u.includes('/api/dues-tiers/7/rates'),
        handle: () => Promise.resolve(jsonResponse({ id: 1, tier_id: 7, amount: 20000, effective_from: '2026-03' }, 201)),
      },
      {
        match: (m, u) => m === 'POST' && u.endsWith('/api/dues-tiers'),
        handle: () => Promise.resolve(jsonResponse({ id: 7, name: 'Rumah tinggal', created_at: 1 }, 201)),
      },
      { match: (m, u) => m === 'POST' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse({ id: 1 }, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={onDone} />)
    await goPastFundName()
    await goPastLocations()
    await screen.findByText(text.balances.heading)
    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    await userEvent.type(screen.getByLabelText(text.roster.tierNameLabel), '  Rumah tinggal ')
    await userEvent.type(screen.getByLabelText(text.roster.rateAmountLabel), '20000')
    // The step starts with no member rows; each one is added on purpose.
    await userEvent.click(screen.getByRole('button', { name: text.roster.addMember }))
    await userEvent.type(screen.getAllByLabelText(text.roster.memberNameLabel)[0], 'Pak Budi')
    await userEvent.click(screen.getByRole('button', { name: text.roster.addMember }))
    await userEvent.click(screen.getByRole('button', { name: text.roster.addMember }))
    // The middle row is left blank and must not become a member.
    await userEvent.type(screen.getAllByLabelText(text.roster.memberNameLabel)[2], 'Bu Sari')
    await userEvent.click(screen.getByRole('button', { name: text.roster.finish }))

    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))

    const tierCalls = fetchMock.mock.calls.filter(([input]) => (input as string).toString().endsWith('/api/dues-tiers'))
    expect(tierCalls).toHaveLength(1)
    expect(JSON.parse(tierCalls[0][1]?.body as string)).toEqual({ name: 'Rumah tinggal' })

    const rateCalls = callsTo(fetchMock, '/api/dues-tiers/7/rates')
    expect(rateCalls).toHaveLength(1)
    expect(JSON.parse(rateCalls[0][1]?.body as string)).toEqual({ amount: 20000, effective_from: '2026-03' })

    const memberBodies = callsTo(fetchMock, '/api/members').map(([, init]) => JSON.parse(init?.body as string) as unknown)
    expect(memberBodies).toEqual([
      { name: 'Pak Budi', tier_id: 7, joined_on: '2026-03-15' },
      { name: 'Bu Sari', tier_id: 7, joined_on: '2026-03-15' },
    ])
  })

  it('creates no tier or rate when the tier name is blank, and members join with no tier', async () => {
    const onDone = vi.fn()
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      { match: (m, u) => m === 'POST' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse({ id: 1 }, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={onDone} />)
    await goPastFundName()
    await goPastLocations()
    await screen.findByText(text.balances.heading)
    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    // A rate typed without a tier name has nothing to attach to.
    await userEvent.type(screen.getByLabelText(text.roster.rateAmountLabel), '20000')
    await userEvent.click(screen.getByRole('button', { name: text.roster.addMember }))
    await userEvent.type(screen.getAllByLabelText(text.roster.memberNameLabel)[0], 'Pak Budi')
    await userEvent.click(screen.getByRole('button', { name: text.roster.finish }))

    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
    expect(callsTo(fetchMock, '/api/dues-tiers')).toHaveLength(0)
    const members = callsTo(fetchMock, '/api/members')
    expect(members).toHaveLength(1)
    expect(JSON.parse(members[0][1]?.body as string)).toMatchObject({ name: 'Pak Budi', tier_id: null })
  })

  it('never calls the deleted per-account opening-balance route', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()
    await screen.findByText(text.balances.heading)
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Tunai')), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    expect(callsTo(fetchMock, '/opening-balance')).toHaveLength(0)
  })
})

// ADR-041: step 1 asks for the fund's time zone, pre-selected from the device's
// UTC offset, and Setup is the one place the client needs a zone before the
// server has a fund.
describe('Setup time zone (ADR-041)', () => {
  const zones = copy.timeZone

  /** Opens the zone field and taps one option. An option's accessible name is
   * its label and its example line together, so it is matched by its label. */
  async function chooseZone(zone: keyof typeof zones.options) {
    const listbox = await openSelect(zones.label)
    await userEvent.click(within(listbox).getByRole('option', { name: new RegExp(`^${zones.options[zone].label}`) }))
  }

  /** The device offset the pre-selection reads, in minutes WEST of UTC (as
   * Date.getTimezoneOffset): WIB is -420. */
  function deviceOffset(minutes: number) {
    vi.spyOn(Date.prototype, 'getTimezoneOffset').mockReturnValue(minutes)
  }

  it('asks for the zone under the name, with the hint', () => {
    deviceOffset(-420)
    render(<Setup onDone={vi.fn()} />)
    expect(screen.getByRole('combobox', { name: zones.label })).toBeInTheDocument()
    expect(screen.getByText(zones.setupHint)).toBeInTheDocument()
  })

  it.each([
    [-420, zones.options['Asia/Jakarta'].label],
    [-480, zones.options['Asia/Makassar'].label],
    [-540, zones.options['Asia/Jayapura'].label],
    // Any other offset falls back to WIB.
    [0, zones.options['Asia/Jakarta'].label],
    [240, zones.options['Asia/Jakarta'].label],
    [-330, zones.options['Asia/Jakarta'].label],
  ])('pre-selects from a device %d minutes from UTC: %s', (offset, label) => {
    deviceOffset(offset)
    render(<Setup onDone={vi.fn()} />)
    expect(selectedOptionName(zones.label)).toBe(label)
  })

  it('offers the three zones, each with its example line', async () => {
    deviceOffset(-420)
    render(<Setup onDone={vi.fn()} />)
    await userEvent.click(screen.getByRole('combobox', { name: zones.label }))
    const listbox = await screen.findByRole('listbox')
    for (const option of Object.values(zones.options)) {
      expect(listbox).toHaveTextContent(option.label)
      expect(listbox).toHaveTextContent(option.example)
    }
  })

  it('sends the zone on the one POST /api/setup: the pre-selected one, or the one she chose', async () => {
    deviceOffset(-480)
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    // She taps nothing: WITA, from the device.
    await goPastFundName()
    await goPastLocations()
    await userEvent.click(await screen.findByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)
    expect(JSON.parse(callsTo(fetchMock, '/api/setup')[0][1]?.body as string)).toMatchObject({ time_zone: 'Asia/Makassar' })
  })

  it("sends a zone she chose over the device's", async () => {
    deviceOffset(-420)
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await chooseZone('Asia/Jayapura')
    expect(selectedOptionName(zones.label)).toBe(zones.options['Asia/Jayapura'].label)
    await goPastFundName()
    await goPastLocations()
    await userEvent.click(await screen.findByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)
    expect(JSON.parse(callsTo(fetchMock, '/api/setup')[0][1]?.body as string)).toMatchObject({ time_zone: 'Asia/Jayapura' })
  })

  // 22:30 WIB on 30 September is 00:30 WIT on 1 October. The opening balance is
  // dated in the zone she chose; then the roster step reads the zone the server
  // stored (here WITA, which the server answered with), not her choice.
  it('dates the opening balance in the chosen zone, and the roster step in the stored one', async () => {
    deviceOffset(-420)
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-09-30T22:30:00+07:00'))
    const stored = { ...setupResult, fund: { ...setupResult.fund, time_zone: 'Asia/Makassar' } }
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(stored, 201)) },
      {
        match: (m, u) => m === 'POST' && u.includes('/api/dues-tiers/7/rates'),
        handle: () => Promise.resolve(jsonResponse({ id: 1, tier_id: 7, amount: 20000, effective_from: '2026-09' }, 201)),
      },
      {
        match: (m, u) => m === 'POST' && u.endsWith('/api/dues-tiers'),
        handle: () => Promise.resolve(jsonResponse({ id: 7, name: 'Reguler', created_at: 1 }, 201)),
      },
      { match: (m, u) => m === 'POST' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse({ id: 1 }, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await chooseZone('Asia/Jayapura')
    await goPastFundName()
    await goPastLocations()
    await userEvent.type(await screen.findByLabelText(text.balances.amountLabel('Tunai')), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    const setupBody = JSON.parse(callsTo(fetchMock, '/api/setup')[0][1]?.body as string) as {
      accounts: { name: string; opening_balance?: { occurred_on: string } }[]
    }
    expect(setupBody.accounts.find((a) => a.name === 'Tunai')?.opening_balance?.occurred_on).toBe('2026-10-01')

    await userEvent.type(screen.getByLabelText(text.roster.tierNameLabel), 'Reguler')
    await userEvent.type(screen.getByLabelText(text.roster.rateAmountLabel), '20000')
    await userEvent.click(screen.getByRole('button', { name: text.roster.addMember }))
    await userEvent.type(screen.getAllByLabelText(text.roster.memberNameLabel)[0], 'Pak Budi')
    await userEvent.click(screen.getByRole('button', { name: text.roster.finish }))

    await waitFor(() => expect(callsTo(fetchMock, '/api/members')).toHaveLength(1))
    expect(JSON.parse(callsTo(fetchMock, '/api/dues-tiers/7/rates')[0][1]?.body as string)).toEqual({
      amount: 20000,
      effective_from: '2026-09',
    })
    expect(JSON.parse(callsTo(fetchMock, '/api/members')[0][1]?.body as string)).toMatchObject({ joined_on: '2026-09-30' })
  })
})
