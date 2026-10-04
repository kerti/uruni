import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ReportLink from '@/screens/Settings/ReportLink'
import { copy } from '@/copy/id'
import { saveBlob } from '@/lib/api'

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
const fund = { id: 1, name: 'Kas RT 05', currency: 'IDR', report_slug: 'old', report_url: OLD_URL, created_at: 1 }

const PDF_PATH = '/report/oldoldoldoldoldoldoldoldoldoldol/pdf'
const PDF_NAME = 'laporan-kas-2026-10.pdf'

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
    if (url === '/api/fund' && method === 'GET') return Promise.resolve(jsonResponse(fund))
    if (url === PDF_PATH && method === 'GET') {
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
  return { posts, pdfGets }
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

function renderCard(entry = '/settings') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <ReportLink />
              <LocationProbe />
            </>
          }
        />
      </Routes>
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

  it("hands the current month's PDF to the share sheet, fetched same-origin before the tap", async () => {
    const { pdfGets } = stubFund()
    const share = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn() }, share, canShare: () => true })
    const user = userEvent.setup()
    renderCard()

    const button = await screen.findByRole('button', { name: text.downloadPdf })
    // Fetched on mount by path - the report_url's origin is not the app's.
    await waitFor(() => expect(pdfGets).toEqual([PDF_PATH]))
    await user.click(button)

    expect(share).toHaveBeenCalledTimes(1)
    const [{ files }] = share.mock.calls[0] as unknown as [{ files: File[] }]
    expect(files[0].name).toBe(PDF_NAME)
    expect(files[0].type).toBe('application/pdf')
    expect(pdfGets).toHaveLength(1)
    expect(saveBlob).not.toHaveBeenCalled()
    expect(screen.queryByRole('link', { name: text.downloadPdf })).toBeNull()
  })

  it('saves the PDF where files cannot be shared', async () => {
    stubFund()
    stubNavigator({})
    const user = userEvent.setup()
    renderCard()

    await user.click(await screen.findByRole('button', { name: text.downloadPdf }))

    await waitFor(() => expect(saveBlob).toHaveBeenCalledTimes(1))
    const [blob, name] = vi.mocked(saveBlob).mock.calls[0]
    expect(name).toBe(PDF_NAME)
    expect(blob.type).toBe('application/pdf')
  })

  it('treats a dismissed or refused share sheet as no failure', async () => {
    stubFund()
    const share = vi.fn(() => Promise.reject(new DOMException('dismissed', 'AbortError')))
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn() }, share, canShare: () => true })
    const user = userEvent.setup()
    renderCard()

    await user.click(await screen.findByRole('button', { name: text.downloadPdf }))

    await waitFor(() => expect(share).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(saveBlob).not.toHaveBeenCalled()
  })

  it('fetches again on the tap when the early fetch failed, and says why when that fails too', async () => {
    const { pdfGets } = stubFund({ pdf: 'down' })
    stubNavigator({})
    const user = userEvent.setup()
    renderCard()

    const button = await screen.findByRole('button', { name: text.downloadPdf })
    await waitFor(() => expect(pdfGets).toHaveLength(1))
    await user.click(button)

    await waitFor(() => expect(pdfGets).toHaveLength(2))
    expect(await screen.findByText(copy.common.errors.network_error)).toBeInTheDocument()
    expect(saveBlob).not.toHaveBeenCalled()
  })

  it('completes a bare path against the page origin', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse({ ...fund, report_url: '/report/abc' })))
    vi.stubGlobal('fetch', fetchMock)
    stubNavigator({})
    renderCard()

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

    await userEvent.click(screen.getByRole('button', { name: text.cancel }))
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
