import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import RecordTransaction from '@/screens/RecordTransaction'
import { selectOptionNames, selectedOptionName } from '@/test/select'
import { copy } from '@/copy/id'

const text = copy.record

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

function stubFormLoad() {
  return routedFetch([
    { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
  ])
}

describe('RecordTransaction', () => {
  // The themed Select shows the chosen item's name, not its id (M6.15), so
  // these assert on what she actually sees in the closed field.
  it('defaults the purpose to the kind:"main" row, not whichever purpose sorts first', async () => {
    vi.stubGlobal('fetch', stubFormLoad())
    render(<RecordTransaction onRecorded={vi.fn()} onCancel={vi.fn()} />)

    // id 11 "Kas utama", not id 10 "Kas Bidang", which sorts first.
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

    await waitFor(() => expect(onRecorded).toHaveBeenCalledWith('out'))

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
