import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RestoreDialog from '@/screens/Settings/RestoreDialog'
import { copy } from '@/copy/id'

const text = copy.settings.backup
const confirmText = copy.restoreConfirm
const common = copy.common

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function requestURL(input: RequestInfo | URL): string {
  if (typeof input === 'string') return input
  if (input instanceof URL) return input.toString()
  return input.url
}

const stagedPreview = {
  token: 'a-fresh-token',
  preview: {
    date: '2026-09-28',
    total: 1_585_000,
    funds: [
      { fund_id: 1, name: 'Kas RT 05', status: 'kept', transactions_lost: 12, cutoff_date: '2026-09-26' },
      { fund_id: 2, name: 'Kas Lama', status: 'removed', transactions_lost: 0 },
      { fund_id: 3, name: 'Kas Baru', status: 'added', transactions_lost: 0 },
    ],
  },
}

function stubFetch(overrides: { inspect?: () => Response; confirm?: () => Response }) {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = requestURL(input)
    if (url === '/api/restore/inspect') return Promise.resolve((overrides.inspect ?? (() => jsonResponse(stagedPreview)))())
    if (url === '/api/restore/confirm') return Promise.resolve((overrides.confirm ?? (() => jsonResponse({})))())
    throw new Error(`stubFetch: unexpected fetch to ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

let reloadMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  reloadMock = vi.fn()
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, reload: reloadMock },
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function chooseFile(fetchMock: ReturnType<typeof vi.fn>) {
  const file = new File(['stub zip bytes'], 'uruni-2026-09-28.zip', { type: 'application/zip' })
  const input = screen.getByLabelText(text.restoreLabel, { selector: 'input[type="file"]' })
  return userEvent.upload(input, file).then(() => fetchMock)
}

describe('RestoreDialog', () => {
  it('opens the confirm dialog with the preview once a file is picked', async () => {
    const fetchMock = stubFetch({})
    render(<RestoreDialog />)

    await chooseFile(fetchMock)

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/restore/inspect', expect.objectContaining({ method: 'POST' })))
    expect(await screen.findByText(confirmText.dateLabel('2026-09-28'))).toBeInTheDocument()
    expect(screen.getByText(confirmText.fundKeptWithCutoff('Kas RT 05', 12, '2026-09-26'))).toBeInTheDocument()
    expect(screen.getByText(confirmText.fundRemoved('Kas Lama'))).toBeInTheDocument()
    expect(screen.getByText(confirmText.fundAdded('Kas Baru'))).toBeInTheDocument()
  })

  it('confirms with the token and password, then reloads the page', async () => {
    const fetchMock = stubFetch({})
    const user = userEvent.setup()
    render(<RestoreDialog />)

    await chooseFile(fetchMock)
    await screen.findByLabelText(confirmText.passwordLabel)

    await user.type(screen.getByLabelText(confirmText.passwordLabel), 'correct-horse-battery')
    await user.click(screen.getByRole('button', { name: confirmText.confirm }))

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/restore/confirm',
        expect.objectContaining({ method: 'POST', body: JSON.stringify({ token: 'a-fresh-token', password: 'correct-horse-battery' }) }),
      ),
    )
    await waitFor(() => expect(reloadMock).toHaveBeenCalled())
  })

  it('shows the shared invalid_credentials copy on a wrong password, without reloading', async () => {
    const fetchMock = stubFetch({
      confirm: () => jsonResponse({ error: { code: 'invalid_credentials', message: 'Invalid email or password.' } }, 401),
    })
    const user = userEvent.setup()
    render(<RestoreDialog />)

    await chooseFile(fetchMock)
    await screen.findByLabelText(confirmText.passwordLabel)
    await user.type(screen.getByLabelText(confirmText.passwordLabel), 'wrong-password')
    await user.click(screen.getByRole('button', { name: confirmText.confirm }))

    expect(await screen.findByText(common.errors.invalid_credentials)).toBeInTheDocument()
    expect(reloadMock).not.toHaveBeenCalled()
  })

  it('cancelling closes the dialog without confirming anything', async () => {
    const fetchMock = stubFetch({})
    const user = userEvent.setup()
    render(<RestoreDialog />)

    await chooseFile(fetchMock)
    await screen.findByLabelText(confirmText.passwordLabel)
    await user.click(screen.getByRole('button', { name: confirmText.cancel }))

    await waitFor(() => expect(screen.queryByLabelText(confirmText.passwordLabel)).not.toBeInTheDocument())
    expect(fetchMock).not.toHaveBeenCalledWith('/api/restore/confirm', expect.anything())
  })
})
