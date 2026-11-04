import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/components/toast/ToastProvider'
import ReportLink from '@/screens/Settings/ReportLink'
import { copy } from '@/copy/id'
import { saveBlob } from '@/lib/api'
import type { Fund } from '@/lib/setup'

// jsdom cannot save a file; what matters is what the card hands over.
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), saveBlob: vi.fn() }))

const text = copy.settings.report

afterEach(() => {
  vi.unstubAllGlobals()
  vi.mocked(saveBlob).mockClear()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const OLD_URL = 'https://kas.example.org/report/oldoldoldoldoldoldoldoldoldoldol'
const NEW_URL = 'https://kas.example.org/report/newnewnewnewnewnewnewnewnewnewnew'
const fund: Fund = {
  id: 1,
  name: 'Kas RT 05',
  currency: 'IDR',
  time_zone: 'Asia/Jakarta',
  report_slug: 'old',
  report_url: OLD_URL,
  created_at: 1,
}

const PDF_PATH = '/report/oldoldoldoldoldoldoldoldoldoldol/pdf'
const PDF_NAME = 'laporan-kas-2026-10.pdf'
// The report's months: the first transaction's (June) through the current one.
const REPORT_MONTHS = { min: '2026-06', max: '2026-10' }

function pdfResponse() {
  // A string body: Node's Response cannot read a jsdom Blob.
  return new Response('%PDF-1.3', {
    headers: { 'Content-Type': 'application/pdf', 'Content-Disposition': `attachment; filename="${PDF_NAME}"` },
  })
}

function stubFund({ pdf = 'ok' }: { pdf?: 'ok' | 'down' } = {}) {
  const posts: string[] = []
  const pdfGets: string[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url === '/api/report/months' && method === 'GET') {
      return Promise.resolve(jsonResponse(REPORT_MONTHS))
    }
    if (url.startsWith(`${PDF_PATH}?`) && method === 'GET') {
      pdfGets.push(url)
      return pdf === 'ok' ? Promise.resolve(pdfResponse()) : Promise.reject(new TypeError('Failed to fetch'))
    }
    if (url === '/api/fund/report-slug' && method === 'POST') {
      posts.push(url)
      return Promise.resolve(jsonResponse({ ...fund, report_slug: 'new', report_url: NEW_URL }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
  })
  vi.stubGlobal('fetch', fetchMock)
  return { posts, pdfGets, fetchMock }
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

/** Holds the fund the way App.tsx does, so a new link handed back through
 * `onChanged` is what the card shows next. */
function AppFund({ initial }: { initial: Fund }) {
  const [current, setCurrent] = useState(initial)
  return <ReportLink fund={current} onChanged={setCurrent} refresh={() => Promise.resolve()} />
}

function renderCard(entry = '/settings', initial: Fund = fund) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <ToastProvider>
        <Routes>
          <Route
            path="/settings"
            element={
              <>
                <AppFund initial={initial} />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  )
}

function stubNavigator(overrides: { share?: unknown; writeText?: (s: string) => Promise<void> }) {
  const writeText = overrides.writeText ?? vi.fn(() => Promise.resolve())
  vi.stubGlobal('navigator', { clipboard: { writeText }, share: overrides.share })
  return { writeText }
}

describe('copy', () => {
  it("uses the issue's wording", () => {
    expect(text.heading).toBe('Laporan publik')
    expect(text.copy).toBe('Salin')
    expect(text.share).toBe('Bagikan')
    expect(text.renew).toBe('Buat tautan baru')
    expect(text.renewConfirm).toBe('Tautan lama berhenti bekerja. Siapa pun yang memegangnya harus diberi tautan baru.')
  })
})

describe('ReportLink', () => {
  it('shows the full link and opens it in a new tab', async () => {
    stubFund()
    stubNavigator({})
    renderCard()

    expect(await screen.findByText(OLD_URL)).toBeInTheDocument()
    const open = screen.getByRole('link', { name: text.open })
    expect(open).toHaveAttribute('href', OLD_URL)
    expect(open).toHaveAttribute('target', '_blank')
    expect(open).toHaveAttribute('rel', expect.stringContaining('noopener'))
  })

  it("opens a month picker on the current month, and saves that month's PDF, fetched same-origin", async () => {
    const { pdfGets } = stubFund()
    const share = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn() }, share, canShare: () => true })
    const user = userEvent.setup()
    renderCard()

    await user.click(await screen.findByRole('button', { name: text.downloadPdf }))
    // Nothing is fetched until the month is confirmed.
    expect(pdfGets).toEqual([])
    expect(screen.getByTestId('location-search')).toHaveTextContent('?edit=report%3Apdf')
    const dialog = await screen.findByRole('dialog', { name: text.downloadPdf })
    expect(await within(dialog).findByRole('button', { name: text.pdfMonthLabel })).toHaveTextContent('Oktober 2026')

    await user.click(within(dialog).getByRole('button', { name: text.downloadPdf }))

    // Fetched by path - the report_url's origin is not the app's.
    await waitFor(() => expect(saveBlob).toHaveBeenCalledTimes(1))
    expect(pdfGets).toEqual([`${PDF_PATH}?month=2026-10`])
    const [blob, name] = vi.mocked(saveBlob).mock.calls[0]
    expect(name).toBe(PDF_NAME)
    expect(blob.type).toBe('application/pdf')
    expect(share).not.toHaveBeenCalled()
    expect(screen.queryByRole('link', { name: text.downloadPdf })).toBeNull()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('offers only the months the report does, and saves the one chosen', async () => {
    const { pdfGets } = stubFund()
    stubNavigator({})
    const user = userEvent.setup()
    renderCard('/settings?edit=report:pdf')

    const dialog = await screen.findByRole('dialog', { name: text.downloadPdf })
    const field = await within(dialog).findByRole('button', { name: text.pdfMonthLabel })
    await waitFor(() => expect(field).toBeEnabled())
    await user.click(field)

    // Before the first transaction's month and after the current one: closed.
    expect(screen.getByRole('button', { name: 'Mei 2026' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'November 2026' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Juni 2026' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Agustus 2026' }))
    expect(field).toHaveTextContent('Agustus 2026')

    await user.click(within(dialog).getByRole('button', { name: text.downloadPdf }))
    await waitFor(() => expect(saveBlob).toHaveBeenCalledTimes(1))
    expect(pdfGets).toEqual([`${PDF_PATH}?month=2026-08`])
  })

  it('says why when the PDF cannot be fetched, and tries again on the next tap', async () => {
    const { pdfGets } = stubFund({ pdf: 'down' })
    stubNavigator({})
    const user = userEvent.setup()
    renderCard('/settings?edit=report:pdf')

    const dialog = await screen.findByRole('dialog', { name: text.downloadPdf })
    const download = within(dialog).getByRole('button', { name: text.downloadPdf })
    await waitFor(() => expect(download).toBeEnabled())
    await user.click(download)
    expect(await within(dialog).findByText(copy.common.errors.network_error)).toBeInTheDocument()
    expect(saveBlob).not.toHaveBeenCalled()

    await user.click(download)
    await waitFor(() => expect(pdfGets).toHaveLength(2))
    expect(saveBlob).not.toHaveBeenCalled()
  })

  it('reads the link from the fund it is handed, with no request of its own', async () => {
    const { fetchMock } = stubFund()
    stubNavigator({})
    renderCard()

    expect(screen.getByText(OLD_URL)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('completes a bare path against the page origin', async () => {
    stubFund()
    stubNavigator({})
    renderCard('/settings', { ...fund, report_url: '/report/abc' })

    expect(await screen.findByText(`${window.location.origin}/report/abc`)).toBeInTheDocument()
  })

  it('hides Bagikan when navigator.share is undefined', async () => {
    stubFund()
    stubNavigator({ share: undefined })
    renderCard()

    await screen.findByText(OLD_URL)
    expect(screen.queryByRole('button', { name: text.share })).not.toBeInTheDocument()
  })

  it('shows Bagikan and hands the link to navigator.share when it exists', async () => {
    stubFund()
    const share = vi.fn(() => Promise.resolve())
    stubNavigator({ share })
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: text.share }))
    expect(share).toHaveBeenCalledWith({ title: text.shareTitle, url: OLD_URL })
  })

  it('treats a dismissed share sheet as a choice, not an error', async () => {
    stubFund()
    const share = vi.fn(() => Promise.reject(new DOMException('dismissed', 'AbortError')))
    stubNavigator({ share })
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: text.share }))
    expect(share).toHaveBeenCalled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('Salin writes the link to the clipboard and says so', async () => {
    stubFund()
    const { writeText } = stubNavigator({})
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: text.copy }))
    expect(writeText).toHaveBeenCalledWith(OLD_URL)
    expect(await screen.findByRole('button', { name: text.copied })).toBeInTheDocument()
  })

  it('stays quiet when the clipboard refuses', async () => {
    stubFund()
    stubNavigator({ writeText: () => Promise.reject(new Error('denied')) })
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: text.copy }))
    expect(screen.getByRole('button', { name: text.copy })).toBeInTheDocument()
  })

  it('cancel does nothing: no request, the old link stays', async () => {
    const { posts } = stubFund()
    stubNavigator({})
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: text.renew }))
    expect(await screen.findByText(text.renewConfirm)).toBeInTheDocument()
    expect(screen.getByTestId('location-search')).toHaveTextContent('?edit=report%3Anew')

    // The footer's Tutup comes before the X in the DOM; both close.
    await userEvent.click(screen.getAllByRole('button', { name: copy.common.close })[0])
    await waitFor(() => expect(screen.queryByText(text.renewConfirm)).not.toBeInTheDocument())
    expect(posts).toEqual([])
    expect(screen.getByText(OLD_URL)).toBeInTheDocument()
  })

  it('confirm posts once, closes, and shows the new link', async () => {
    const { posts } = stubFund()
    stubNavigator({})
    renderCard()

    await userEvent.click(await screen.findByRole('button', { name: text.renew }))
    await userEvent.click(await screen.findByRole('button', { name: text.renewConfirmAction }))

    expect(await screen.findByText(NEW_URL)).toBeInTheDocument()
    expect(posts).toEqual(['/api/fund/report-slug'])
    expect(screen.queryByText(OLD_URL)).not.toBeInTheDocument()
    expect(screen.getByText(text.renewed)).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText(text.renewConfirm)).not.toBeInTheDocument())
  })

  it("clears its own malformed ?edit= value but leaves a sibling's alone", async () => {
    stubFund()
    stubNavigator({})
    renderCard('/settings?edit=report:nope')
    await screen.findByText(OLD_URL)
    await waitFor(() => expect(screen.getByTestId('location-search')).toHaveTextContent(/^$/))
  })

  it('leaves a sibling section param untouched', async () => {
    stubFund()
    stubNavigator({})
    renderCard('/settings?edit=incidental:new')
    await screen.findByText(OLD_URL)
    expect(screen.getByTestId('location-search')).toHaveTextContent('?edit=incidental:new')
  })
})
