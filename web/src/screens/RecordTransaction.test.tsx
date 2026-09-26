import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import RecordTransaction from '@/screens/RecordTransaction'
import { chooseOption, selectOptionNames, selectedOptionName } from '@/test/select'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.record

/** The same normalization Incidentals.test.tsx uses: formatIDR emits a
 * non-breaking space that a text matcher will not find otherwise. */
function money(amount: number): string {
  return formatIDR(amount).replace(/\u00a0/g, ' ')
}

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

const accounts = [
  { id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
  { id: 2, kind: 'bank', name: 'Bank lama', inactive_on: '2026-01-01', created_at: 1 },
  { id: 3, kind: 'bank', name: 'Bank Uji Coba', inactive_on: null, created_at: 1 },
]

const purposes = [
  { id: 10, kind: 'pass_through', name: 'Kas Bidang', created_at: 1 },
  { id: 11, kind: 'main', name: 'Kas utama', created_at: 1 },
  { id: 12, kind: 'incidental', name: 'Halal bihalal RT', created_at: 1 },
]

const postedTransaction = {
  id: 1,
  account_id: 1,
  purpose_id: 11,
  direction: 'out',
  amount: 50000,
  occurred_on: '2026-09-02',
  kind: 'normal',
  member_id: null,
  dues_period: null,
  reimbursement_id: null,
  transfer_id: null,
  reverses_transaction_id: null,
  note: null,
  created_at: 1,
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** Routes a stubbed fetch by method + path substring, recording every call
 * so a test can assert on the body a specific route was called with. */
function routedFetch(handlers: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

/** Purpose balances, which the form fetches so the Titipan warning (#266)
 * can answer as she types. Kas Bidang holds 30.000 of somebody else's
 * money; an out larger than that is the mis-tag the warning is about. */
function balancesWith(passThroughBalance: number, tunaiBalance = 1_000_000, bankBalance = 250_000) {
  return {
    fund_total: 1_000_000,
    accounts: [
      { id: 1, kind: 'cash', name: 'Tunai', balance: tunaiBalance },
      { id: 3, kind: 'bank', name: 'Bank Uji Coba', balance: bankBalance },
    ],
    purposes: [
      { id: 10, kind: 'pass_through', name: 'Kas Bidang', balance: passThroughBalance },
      { id: 11, kind: 'main', name: 'Kas utama', balance: 1_000_000 },
      { id: 12, kind: 'incidental', name: 'Halal bihalal RT', balance: 0 },
    ],
  }
}

function stubFormLoad(passThroughBalance = 30_000) {
  return routedFetch([
    { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    { match: (m, u) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balancesWith(passThroughBalance))) },
    { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
  ])
}

describe('RecordTransaction', () => {
  // ADR-031: the picker asks for selectable=true so it never offers a
  // closed envelope's purpose - PostTransaction's own guard would refuse
  // posting to one anyway.
  it('requests only selectable purposes for the picker', async () => {
    const fetchMock = stubFormLoad()
    vi.stubGlobal('fetch', fetchMock)
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    await waitFor(() => expect(selectedOptionName(text.purposeLabel)).toBe('Kas utama'))

    const purposeCall = fetchMock.mock.calls.find(([input]) => {
      const url = typeof input === 'string' ? input : input.toString()
      return url.includes('/api/purposes')
    })
    expect(purposeCall?.[0]).toContain('selectable=true')
  })

  // The themed Select shows the chosen item's name, not its id (M6.15), so
  // these assert on what she actually sees in the closed field.
  it('defaults the purpose to the kind:"main" row, not whichever purpose sorts first', async () => {
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    // id 11 "Kas utama", not id 10 "Kas Bidang", which sorts first.
    await waitFor(() => expect(selectedOptionName(text.purposeLabel)).toBe('Kas utama'))
  })

  it('seeds the purpose from initialPurposeId, so M6.19 can reuse this form for an envelope', async () => {
    // The incidentals screen navigates here as /record?purpose=12 rather
    // than carrying a second copy of these fields (M6.19).
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} initialPurposeId={12} />)

    await waitFor(() => expect(selectedOptionName(text.purposeLabel)).toBe('Halal bihalal RT'))
  })

  it('falls back to the main purpose when initialPurposeId names one the fund no longer has', async () => {
    // A stale link would otherwise leave the picker on an id nothing
    // matches, and the form unsubmittable for no visible reason.
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} initialPurposeId={999} />)

    await waitFor(() => expect(selectedOptionName(text.purposeLabel)).toBe('Kas utama'))
  })

  it('excludes a retired account from the location picker and its default', async () => {
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    // First active account (id 1, "Tunai") is the default when nothing was
    // remembered yet.
    await waitFor(() => expect(selectedOptionName(text.locationLabel)).toBe('Tunai'))
    expect(await selectOptionNames(text.locationLabel)).toEqual(['Tunai', 'Bank Uji Coba'])
  })

  it('defaults the location to the last one remembered in localStorage, when it is still active', async () => {
    window.localStorage.setItem('uruni:record:last-account-id', '3')
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    await waitFor(() => expect(selectedOptionName(text.locationLabel)).toBe('Bank Uji Coba'))
  })

  it('falls back to the first active account when the remembered one was retired since', async () => {
    window.localStorage.setItem('uruni:record:last-account-id', '2')
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    await waitFor(() => expect(selectedOptionName(text.locationLabel)).toBe('Tunai'))
  })

  it('defaults the date to today', async () => {
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    const dateInput = (await screen.findByLabelText(text.dateLabel)) as HTMLInputElement
    const now = new Date()
    const expected = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    expect(dateInput.value).toBe(expected)
  })

  it('posts the plain integer amount typed, remembers the location, and calls onRecorded', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
      { match: (m, u) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balancesWith(30_000))) },
      { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      {
        match: (m, u) => m === 'POST' && u.includes('/api/transactions'),
        handle: () => Promise.resolve(jsonResponse(postedTransaction, 201)),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    const onRecorded = vi.fn()
    render(<RecordTransaction onRecorded={onRecorded} onCancel={vi.fn()} />)

    await screen.findByLabelText(text.locationLabel)
    await userEvent.type(screen.getByLabelText(text.amountLabel), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    // No photo picked, so photoFailed is always false - see this
    // screen's own doc comment on onRecorded's second argument (#154).
    await waitFor(() => expect(onRecorded).toHaveBeenCalledWith('out', false))

    const postCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/api/transactions'))
    expect(postCall).toBeDefined()
    const body = JSON.parse((postCall![1] as RequestInit).body as string) as Record<string, unknown>
    expect(body).toMatchObject({
      account_id: 1,
      purpose_id: 11,
      direction: 'out',
      amount: 50000,
      is_adjustment: false,
    })
    expect(typeof body.amount).toBe('number')

    expect(window.localStorage.getItem('uruni:record:last-account-id')).toBe('1')
  })

  // Installed to a home screen there is no browser back button (ADR-008's
  // display: standalone), so leaving without recording has to be possible
  // from inside the form.
  it('leaves without posting anything when cancelled', async () => {
    const onCancel = vi.fn()
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={onCancel} />)

    await userEvent.click(await screen.findByRole('button', { name: text.cancel }))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('keeps the submit button disabled until an amount is entered', async () => {
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    await screen.findByLabelText(text.locationLabel)
    expect(screen.getByRole('button', { name: text.submit })).toBeDisabled()

    await userEvent.type(screen.getByLabelText(text.amountLabel), '1000')
    expect(screen.getByRole('button', { name: text.submit })).not.toBeDisabled()
  })
})

describe('RecordTransaction: the Titipan warning (#266)', () => {
  // Paying the parent body is two different things wearing one shape (PRD
  // section 7.6). The warning fires on the one that is wrong and stays
  // silent on the one that is right, which is possible because a genuine
  // forward can never take a titipan below zero - the fund collected the
  // money before it forwarded it.
  async function fillOut(amount: string, purposeName: string) {
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(text.amountLabel), amount)
    await chooseOption(text.purposeLabel, purposeName)
    return user
  }

  it('warns when an out tagged to a titipan would take it below zero, naming Kas Utama', async () => {
    vi.stubGlobal('fetch', stubFormLoad(30_000))
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())

    await fillOut('50000', 'Kas Bidang')

    expect(await screen.findByText(text.passThroughNegativeHint('Kas Bidang'))).toBeInTheDocument()
  })

  it('stays silent on a forward the titipan actually holds the money for', async () => {
    vi.stubGlobal('fetch', stubFormLoad(80_000))
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())

    await fillOut('50000', 'Kas Bidang')

    expect(screen.queryByText(text.passThroughNegativeHint('Kas Bidang'))).not.toBeInTheDocument()
  })

  it('stays silent on money coming in, whatever the titipan holds', async () => {
    vi.stubGlobal('fetch', stubFormLoad(0))
    const user = userEvent.setup()
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: text.directionIn }))
    await fillOut('50000', 'Kas Bidang')

    expect(screen.queryByText(text.passThroughNegativeHint('Kas Bidang'))).not.toBeInTheDocument()
  })

  it('stays silent for Kas Utama and for an amplop, which may go negative on their own terms', async () => {
    // ADR-031 blessed an incidental's shortfall; nothing here second-guesses
    // the fund's own routine money either.
    vi.stubGlobal('fetch', stubFormLoad(0))
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())

    await fillOut('5000000', 'Halal bihalal RT')

    expect(screen.queryByText(text.passThroughNegativeHint('Kas Bidang'))).not.toBeInTheDocument()
    expect(screen.queryByText(text.passThroughNegativeHint('Halal bihalal RT'))).not.toBeInTheDocument()
  })

  it('warns without blocking - she may mean it, and #276 makes it correctable either way', async () => {
    vi.stubGlobal('fetch', stubFormLoad(0))
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())

    await fillOut('50000', 'Kas Bidang')

    await screen.findByText(text.passThroughNegativeHint('Kas Bidang'))
    expect(screen.getByRole('button', { name: text.submit })).toBeEnabled()
  })
})

describe('RecordTransaction: Pindah lokasi (#235)', () => {
  // POST /api/transfers existed since M4 with no UI path at all, so cash
  // deposited at the bank could not be recorded and the per-location split -
  // the whole reason balances are tracked per location, PRD section 6 -
  // drifted from reality.
  function stubTransferPost() {
    const posts: unknown[] = []
    vi.stubGlobal(
      'fetch',
      routedFetch([
        { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
        { match: (m, u) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balancesWith(30_000))) },
        { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
        {
          match: (m, u) => m === 'POST' && u.includes('/api/transfers'),
          handle: () => Promise.resolve(jsonResponse({ id: 1, kind: 'between_accounts', created_at: 1 }, 201)),
        },
      ]),
    )
    return posts
  }

  async function chooseTransfer() {
    const user = userEvent.setup()
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: text.directionTransfer }))
    return user
  }

  it('swaps the one location field for from/to and hides the peruntukan', async () => {
    stubTransferPost()
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await chooseTransfer()

    expect(screen.getByLabelText(text.fromLocationLabel)).toBeInTheDocument()
    expect(screen.getByLabelText(text.toLocationLabel)).toBeInTheDocument()
    // A transfer has no peruntukan to choose: both legs carry the same one
    // and it nets to zero (ADR-024).
    expect(screen.queryByLabelText(text.purposeLabel)).not.toBeInTheDocument()
  })

  it('refuses the same location on both sides, saying why, and keeps submit disabled', async () => {
    stubTransferPost()
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    const user = await chooseTransfer()

    await user.type(screen.getByLabelText(text.amountLabel), '50000')
    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Tunai')

    expect(await screen.findByText(text.sameLocationHint)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.submit })).toBeDisabled()
  })

  it('posts both legs through /api/transfers, with Kas Utama as the purpose and the note on the pair', async () => {
    const fetchMock = vi.fn()
    const posts: { url: string; body: Record<string, unknown> }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const method = (init?.method ?? 'GET').toUpperCase()
        if (method === 'POST' && url.includes('/api/transfers')) {
          posts.push({ url, body: JSON.parse(String(init?.body)) })
          return Promise.resolve(jsonResponse({ id: 1, kind: 'between_accounts', created_at: 1 }, 201))
        }
        if (url.includes('/api/accounts')) return Promise.resolve(jsonResponse(accounts))
        if (url.includes('/api/balances')) return Promise.resolve(jsonResponse(balancesWith(30_000)))
        if (url.includes('/api/purposes')) return Promise.resolve(jsonResponse(purposes))
        return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      }),
    )
    void fetchMock

    const onRecorded = vi.fn()
    render(<RecordTransaction onRecorded={onRecorded} onCancel={vi.fn()} />)
    const user = await chooseTransfer()

    await user.type(screen.getByLabelText(text.amountLabel), '50000')
    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Bank Uji Coba')
    await user.type(screen.getByLabelText(text.noteLabel), 'Setor tunai ke bank')
    await user.click(screen.getByRole('button', { name: text.submit }))

    await waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0].body).toMatchObject({
      from_account_id: 1,
      to_account_id: 3,
      amount: 50_000,
      // Kas Utama, never asked for: both legs carry it, so it nets to zero
      // on every purpose balance (#235).
      purpose_id: 11,
      note: 'Setor tunai ke bank',
    })
    // The caller is told which of the three it was, so Beranda shows the
    // confirmation that names the total staying put.
    expect(onRecorded).toHaveBeenCalledWith('transfer')
  })

  it('never posts a transfer to /api/transactions', async () => {
    // A transfer is a different route, not a third kind of transaction
    // (ADR-024, ADR-027). One row would change the fund total; nothing moved,
    // so nothing may.
    stubTransferPost()
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    const user = await chooseTransfer()

    await user.type(screen.getByLabelText(text.amountLabel), '50000')
    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Bank Uji Coba')
    await user.click(screen.getByRole('button', { name: text.submit }))

    const calls = (globalThis.fetch as unknown as { mock: { calls: [RequestInfo | URL, RequestInit?][] } }).mock.calls
    const posted = calls.filter(([, init]) => (init?.method ?? 'GET').toUpperCase() === 'POST').map(([input]) => String(input))
    expect(posted.some((u) => u.includes('/api/transfers'))).toBe(true)
    expect(posted.some((u) => u.includes('/api/transactions'))).toBe(false)
  })

  it('leaves the two ordinary directions alone', async () => {
    stubTransferPost()
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())

    expect(screen.getByLabelText(text.purposeLabel)).toBeInTheDocument()
    expect(screen.queryByLabelText(text.toLocationLabel)).not.toBeInTheDocument()
  })
})

describe('RecordTransaction: moving money, with its balances in view (#235 revision)', () => {
  function stubWith(tunai: number, bank: number) {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
        { match: (m, u) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balancesWith(30_000, tunai, bank))) },
        { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
        {
          match: (m, u) => m === 'POST' && u.includes('/api/transfers'),
          handle: () => Promise.resolve(jsonResponse({ id: 1, kind: 'between_accounts', created_at: 1 }, 201)),
        },
      ]),
    )
  }

  async function startTransfer() {
    const user = userEvent.setup()
    await waitFor(() => expect(screen.getByLabelText(text.locationLabel)).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: text.directionTransfer }))
    return user
  }

  it('shows what each location holds, so the move can be read against it', async () => {
    stubWith(135_000, 250_000)
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    const user = await startTransfer()

    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Bank Uji Coba')
    void user

    expect(screen.getByText(text.locationBalance(money(135_000)))).toBeInTheDocument()
    expect(screen.getByText(text.locationBalance(money(250_000)))).toBeInTheDocument()
  })

  it('warns when the move would take the SOURCE below zero, without blocking it', async () => {
    stubWith(30_000, 250_000)
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    const user = await startTransfer()

    await user.type(screen.getByLabelText(text.amountLabel), '50000')
    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Bank Uji Coba')

    expect(await screen.findByText(text.locationGoesNegative(money(20_000)))).toBeInTheDocument()
    // Never blocked: every other posting path already allows an out larger
    // than the balance, and a real deposit from a wallet the app believes is
    // empty has to be recordable.
    expect(screen.getByRole('button', { name: text.submit })).toBeEnabled()
  })

  it('never warns about the destination, which receiving money cannot push down', async () => {
    // The destination starts negative and stays negative - but it is moving
    // toward zero, not away from it, so there is nothing to say.
    stubWith(1_000_000, -80_000)
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    const user = await startTransfer()

    await user.type(screen.getByLabelText(text.amountLabel), '50000')
    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Bank Uji Coba')

    expect(screen.getByText(text.locationBalance(money(-80_000)))).toBeInTheDocument()
    expect(screen.queryByText(text.locationGoesNegative(money(30_000)))).not.toBeInTheDocument()
  })

  it('swaps the two locations in one tap - the deposit/withdraw pair reversed', async () => {
    stubWith(135_000, 250_000)
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)
    const user = await startTransfer()

    await chooseOption(text.fromLocationLabel, 'Tunai')
    await chooseOption(text.toLocationLabel, 'Bank Uji Coba')
    await user.click(screen.getByRole('button', { name: text.swapLocations }))

    expect(selectedOptionName(text.fromLocationLabel)).toBe('Bank Uji Coba')
    expect(selectedOptionName(text.toLocationLabel)).toBe('Tunai')
  })

  // #154: the optional photo field, posted as a second request once the
  // transaction itself exists.
  describe('the optional receipt photo', () => {
    const receiptsText = copy.receipts

    function stubWithReceiptRoute(uploadHandler: () => Promise<Response>) {
      return routedFetch([
        { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
        { match: (m, u) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balancesWith(30_000))) },
        { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
        {
          match: (m, u) => m === 'POST' && u.includes('/api/transactions') && !u.includes('/receipts'),
          handle: () => Promise.resolve(jsonResponse(postedTransaction, 201)),
        },
        {
          match: (m, u) => m === 'POST' && u.includes(`/api/transactions/${postedTransaction.id}/receipts`),
          handle: uploadHandler,
        },
      ])
    }

    async function fillAndPickPhoto(file: File) {
      await screen.findByLabelText(text.locationLabel)
      await userEvent.type(screen.getByLabelText(text.amountLabel), '50000')
      await userEvent.upload(screen.getByLabelText(receiptsText.fieldLabel), file)
    }

    it('uploads the picked photo after the transaction posts, and calls onRecorded with photoFailed: false', async () => {
      const fetchMock = stubWithReceiptRoute(() => Promise.resolve(jsonResponse({ id: 5, uploaded_at: 1 }, 201)))
      vi.stubGlobal('fetch', fetchMock)

      const onRecorded = vi.fn()
      render(<RecordTransaction onRecorded={onRecorded} onCancel={vi.fn()} />)
      const file = new File(['fake-bytes'], 'nota.jpg', { type: 'image/jpeg' })
      await fillAndPickPhoto(file)

      await userEvent.click(screen.getByRole('button', { name: text.submit }))

      await waitFor(() => expect(onRecorded).toHaveBeenCalledWith('out', false))

      const uploadCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/receipts'))
      expect(uploadCall).toBeDefined()
      const uploadBody = (uploadCall![1] as RequestInit).body as FormData
      expect(uploadBody.get('file')).toBe(file)
    })

    it('still calls onRecorded, with photoFailed: true, when the transaction posts but the photo upload fails', async () => {
      const fetchMock = stubWithReceiptRoute(() =>
        Promise.resolve(new Response(JSON.stringify({ error: { code: 'unsupported_media_type', message: 'nope' } }), { status: 415 })),
      )
      vi.stubGlobal('fetch', fetchMock)

      const onRecorded = vi.fn()
      render(<RecordTransaction onRecorded={onRecorded} onCancel={vi.fn()} />)
      await fillAndPickPhoto(new File(['fake-bytes'], 'nota.jpg', { type: 'image/jpeg' }))

      await userEvent.click(screen.getByRole('button', { name: text.submit }))

      await waitFor(() => expect(onRecorded).toHaveBeenCalledWith('out', true))
    })

    it('skipping the photo is the normal path: no receipts request is ever made', async () => {
      const fetchMock = stubWithReceiptRoute(() => Promise.resolve(jsonResponse({ id: 5, uploaded_at: 1 }, 201)))
      vi.stubGlobal('fetch', fetchMock)

      const onRecorded = vi.fn()
      render(<RecordTransaction onRecorded={onRecorded} onCancel={vi.fn()} />)
      await screen.findByLabelText(text.locationLabel)
      await userEvent.type(screen.getByLabelText(text.amountLabel), '50000')

      await userEvent.click(screen.getByRole('button', { name: text.submit }))

      await waitFor(() => expect(onRecorded).toHaveBeenCalledWith('out', false))
      expect(fetchMock.mock.calls.some(([input]) => (input as string).toString().includes('/receipts'))).toBe(false)
    })
  })
})
