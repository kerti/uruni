import { useEffect, useRef, useState } from 'react'
import { Check, Copy, Download, ExternalLink, Share2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { saveBlob } from '@/lib/api'
import type { ApiError } from '@/lib/api'
import { fetchReportPdf, replaceReportSlug } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Fund } from '@/lib/setup'

const text = copy.settings.report

/** How long "Tersalin" stays on the Salin button before it reads Salin again. */
const COPIED_MS = 2000

/**
 * The server sends the finished link (URUNI_BASE_URL + /report/ + slug). With
 * no base URL configured - local dev - it is the bare path, which only the
 * browser can complete.
 */
function absoluteUrl(reportUrl: string): string {
  return reportUrl.startsWith('/') ? `${window.location.origin}${reportUrl}` : reportUrl
}

/**
 * The Laporan publik card (ADR-035; ADR-032 allotted it to Pengaturan): the
 * report's link with Salin, Bagikan, a way to open it and the current
 * month's PDF, and "Buat tautan baru" behind a confirm, the escape hatch for a
 * link that leaked.
 *
 * The confirm is a dialog addressed by `?edit=report:new` like every other
 * Pengaturan dialog (ADR-032: the URL is the source of truth). Replacing the
 * link posts no ledger entry and edits no row a reader can see, so it is a
 * dialog rather than a screen.
 *
 * Bagikan is offered only where `navigator.share` exists (most phones, few
 * desktops); where it does not, the button is absent rather than disabled.
 *
 * The fund is App.tsx's, passed down as FundName's is; a new link goes back
 * through `onChanged`, and `refresh` re-reads it after an unknown outcome.
 */
export default function ReportLink({
  fund,
  onChanged,
  refresh,
}: {
  fund: Fund
  onChanged: (fund: Fund) => void
  refresh: () => Promise<void>
}) {
  const { value, open, close, clear } = useDialogParam()

  // 'report' is this section's prefix (dialogTarget.ts); `new` is its only
  // value. Anything else carrying the prefix is cleared once, and a sibling's
  // value is never touched.
  const target = parseDialogTarget('report', value)
  const isOwn = target.kind !== 'foreign'
  const confirming = target.kind === 'new'

  useEffect(() => {
    if (!isOwn || confirming) return
    clear()
  }, [value, isOwn, confirming, clear])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      <LinkActions url={absoluteUrl(fund.report_url)} reportUrl={fund.report_url} onRenew={() => open('report:new')} />

      <RenewDialog
        open={confirming}
        onClose={close}
        onRenewed={(updated) => {
          // Straight into the app's fund: no second fetch, and the old link
          // is never on screen once the new one exists.
          onChanged(updated)
          close()
        }}
        refresh={refresh}
      />
    </section>
  )
}

function LinkActions({ url, reportUrl, onRenew }: { url: string; reportUrl: string; onRenew: () => void }) {
  // Which link was copied, not a bare flag: a new link is a new thing to copy,
  // so `copied` falls back to false by itself when the url changes.
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null)
  const copied = copiedUrl === url
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  // Feature-detected per render, not at module load: it is a property of the
  // browser the card is in, and a test can stub it either way.
  const canShare = typeof navigator !== 'undefined' && typeof navigator.share === 'function'

  useEffect(() => () => clearTimeout(timer.current), [])

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(url)
    } catch {
      // No clipboard permission (or no secure context): the link is on screen
      // and selectable, so there is nothing to report.
      return
    }
    setCopiedUrl(url)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopiedUrl(null), COPIED_MS)
  }

  async function handleShare() {
    try {
      await navigator.share({ title: text.shareTitle, url })
    } catch {
      // Closing the share sheet rejects with AbortError; that is a choice, not
      // a failure.
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10">
        <span className="text-sm text-muted-foreground">{text.linkLabel}</span>
        <span className="min-w-0 text-sm font-medium break-all select-all">{url}</span>
      </div>

      <div className="flex gap-3">
        <Button type="button" variant="outline" size="lg" className="flex-1 justify-center gap-2" onClick={() => void handleCopy()}>
          {copied ? <Check aria-hidden="true" className="size-4" /> : <Copy aria-hidden="true" className="size-4" />}
          {copied ? text.copied : text.copy}
        </Button>
        {canShare && (
          <Button type="button" variant="outline" size="lg" className="flex-1 justify-center gap-2" onClick={() => void handleShare()}>
            <Share2 aria-hidden="true" className="size-4" />
            {text.share}
          </Button>
        )}
      </div>

      <Button asChild variant="outline" size="lg" className="w-full justify-center gap-2">
        <a href={url} target="_blank" rel="noopener noreferrer">
          <ExternalLink aria-hidden="true" className="size-4" />
          {text.open}
        </a>
      </Button>

      <PdfButton reportUrl={reportUrl} />

      <Button type="button" variant="ghost" size="lg" className="w-full justify-center" onClick={onRenew}>
        {text.renew}
      </Button>
    </div>
  )
}

/**
 * Unduh PDF. Never a link to /report/.../pdf: an installed PWA's scope is `/`,
 * so iOS opens that path inside the app's window - `download` and `target`
 * both ignored - and the PDF replaces the shell with no way back. The app
 * fetches the file and saves it from a `blob:` URL, which sits outside the
 * scope: the installed iOS app shows it in its file viewer (close, back, and
 * Share for Files, Print, WhatsApp) and returns to Uruni on close; a desktop
 * browser simply downloads it.
 */
function PdfButton({ reportUrl }: { reportUrl: string }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  async function handleTap() {
    setError(null)
    setBusy(true)
    try {
      const pdf = await fetchReportPdf(reportUrl)
      saveBlob(pdf, pdf.name)
    } catch (err) {
      setError(err as ApiError)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <Button
        type="button"
        variant="outline"
        size="lg"
        className="w-full justify-center gap-2"
        disabled={busy}
        onClick={() => void handleTap()}
      >
        <Download aria-hidden="true" className="size-4" />
        {text.downloadPdf}
      </Button>
      {error && <ErrorState error={error} />}
    </div>
  )
}

/** The confirm. The consequence is named in terracotta, never alarm-red
 * (ADR-032), and the dialog's X is its only close besides Tutup. */
function RenewDialog({
  open,
  onClose,
  onRenewed,
  refresh,
}: {
  open: boolean
  onClose: () => void
  onRenewed: (fund: Fund) => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<Fund>()
  const busy = state.status === 'loading'

  function handleConfirm() {
    void run(
      async () => {
        const updated = await replaceReportSlug()
        onRenewed(updated)
        return updated
      },
      { settle: refresh },
    )
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.renew}</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-attention-text">{text.renewConfirm}</p>
        {state.status === 'error' && state.error && <ErrorState error={state.error} />}
        <DialogFooter className="mt-1">
          <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
            {copy.common.close}
          </Button>
          <Button type="button" size="lg" disabled={busy} onClick={handleConfirm}>
            {busy ? text.renewing : text.renewConfirmAction}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
