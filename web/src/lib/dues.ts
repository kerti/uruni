// Typed call over apiFetch for GET /api/dues-status (PRD section 7.3). The
// Member shape is lib/setup.ts's, imported rather than redefined.

import { apiFetch } from '@/lib/api'
import type { Member } from '@/lib/setup'

/** The schema's four dues-status values (internal/ledger's MemberDuesStatus,
 * ADR-014: English on the wire and in code, Indonesian only on screen - see
 * copy.dues.statuses). */
export type DuesStatusKind = 'unpaid' | 'partial' | 'paid' | 'paid_in_advance'

/** One row of GET /api/dues-status's roster: one member who owes dues for the
 * requested period. A member with no tier never appears -
 * DuesStatusForPeriod only classifies members who have a dues obligation. */
export interface DuesStatusRow {
  member: Member
  owed_amount: number
  paid_amount: number
  status: DuesStatusKind
  /** "YYYY-MM": the last month of the unbroken run of paid months for a
   * member paid ahead; null otherwise. */
  paid_through: string | null
}

/**
 * GET /api/dues-status?period=YYYY-MM - the dues roster for one period. A
 * missing or malformed period is passed through unvalidated and comes back
 * from the server as `invalid_argument` through the normal error path.
 */
export function getDuesStatus(period: string): Promise<DuesStatusRow[]> {
  return apiFetch<DuesStatusRow[]>(`/api/dues-status?period=${encodeURIComponent(period)}`)
}

/** One cell of the twelve-month grid (ADR-044): the three statuses
 * `DuesStatusForPeriod` reports (a month it reads as paid in advance arrives as
 * 'paid'), or 'none' - the member owes nothing that month, whatever the reason. */
export type DuesGridCell = 'paid' | 'partial' | 'unpaid' | 'none'

/** One member's row: `cells[i]` is the status for `periods[i]`. */
export interface DuesGridMember {
  id: number
  name: string
  cells: DuesGridCell[]
}

/** GET /api/dues-status/grid: the months, oldest first ("YYYY-MM", at most
 * twelve, ending at the fund's current month), and a row per member who owes in
 * any of them, in the server's order. */
export interface DuesGrid {
  periods: string[]
  members: DuesGridMember[]
}

/** GET /api/dues-status/grid - takes no parameters; the server reads the
 * fund's current month itself (ADR-041). */
export function getDuesGrid(): Promise<DuesGrid> {
  return apiFetch<DuesGrid>('/api/dues-status/grid')
}

/** One row of GET /api/members/{id}/outstanding-dues: one period this member
 * still owes something for, oldest first. No nested member (the id was the
 * path segment), and `status` is only ever 'unpaid' or 'partial'. */
export interface OutstandingDuesPeriod {
  period: string
  owed_amount: number
  paid_amount: number
  status: Extract<DuesStatusKind, 'unpaid' | 'partial'>
}

/**
 * GET /api/members/{id}/outstanding-dues?through=YYYY-MM - only the periods
 * this member still owes for, oldest first. An empty array is a normal 200:
 * the member is square, or has no tier at all.
 *
 * `through` is the fund's current month (dates.ts), always passed explicitly, so
 * the month the form shows and the month the arrears walk to are one.
 */
export function getOutstandingDues(memberId: number, through: string): Promise<OutstandingDuesPeriod[]> {
  return apiFetch<OutstandingDuesPeriod[]>(`/api/members/${memberId}/outstanding-dues?through=${encodeURIComponent(through)}`)
}

/** One period being paid in a POST /api/dues-payments body. */
export interface DuesPaymentPeriod {
  dues_period: string
  amount: number
}

/**
 * POST /api/dues-payments - one member paying one or more periods in the
 * same sitting, on the same account, purpose, date and note. Always one call
 * carrying every period: the server posts one row per period inside a single
 * database transaction, so a failure on any period leaves nothing posted.
 *
 * `note` holds only what the treasurer typed - RecordPayment.tsx has no field
 * for it, so it is always null there today. A dues payment explains itself
 * through TransactionList's display label.
 *
 * No purpose on the wire: the server always posts dues to the fund's Kas
 * Utama.
 */
export function createDuesPayment(params: {
  memberId: number
  accountId: number
  occurredOn: string
  note: string | null
  periods: DuesPaymentPeriod[]
}): Promise<unknown> {
  return apiFetch<unknown>('/api/dues-payments', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      member_id: params.memberId,
      account_id: params.accountId,
      occurred_on: params.occurredOn,
      note: params.note,
      periods: params.periods,
    }),
  })
}

/** One row of GET /api/dues-payments: a dues payment, or the
 * kind='adjustment' row that reverses one (ADR-029) - the same list.
 * `is_reversal` replaces a bare kind string, since the client only needs to
 * tell these two shapes apart.
 *
 * `reverses_transaction_id` is set only on a reversal row;
 * `reversed_by_transaction_id` only on a payment something else reverses
 * (never both). `reverses_occurred_on` rides only on a reversal row - the
 * original payment's date, so the link reads correctly even when that
 * original sits on a page not fetched yet. */
export interface DuesPaymentHistoryRow {
  id: number
  is_reversal: boolean
  member_id: number
  member_name: string
  dues_period: string
  amount: number
  occurred_on: string
  account_name: string
  note: string | null
  reverses_transaction_id: number | null
  reversed_by_transaction_id: number | null
  reverses_occurred_on: string | null
}

/** GET /api/dues-payments's optional query parameters (ADR-032 "Lists: paging
 * and search"). cursor is a previous page's nextCursor - omit it for the
 * first page. q searches member name only. */
export interface ListDuesPaymentsInput {
  q?: string
  cursor?: string
}

/** One page of GET /api/dues-payments, camelCase on this side of the wire
 * boundary. nextCursor is null once there is no further page. */
export interface DuesPaymentsPage {
  duesPayments: DuesPaymentHistoryRow[]
  nextCursor: string | null
}

/**
 * GET /api/dues-payments (keyset-paged, searchable by member name):
 * newest-first, 25 rows a page. Never period-scoped - the period selector on
 * the Iuran tab drives only the status matrix above this list.
 */
export function listDuesPayments(input: ListDuesPaymentsInput = {}): Promise<DuesPaymentsPage> {
  const params = new URLSearchParams()
  if (input.q) params.set('q', input.q)
  if (input.cursor) params.set('cursor', input.cursor)
  const query = params.toString()

  return apiFetch<{ dues_payments: DuesPaymentHistoryRow[]; next_cursor: string | null }>(
    `/api/dues-payments${query ? `?${query}` : ''}`,
  ).then((page) => ({ duesPayments: page.dues_payments, nextCursor: page.next_cursor }))
}
