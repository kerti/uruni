import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Correct from '@/screens/Reimbursements/Correct'
import SettleDialog from '@/screens/Reimbursements/SettleDialog'
import { chooseOption, selectedOptionName, selectOptionNames } from '@/test/select'
import { copy } from '@/copy/id'
import type { Purpose } from '@/lib/purposes'
import type { Reimbursement } from '@/lib/reimbursements'
import type { Account, Member, Transaction } from '@/lib/setup'

const text = copy.reimbursements

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
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

function claim(overrides: Partial<Reimbursement> = {}): Reimbursement {
  return {
    id: 1,
    member_id: 1,
    purpose_id: 11,
    amount: 15_000,
    incurred_on: '2026-09-01',
    waived_on: null,
    settled: false,
    note: 'Parkir',
    created_at: 1,
    receipt_ids: [],
    ...overrides,
  }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

type Handler = { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }

function routedFetch(handlers: Handler[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

const lookups: Handler[] = [
  {
    match: (m, u) => m === 'GET' && u.includes('/api/members'),
    handle: () => Promise.resolve(jsonResponse({ members, next_cursor: null })),
  },
  { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
  { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
]

function getClaim(body: unknown, status = 200): Handler {
  return {
    match: (m, u) => m === 'GET' && /\/api\/reimbursements\/1$/.test(u),
    handle: () => Promise.resolve(jsonResponse(body, status)),
  }
}

const notFound = { error: { code: 'not_found', message: 'nope' } }

/** Where the screens send her: the tab, with whatever state they handed back. */
function Tab() {
  const location = useLocation()
  return <output data-testid="tab">{(location.state as { reimbursementDone?: string } | null)?.reimbursementDone ?? 'no-state'}</output>
}

function renderScreen(element: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={['/screen']}>
      <Routes>
        <Route path="/screen" element={element} />
        <Route path="/history/reimbursements" element={<Tab />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('SettleDialog', () => {
  const settle: Handler = {
    match: (m, u) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
    handle: () =>
      Promise.resolve(
        jsonResponse(
          {
            id: 1,
            account_id: 1,
            purpose_id: 11,
            direction: 'out',
            amount: 15_000,
            occurred_on: '2026-09-02',
            kind: 'reimbursement',
            member_id: 1,
            dues_period: null,
            reimbursement_id: 1,
            transfer_id: null,
            reverses_transaction_id: null,
            note: null,
            created_at: 1,
          } satisfies Transaction,
          201,
        ),
      ),
  }

  const twoAccounts = [...accounts, { id: 2, kind: 'bank', name: 'Bank Uji Coba', inactive_on: null, created_at: 1 }] satisfies Account[]
  const withAccounts = (list: Account[]): Handler[] => [
    ...lookups.filter((h) => !h.match('GET', '/api/accounts')),
    { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(list)) },
  ]

  it('opens Bayar dari on the last-used location, else the first active one, and remembers the one it paid from (#737)', async () => {
    window.localStorage.setItem('uruni:record:last-account-id', '2')
    const fetchMock = routedFetch([settle, getClaim(claim()), ...withAccounts(twoAccounts)])
    vi.stubGlobal('fetch', fetchMock)
    const onSettled = vi.fn()
    const { unmount } = renderScreen(<SettleDialog claimId={1} onClose={vi.fn()} onSettled={onSettled} onUnusable={vi.fn()} />)

    await screen.findByRole('heading', { name: text.settle.heading })
    expect(selectedOptionName(text.settle.accountLabel)).toBe('Bank Uji Coba')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))
    await waitFor(() => expect(onSettled).toHaveBeenCalled())
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')
    expect(JSON.parse(String(post?.[1]?.body))).toMatchObject({ account_id: 2 })
    expect(window.localStorage.getItem('uruni:record:last-account-id')).toBe('2')
    unmount()

    window.localStorage.setItem('uruni:record:last-account-id', '99')
    vi.stubGlobal('fetch', routedFetch([settle, getClaim(claim()), ...withAccounts(twoAccounts)]))
    renderScreen(<SettleDialog claimId={1} onClose={vi.fn()} onSettled={vi.fn()} onUnusable={vi.fn()} />)
    await screen.findByRole('heading', { name: text.settle.heading })
    expect(selectedOptionName(text.settle.accountLabel)).toBe('Tunai')
  })

  it('says whose claim it is and what for before she pays it', async () => {
    vi.stubGlobal('fetch', routedFetch([settle, getClaim(claim()), ...lookups]))
    renderScreen(<SettleDialog claimId={1} onClose={vi.fn()} onSettled={vi.fn()} onUnusable={vi.fn()} />)

    await screen.findByRole('heading', { name: text.settle.heading })
    const summary = screen.getAllByRole('definition')[0].closest('dl')!
    expect(summary).toHaveTextContent(`${text.record.memberLabel}Jane`)
    expect(summary).toHaveTextContent(`${text.record.purposeLabel}Kas Utama`)
    expect(summary).toHaveTextContent(`${text.record.dateLabel}1 September 2026`)
    expect(summary).toHaveTextContent(`${copy.transactionDetail.noteLabel}Parkir`)
    expect(summary).toHaveTextContent(`${text.record.amountLabel}Rp15.000`)
  })

  it('settles the claim and calls onSettled', async () => {
    vi.stubGlobal('fetch', routedFetch([settle, getClaim(claim()), ...lookups]))
    const onSettled = vi.fn()
    renderScreen(<SettleDialog claimId={1} onClose={vi.fn()} onSettled={onSettled} onUnusable={vi.fn()} />)

    expect(await screen.findByRole('heading', { name: text.settle.heading })).toBeInTheDocument()
    await chooseOption(text.settle.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))

    await waitFor(() => expect(onSettled).toHaveBeenCalled())
  })

  it('a named 409 says why, from copy, and keeps the dialog', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m, u) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
          handle: () =>
            Promise.resolve(jsonResponse({ error: { code: 'reimbursement_already_settled', message: 'already settled' } }, 409)),
        },
        getClaim(claim()),
        ...lookups,
      ]),
    )
    const onSettled = vi.fn()
    renderScreen(<SettleDialog claimId={1} onClose={vi.fn()} onSettled={onSettled} onUnusable={vi.fn()} />)

    await screen.findByRole('heading', { name: text.settle.heading })
    await chooseOption(text.settle.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))

    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe(text.errors.reimbursement_already_settled))
    expect(onSettled).not.toHaveBeenCalled()
    expect(screen.getByRole('heading', { name: text.settle.heading })).toBeInTheDocument()
  })

  it.each([
    ['an unknown claim', getClaim(notFound, 404)],
    ['an already settled claim', getClaim(claim({ settled: true }))],
    ['a waived claim', getClaim(claim({ waived_on: '2026-09-05' }))],
  ])('never opens for %s, and asks the caller to strip the param', async (_label, handler) => {
    vi.stubGlobal('fetch', routedFetch([handler, ...lookups]))
    const onUnusable = vi.fn()
    renderScreen(<SettleDialog claimId={1} onClose={vi.fn()} onSettled={vi.fn()} onUnusable={onUnusable} />)

    await waitFor(() => expect(onUnusable).toHaveBeenCalled())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('has no cancel button, ignores a tap outside, and closes from the X', async () => {
    vi.stubGlobal('fetch', routedFetch([settle, getClaim(claim()), ...lookups]))
    const onClose = vi.fn()
    renderScreen(<SettleDialog claimId={1} onClose={onClose} onSettled={vi.fn()} onUnusable={vi.fn()} />)

    const dialog = await screen.findByRole('dialog', { name: text.settle.heading })
    expect(within(dialog).queryByRole('button', { name: copy.common.back })).not.toBeInTheDocument()

    await new Promise((resolve) => setTimeout(resolve, 10))
    const overlay = document.querySelector('[data-slot="dialog-overlay"]')!
    fireEvent.pointerDown(overlay)
    fireEvent.pointerUp(overlay)
    fireEvent.click(overlay)
    expect(onClose).not.toHaveBeenCalled()

    await userEvent.click(within(dialog).getByRole('button', { name: copy.common.close }))
    expect(onClose).toHaveBeenCalled()
  })
})

describe('Correct', () => {
  it('opens pre-filled and PATCHes the correction', async () => {
    let body: Record<string, unknown> | null = null
    const fetchMock = routedFetch([
      {
        match: (m, u) => m === 'PATCH' && u.includes('/api/reimbursements/1'),
        handle: () => Promise.resolve(jsonResponse(claim({ amount: 20_000 }))),
      },
      getClaim(claim()),
      ...lookups,
    ])
    vi.stubGlobal('fetch', fetchMock)
    const onDone = vi.fn()
    renderScreen(<Correct claimId={1} onDone={onDone} onCancel={vi.fn()} />)

    expect(await screen.findByRole('heading', { name: text.correct.heading })).toBeInTheDocument()
    expect(screen.getByLabelText(text.record.noteLabel)).toHaveValue('Parkir')
    await userEvent.click(screen.getByRole('button', { name: text.correct.submit }))

    await waitFor(() => expect(onDone).toHaveBeenCalled())
    const patchCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH')
    body = JSON.parse(String(patchCall?.[1]?.body))
    expect(body).toMatchObject({ member_id: 1, purpose_id: 11, amount: 15_000, incurred_on: '2026-09-01', note: 'Parkir' })
  })

  const envelopes = [
    ...purposes,
    { id: 12, kind: 'incidental', name: 'Halal bihalal RT', created_at: 1 },
    { id: 13, kind: 'incidental', name: 'Tutup buku', created_at: 1 },
  ] satisfies Purpose[]
  // The server drops a closed envelope (13) from ?selectable=true and from
  // nothing else.
  const purposeLists: Handler = {
    match: (m, u) => m === 'GET' && u.includes('/api/purposes'),
    handle: () => Promise.resolve(jsonResponse(envelopes)),
  }
  const selectablePurposeList: Handler = {
    match: (m, u) => m === 'GET' && u.includes('/api/purposes?selectable=true'),
    handle: () => Promise.resolve(jsonResponse(envelopes.filter((p) => p.id !== 13))),
  }
  const others = lookups.filter((h) => !h.match('GET', '/api/purposes'))

  it('lists no closed envelope as a pos', async () => {
    vi.stubGlobal('fetch', routedFetch([getClaim(claim()), selectablePurposeList, purposeLists, ...others]))
    renderScreen(<Correct claimId={1} onDone={vi.fn()} onCancel={vi.fn()} />)

    await screen.findByRole('heading', { name: text.correct.heading })
    expect(await selectOptionNames(text.record.purposeLabel)).toEqual(['Seragam', 'Kas Utama', 'Halal bihalal RT'])
  })

  it("keeps the claim's own pos listed when its envelope has since closed, so the value still renders", async () => {
    vi.stubGlobal('fetch', routedFetch([getClaim(claim({ purpose_id: 13 })), selectablePurposeList, purposeLists, ...others]))
    renderScreen(<Correct claimId={1} onDone={vi.fn()} onCancel={vi.fn()} />)

    await screen.findByRole('heading', { name: text.correct.heading })
    expect(selectedOptionName(text.record.purposeLabel)).toBe('Tutup buku')
    expect(await selectOptionNames(text.record.purposeLabel)).toEqual(['Seragam', 'Kas Utama', 'Halal bihalal RT', 'Tutup buku'])
  })

  it("asks Jumlah, Anggota, Pos, Tanggal in Catat's talangan order", async () => {
    vi.stubGlobal('fetch', routedFetch([getClaim(claim()), ...lookups]))
    renderScreen(<Correct claimId={1} onDone={vi.fn()} onCancel={vi.fn()} />)

    await screen.findByRole('heading', { name: text.correct.heading })
    const form = document.querySelector('form') as HTMLFormElement
    const order = [text.record.amountLabel, text.record.memberLabel, text.record.purposeLabel, text.record.dateLabel].map((label) =>
      Array.from(form.querySelectorAll('label, button[role="combobox"]')).findIndex(
        (el) => el.textContent?.trim() === label || el.getAttribute('aria-label') === label,
      ),
    )
    expect(order.every((i) => i >= 0)).toBe(true)
    expect(order).toEqual([...order].sort((a, b) => a - b))
  })

  it.each([
    ['an unknown claim', getClaim(notFound, 404)],
    ['an already settled claim', getClaim(claim({ settled: true }))],
    ['a waived claim', getClaim(claim({ waived_on: '2026-09-05' }))],
  ])('redirects to the tab for %s', async (_label, handler) => {
    vi.stubGlobal('fetch', routedFetch([handler, ...lookups]))
    renderScreen(<Correct claimId={1} onDone={vi.fn()} onCancel={vi.fn()} />)
    expect(await screen.findByTestId('tab')).toBeInTheDocument()
  })
})
