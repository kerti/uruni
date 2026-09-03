import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import FundName from '@/screens/Settings/FundName'
import { copy } from '@/copy/id'

const text = copy.settings.fund

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const fund = { id: 1, name: 'Kas Ruang 3A', currency: 'IDR', report_slug: 'abcdefghijklmnopqrstuv', created_at: 1 }

function stubFund() {
  const calls: { method: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/fund')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(fund))
      calls.push({ method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(jsonResponse({ ...fund, name: 'Kas Ruang 3B' }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

describe('Settings fund name', () => {
  it('seeds the field with the fund\'s current name', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    render(<FundName onRenamed={vi.fn()} />)

    expect(await screen.findByLabelText(text.nameLabel)).toHaveValue('Kas Ruang 3A')
  })

  it('renames the fund and hands the new one back to the caller', async () => {
    const { fetchMock, calls } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    const onRenamed = vi.fn()
    render(<FundName onRenamed={onRenamed} />)

    const input = await screen.findByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Kas Ruang 3B')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { name: 'Kas Ruang 3B' } })
    // Shell's header is the same name, read once when the app booted - the
    // callback is what keeps it from showing the old one until a reload.
    await waitFor(() => expect(onRenamed).toHaveBeenCalledWith(expect.objectContaining({ name: 'Kas Ruang 3B' })))
    expect(await screen.findByRole('status')).toHaveTextContent(text.saved)
  })

  it('will not submit a name that has not changed, or an empty one', async () => {
    const { fetchMock, calls } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    render(<FundName onRenamed={vi.fn()} />)

    const input = await screen.findByLabelText(text.nameLabel)
    expect(screen.getByRole('button', { name: text.save })).toBeDisabled()

    await userEvent.clear(input)
    expect(screen.getByRole('button', { name: text.save })).toBeDisabled()
    expect(calls).toHaveLength(0)
  })
})
