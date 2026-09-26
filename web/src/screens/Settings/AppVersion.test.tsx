import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AppVersion from '@/screens/Settings/AppVersion'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

function stubHealthz(body: unknown, status = 200) {
  const fetchMock = vi.fn(() =>
    Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('AppVersion', () => {
  it('shows a tagged build by its version alone, read from /healthz', async () => {
    const fetchMock = stubHealthz({ status: 'ok', version: 'v0.6.0-alpha.7', commit: 'abcdef1234567' })
    render(<AppVersion />)

    expect(await screen.findByText(copy.settings.versionLine('v0.6.0-alpha.7'))).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/healthz')
  })

  it('names an untagged dev build by its short commit', async () => {
    stubHealthz({ status: 'ok', version: 'dev', commit: 'abcdef1234567' })
    render(<AppVersion />)

    expect(await screen.findByText(copy.settings.versionLine('dev (abcdef1)'))).toBeInTheDocument()
  })

  it('renders nothing when /healthz fails', async () => {
    const fetchMock = stubHealthz({}, 503)
    const { container } = render(<AppVersion />)

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })
})
