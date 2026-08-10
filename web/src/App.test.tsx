import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from '@/App'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('App', () => {
  it('renders the Indonesian smoke copy', () => {
    render(<App />)
    expect(screen.getByText(copy.smoke.heading)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.smoke.check })).toBeInTheDocument()
  })

  it('reports a reachable server', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })))
    render(<App />)
    await userEvent.click(screen.getByRole('button', { name: copy.smoke.check }))
    expect(await screen.findByText(copy.smoke.online)).toBeInTheDocument()
  })

  it('says the app needs a connection when the server is unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')))
    render(<App />)
    await userEvent.click(screen.getByRole('button', { name: copy.smoke.check }))
    expect(await screen.findByText(copy.smoke.offline)).toBeInTheDocument()
  })
})
