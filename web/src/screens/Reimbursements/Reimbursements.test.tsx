import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Correct from '@/screens/Reimbursements/Correct'
import Settle from '@/screens/Reimbursements/Settle'
import { chooseOption } from '@/test/select'
import { copy } from '@/copy/id'

const text = copy.reimbursements

afterEach(() => {
  vi.unstubAllGlobals()
})

const members = [
  { id: 1, name: 'Jane', tier_id: 1, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
  { id: 2, name: 'John', tier_id: 2, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
]
const purposes = [
  { id: 10, kind: 'pass_through', name: 'Kas Bidang', created_at: 1 },
  { id: 11, kind: 'main', name: 'Kas Utama', created_at: 1 },
]
const accounts = [{ id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 }]

function claim(overrides: Record<string, unknown> = {}) {
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

describe('Settle', () => {
  const settle: Handler = {
    match: (m, u) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
    handle: () => Promise.resolve(jsonResponse({ id: 1 }, 201)),
  }

  it('settles the claim and calls onDone', async () => {
    vi.stubGlobal('fetch', routedFetch([settle, getClaim(claim()), ...lookups]))
    const onDone = vi.fn()
    renderScreen(<Settle claimId={1} onDone={onDone} onCancel={vi.fn()} />)

    expect(await screen.findByRole('heading', { name: text.settle.heading })).toBeInTheDocument()
    await chooseOption(text.settle.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))

    await waitFor(() => expect(onDone).toHaveBeenCalled())
  })

  it('a named 409 says why, from copy, and keeps the screen', async () => {
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
    const onDone = vi.fn()
    renderScreen(<Settle claimId={1} onDone={onDone} onCancel={vi.fn()} />)

    await screen.findByRole('heading', { name: text.settle.heading })
    await chooseOption(text.settle.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))

    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe(text.errors.reimbursement_already_settled))
    expect(onDone).not.toHaveBeenCalled()
    expect(screen.getByRole('heading', { name: text.settle.heading })).toBeInTheDocument()
  })

  it.each([
    ['an unknown claim', getClaim(notFound, 404)],
    ['an already settled claim', getClaim(claim({ settled: true }))],
    ['a waived claim', getClaim(claim({ waived_on: '2026-09-05' }))],
  ])('redirects to the tab for %s', async (_label, handler) => {
    vi.stubGlobal('fetch', routedFetch([handler, ...lookups]))
    renderScreen(<Settle claimId={1} onDone={vi.fn()} onCancel={vi.fn()} />)
    expect(await screen.findByTestId('tab')).toBeInTheDocument()
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
