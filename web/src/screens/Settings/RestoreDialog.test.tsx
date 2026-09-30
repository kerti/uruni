import { createRef } from 'react'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RestoreDialog from '@/screens/Settings/RestoreDialog'
import { copy } from '@/copy/id'
import type { RestoreDialogHandle } from '@/screens/Settings/RestoreDialog'

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

function stubFetch(overrides: { inspect?: () => Response; inspectStored?: () => Response; confirm?: () => Response }) {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = requestURL(input)
    if (url === '/api/restore/inspect') return Promise.resolve((overrides.inspect ?? (() => jsonResponse(stagedPreview)))())
    if (url.startsWith('/api/restore/inspect-stored/'))
      return Promise.resolve((overrides.inspectStored ?? (() => jsonResponse(stagedPreview)))())
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

  // #326: the Cadangan card's per-row "pulihkan" control skips the file
  // picker entirely and reaches this same dialog through its ref handle -
  // everything past the inspect call (the preview, the password field, the
  // confirm button above) is the identical markup the file-picked cases
  // above already cover, so this test only needs to prove the entry point
  // itself reaches the right server route.
  it('opens the confirm dialog with the preview when started from a stored backup, via the ref handle', async () => {
    const fetchMock = stubFetch({})
    const ref = createRef<RestoreDialogHandle>()
    render(<RestoreDialog ref={ref} />)

    ref.current?.openForStoredBackup('uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip')

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/restore/inspect-stored/uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
    expect(await screen.findByText(confirmText.dateLabel('2026-09-28'))).toBeInTheDocument()
  })

  // A stored backup can be pruned by retention between the list loading and
  // the tap: the failure has to show inside the dialog, not behind its overlay.
  it('shows an inspect failure inside the open dialog', async () => {
    stubFetch({ inspectStored: () => jsonResponse({ error: { code: 'not_found', message: 'Not found.' } }, 404) })
    const ref = createRef<RestoreDialogHandle>()
    render(<RestoreDialog ref={ref} />)

    act(() => ref.current?.openForStoredBackup('uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip'))

    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(common.errors.not_found)).toBeInTheDocument()
    expect(within(dialog).queryByLabelText(confirmText.passwordLabel)).not.toBeInTheDocument()
  })

  it("does not carry a cancelled attempt's wrong-password error into the next restore", async () => {
    const fetchMock = stubFetch({
      confirm: () => jsonResponse({ error: { code: 'invalid_credentials', message: 'Invalid email or password.' } }, 401),
    })
    const user = userEvent.setup()
    const ref = createRef<RestoreDialogHandle>()
    render(<RestoreDialog ref={ref} />)

    await chooseFile(fetchMock)
    await user.type(await screen.findByLabelText(confirmText.passwordLabel), 'wrong-password')
    await user.click(screen.getByRole('button', { name: confirmText.confirm }))
    await screen.findByText(common.errors.invalid_credentials)
    await user.click(screen.getByRole('button', { name: confirmText.cancel }))
    await waitFor(() => expect(screen.queryByLabelText(confirmText.passwordLabel)).not.toBeInTheDocument())

    act(() => ref.current?.openForStoredBackup('uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip'))

    await screen.findByLabelText(confirmText.passwordLabel)
    expect(screen.queryByText(common.errors.invalid_credentials)).not.toBeInTheDocument()
  })
})
