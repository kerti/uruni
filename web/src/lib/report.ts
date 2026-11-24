// Typed calls for the public report as the treasurer's app reads it: the
// months it offers and its PDF.

import { apiFetch, fetchBlob } from '@/lib/api'
import type { DateBounds } from '@/lib/dates'

/** What the PDF is saved as when the server's Content-Disposition names nothing. */
const REPORT_PDF_FALLBACK_NAME = 'laporan-kas.pdf'

/** How the report lays out its list (?group=): by kind - Uang masuk, Uang
 * keluar, Lainnya - the default, or by month. */
export type ReportGroup = 'kind' | 'month'

/** GET /api/report/months - what the public report offers, in the fund's
 * zone: `min`/`max` the first and last month ("YYYY-MM", no gaps, so
 * MonthField's bounds as they stand), and `first_day`/`today` the first
 * transaction's day and today ("YYYY-MM-DD"), a date range's bounds
 * (ADR-049). */
export interface ReportSpan extends DateBounds {
  first_day: string
  today: string
}

export function getReportMonths(): Promise<ReportSpan> {
  return apiFetch<ReportSpan>('/api/report/months')
}

/** Which days the report covers: one month, or a date range (ADR-049). */
export type ReportPeriod = { month: string } | { from: string; to: string }

/**
 * GET /report/{slug}/pdf?month=YYYY-MM (or ?from=&to=, a date range) - that
 * period's statement, as a File
 * ready for saveBlob, named by the server's Content-Disposition. Fetched by
 * path, never by the absolute report_url: URUNI_BASE_URL may name another
 * origin than the one the app is open on (a tunnel in dev), and the request
 * must stay same-origin. A month the report does not offer draws the current
 * month, as the page does. `group` lays the list out as the page's
 * Kelompokkan does; by kind is the server's default, so it is left out.
 */
export async function fetchReportPdf(reportUrl: string, period: ReportPeriod, group: ReportGroup = 'kind'): Promise<File> {
  const path = reportUrl.startsWith('/') ? reportUrl : new URL(reportUrl).pathname
  const days = 'month' in period ? `month=${encodeURIComponent(period.month)}` : `from=${period.from}&to=${period.to}`
  const query = `${days}${group === 'month' ? '&group=month' : ''}`
  const { blob, res } = await fetchBlob(`${path}/pdf?${query}`)
  const named = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')
  return new File([blob], named?.[1] ?? REPORT_PDF_FALLBACK_NAME, { type: 'application/pdf' })
}
