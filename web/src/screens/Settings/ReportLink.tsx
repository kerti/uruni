import { useCallback, useEffect, useRef, useState } from 'react'
import SectionLabel from '@/components/SectionLabel'
import { ArrowSquareOut, Check, Copy, DownloadSimple, ShareNetwork } from '@phosphor-icons/react'

import DateField from '@/components/DateField'
import MonthField from '@/components/MonthField'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useToast } from '@/components/toast/useToast'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { saveBlob } from '@/lib/api'
import type { ApiError } from '@/lib/api'
import { fetchReportPdf, getReportMonths, replaceReportSlug, type ReportGroup, type ReportSpan } from '@/lib/setup'
import { currentPeriod } from '@/lib/dates'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Fund } from '@/lib/setup'
import { card } from '@/components/cardClasses'

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
 * report's link with Salin, Bagikan, a way to open it, a month's PDF, and
 * "Buat tautan baru" behind a confirm, the escape hatch for a link that
 * leaked.
 *
 * The confirm is a dialog addressed by `?edit=report:new` like every other
 * Pengaturan dialog (ADR-032: the URL is the source of truth); the PDF's month
 * picker is `?edit=report:pdf`. Replacing the
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
  const toast = useToast()

  // 'report' is this section's prefix (dialogTarget.ts); `new` is its only
  // value. Anything else carrying the prefix is cleared once, and a sibling's
  // value is never touched.
  const target = parseDialogTarget('report', value, ['pdf'])
  const isOwn = target.kind !== 'foreign'
  const confirming = target.kind === 'new'
  const choosingPdf = target.kind === 'named' && target.name === 'pdf'

  useEffect(() => {
    if (!isOwn || confirming || choosingPdf) return
    clear()
  }, [value, isOwn, confirming, choosingPdf, clear])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <SectionLabel>{text.heading}</SectionLabel>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      <LinkActions url={absoluteUrl(fund.report_url)} onPdf={() => open('report:pdf')} onRenew={() => open('report:new')} />

      <PdfDialog open={choosingPdf} onClose={close} reportUrl={fund.report_url} />

      <RenewDialog
        open={confirming}
        onClose={close}
        onRenewed={(updated) => {
          // Straight into the app's fund: no second fetch, and the old link
          // is never on screen once the new one exists.
          onChanged(updated)
          toast.success(text.renewed)
          close()
        }}
        refresh={refresh}
      />
    </section>
  )
}

function LinkActions({ url, onPdf, onRenew }: { url: string; onPdf: () => void; onRenew: () => void }) {
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
      <div className={`flex flex-col gap-1 ${card}`}>
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
            <ShareNetwork aria-hidden="true" className="size-4" />
            {text.share}
          </Button>
        )}
      </div>

      <Button asChild variant="outline" size="lg" className="w-full justify-center gap-2">
        <a href={url} target="_blank" rel="noopener noreferrer">
          <ArrowSquareOut aria-hidden="true" className="size-4" />
          {text.open}
        </a>
      </Button>

      <Button type="button" variant="outline" size="lg" className="w-full justify-center gap-2" onClick={onPdf}>
        <DownloadSimple aria-hidden="true" className="size-4" />
        {text.downloadPdf}
      </Button>

      <Button type="button" variant="ghost" size="lg" className="w-full justify-center" onClick={onRenew}>
        {text.renew}
      </Button>
    </div>
  )
}

/**
 * Unduh PDF's dialog: which days the statement covers, and how its list is
 * grouped - the report page's own choices, in its order (ADR-049). Periode
 * first: Bulanan, a month the report offers (GET /api/report/months: the first
 * transaction's month through the current one), opening on the current one;
 * or Rentang tanggal, Dari and Sampai between the first transaction's day and
 * today, opening on the 1st of this month through today. Each date field stops
 * at the other, so the range cannot be backwards. The grouping is the page's
 * Kelompokkan, by kind unless she picks by month.
 *
 * Never a link to /report/.../pdf: an installed PWA's scope is `/`, so iOS
 * opens that path inside the app's window - `download` and `target` both
 * ignored - and the PDF replaces the shell with no way back. The app fetches
 * the file and saves it from a `blob:` URL, which sits outside the scope: the
 * installed iOS app shows it in its file viewer (close, back, and Share for
 * Files, Print, WhatsApp) and returns to Uruni on close; a desktop browser
 * simply downloads it.
 */
function PdfDialog({ open, onClose, reportUrl }: { open: boolean; onClose: () => void; reportUrl: string }) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.downloadPdf}</DialogTitle>
        </DialogHeader>
        {/* Mounted with each opening (DialogContent unmounts on close), so
            every opening starts clean and reads the months afresh: a row
            recorded since may have moved the first month, and the current
            month turns over. */}
        <PdfMonthForm onClose={onClose} reportUrl={reportUrl} />
      </DialogContent>
    </Dialog>
  )
}

function PdfMonthForm({ onClose, reportUrl }: { onClose: () => void; reportUrl: string }) {
  const [months, loadMonths] = useApi<ReportSpan>()
  const [mode, setMode] = useState<'month' | 'range'>('month')
  // The current month from the start - always one the report offers - so
  // the fields are usable at once; the span that arrives only widens them.
  const [month, setMonth] = useState(currentPeriod)
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [group, setGroup] = useState<ReportGroup>('kind')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  const load = useCallback(
    () =>
      loadMonths(async () => {
        const span = await getReportMonths()
        setMonth(span.max)
        // The 1st of this month, or the first transaction's day if later.
        const firstOfMonth = `${span.today.slice(0, 7)}-01`
        setFrom(firstOfMonth > span.first_day ? firstOfMonth : span.first_day)
        setTo(span.today)
        return span
      }),
    [loadMonths],
  )

  useEffect(() => {
    void load()
  }, [load])

  async function handleDownload() {
    setError(null)
    setBusy(true)
    try {
      const pdf = await fetchReportPdf(reportUrl, mode === 'month' ? { month } : { from, to }, group)
      saveBlob(pdf, pdf.name)
      onClose()
    } catch (err) {
      setError(err as ApiError)
    } finally {
      setBusy(false)
    }
  }

  const span = months.status === 'success' ? months.data : undefined

  return (
    <>
      {months.status === 'error' && months.error ? (
        <ErrorState error={months.error} onRetry={() => void load()} />
      ) : (
        <>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="report-pdf-period">{text.pdfPeriodLabel}</Label>
            <Select value={mode} onValueChange={(next) => setMode(next as 'month' | 'range')} disabled={busy}>
              <SelectTrigger id="report-pdf-period" aria-label={text.pdfPeriodLabel}>
                <SelectValue>{mode === 'range' ? text.pdfPeriodRange : text.pdfPeriodMonth}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="month">{text.pdfPeriodMonth}</SelectItem>
                <SelectItem value="range">{text.pdfPeriodRange}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {mode === 'month' ? (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="report-pdf-month">{text.pdfMonthLabel}</Label>
              <MonthField
                id="report-pdf-month"
                value={month}
                onChange={setMonth}
                bounds={span ?? { min: month, max: month }}
                disabled={busy}
              />
            </div>
          ) : (
            span && (
              <>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="report-pdf-from">{text.pdfFromLabel}</Label>
                  <DateField
                    id="report-pdf-from"
                    value={from}
                    onChange={setFrom}
                    bounds={{ min: span.first_day, max: to }}
                    disabled={busy}
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="report-pdf-to">{text.pdfToLabel}</Label>
                  <DateField id="report-pdf-to" value={to} onChange={setTo} bounds={{ min: from, max: span.today }} disabled={busy} />
                </div>
              </>
            )
          )}
        </>
      )}
      {/* A labelled Select, as every fixed choice in a dialog is (Lokasi's
          Jenis, Riwayat's filter), even with two options. */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="report-pdf-group">{text.pdfGroupLabel}</Label>
        <Select value={group} onValueChange={(next) => setGroup(next as ReportGroup)} disabled={busy}>
          <SelectTrigger id="report-pdf-group" aria-label={text.pdfGroupLabel}>
            <SelectValue>{group === 'month' ? text.pdfGroupByMonth : text.pdfGroupByKind}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="kind">{text.pdfGroupByKind}</SelectItem>
            <SelectItem value="month">{text.pdfGroupByMonth}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {error && <ErrorState error={error} />}
      <DialogFooter className="mt-1">
        <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
          {copy.common.close}
        </Button>
        <Button type="button" size="lg" disabled={!span || busy} onClick={() => void handleDownload()}>
          {text.downloadPdf}
        </Button>
      </DialogFooter>
    </>
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
