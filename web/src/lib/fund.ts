// Typed calls over apiFetch for the fund itself: GET/PATCH /api/fund, the
// report link's replacement, and POST /api/setup, which creates the fund.
// Every one of them hands the fund's zone to lib/dates.ts (adoptZone below).

import { apiFetch } from '@/lib/api'
import type { Account } from '@/lib/accounts'
import { setFundTimeZone } from '@/lib/dates'
import type { AccountKind } from '@/lib/enums'

/** GET /api/fund and setupResponse.fund's shape (internal/http/setup.go). */
export interface Fund {
  id: number
  name: string
  currency: string
  /** The fund's IANA time zone (ADR-041): Asia/Jakarta, Asia/Makassar or
   * Asia/Jayapura. WIB, WITA and WIT exist only as copy (copy.timeZone). */
  time_zone: string
  report_slug: string
  /** The shareable link, built server-side from URUNI_BASE_URL. A bare
   * `/report/<slug>` path when no base URL is configured (local dev). */
  report_url: string
  created_at: number
}

/** POST /api/setup's 201 body. */
export interface SetupResult {
  fund: Fund
  main_purpose_id: number
  accounts: Account[]
}

/** One row of POST /api/setup's `accounts`. `opening_balance`, when given, is
 * posted in the same database transaction that creates the fund and its
 * accounts - never a separate request. `note` holds only what the treasurer
 * typed; the wizard has no field for it, so it is omitted in practice (the
 * row explains itself through a display label). */
export interface SetupAccountInput {
  kind: AccountKind
  name: string
  opening_balance?: { amount: number; occurred_on: string; note?: string | null }
}

/**
 * Every function below that returns a fund hands its `time_zone` to the date
 * helpers (lib/dates.ts) before its promise resolves (ADR-041), never from an
 * effect or a render: AuthedGate mounts the Shell on the render right after the
 * fetch, and every `useState(todayISODate)` under it runs then.
 */
function adoptZone(fund: Fund): Fund {
  setFundTimeZone(fund.time_zone)
  return fund
}

/** GET /api/fund - 404 not_found means "run setup". */
export function getFund(): Promise<Fund> {
  return apiFetch<Fund>('/api/fund').then(adoptZone)
}

/**
 * PATCH /api/fund - renames the kas and/or changes its time zone; each is
 * optional, at least one is required. The name is a display label: it heads
 * every screen and the public report, and nothing posted references it, so
 * this rewrites no history. A zone change rewrites nothing either (ADR-041).
 * currency and report_slug are not settable - only replaceReportSlug changes the slug.
 */
export function updateFund(patch: { name?: string; timeZone?: string }): Promise<Fund> {
  const body: Record<string, string> = {}
  if (patch.name !== undefined) body.name = patch.name
  if (patch.timeZone !== undefined) body.time_zone = patch.timeZone
  return apiFetch<Fund>('/api/fund', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then(adoptZone)
}

/**
 * POST /api/fund/report-slug - replaces the public report's link (ADR-035).
 * The old slug stops resolving at once: no history, no redirect. Answers
 * with the whole fund, so the caller swaps in the new report_url.
 */
export function replaceReportSlug(): Promise<Fund> {
  return apiFetch<Fund>('/api/fund/report-slug', { method: 'POST' }).then(adoptZone)
}

/**
 * POST /api/setup - the fund's name and time zone plus every account it starts
 * with, and each opening balance, in one call: one database transaction, or
 * nothing. A second call for a fund that already exists is 409
 * fund_already_exists. The zone it answers with is the one stored, and is held
 * for the roster step that follows.
 */
export function postSetup(name: string, timeZone: string, accounts: SetupAccountInput[]): Promise<SetupResult> {
  return apiFetch<SetupResult>('/api/setup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, time_zone: timeZone, accounts }),
  }).then((result) => {
    adoptZone(result.fund)
    return result
  })
}
