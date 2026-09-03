// Typed call over apiFetch for GET /api/dues-status (M6.12, PRD §7.3), same
// idiom as lib/balances.ts and lib/reconciliations.ts. The Member shape is
// already defined in lib/setup.ts (the setup wizard's roster step created
// these same rows) - re-exported here rather than redefined, same reasoning
// as lib/accounts.ts's own Account re-export.

import { apiFetch } from '@/lib/api'
import type { Member } from '@/lib/setup'

export type { Member }

/** The schema's own four dues-status values (internal/ledger's
 * MemberDuesStatus, ADR-014: English on the wire and in code, Indonesian
 * only on screen - see copy.dues.statuses). */
export type DuesStatusKind = 'unpaid' | 'partial' | 'paid' | 'paid_in_advance'

/** One row of GET /api/dues-status's roster
 * (internal/http/dues_status.go's duesStatusResponse): one member who owes
 * dues for the requested period. A member with no tier never appears here
 * at all - DuesStatusForPeriod only classifies members who have a dues
 * obligation, so there is nothing for this screen to synthesize or hide. */
export interface DuesStatusRow {
  member: Member
  owed_amount: number
  paid_amount: number
  status: DuesStatusKind
}

/**
 * GET /api/dues-status?period=YYYY-MM - the dues roster for one period. A
 * missing or malformed period reaches the server's own validateDuesPeriod
 * check and comes back as `invalid_argument` through the normal error path
 * (dues_status.go's own doc comment); this call passes the period through
 * unvalidated and lets that happen.
 */
export function getDuesStatus(period: string): Promise<DuesStatusRow[]> {
  return apiFetch<DuesStatusRow[]>(`/api/dues-status?period=${encodeURIComponent(period)}`)
}

/** One row of GET /api/members/{id}/outstanding-dues
 * (internal/http/dues_status.go's outstandingDuesResponse): one period this
 * member still owes something for, oldest first. No nested member - the id
 * was the path segment - and `status` is only ever 'unpaid' or 'partial',
 * per OutstandingDuesForMember's own contract. */
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
 * `through` is the *client's* local month, always passed explicitly: the
 * server does not share the treasurer's timezone, so letting it default to
 * its own current month can hide a month that has already started in WIB
 * (#186's own reasoning for the parameter existing).
 */
export function getOutstandingDues(memberId: number, through: string): Promise<OutstandingDuesPeriod[]> {
  return apiFetch<OutstandingDuesPeriod[]>(
    `/api/members/${memberId}/outstanding-dues?through=${encodeURIComponent(through)}`,
  )
}

/** One period being paid in a POST /api/dues-payments body
 * (internal/http/dues_payments.go's duesPaymentPeriod). */
export interface DuesPaymentPeriod {
  dues_period: string
  amount: number
}

/**
 * POST /api/dues-payments - one member paying one or more periods in the
 * same sitting, on the same account, purpose, date and note. Always one call
 * carrying every period, never one call per period: the server posts one row
 * per period inside a single database transaction, so a failure on any
 * period leaves nothing posted (#96).
 *
 * `note` is required here even though the wire field is nullable: a dues row
 * that reaches recent activity or the public report with an empty note reads
 * as a bare amount. The caller derives it from copy, never types it.
 */
export function createDuesPayment(params: {
  memberId: number
  accountId: number
  purposeId: number
  occurredOn: string
  note: string
  periods: DuesPaymentPeriod[]
}): Promise<unknown> {
  return apiFetch<unknown>('/api/dues-payments', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      member_id: params.memberId,
      account_id: params.accountId,
      purpose_id: params.purposeId,
      occurred_on: params.occurredOn,
      note: params.note,
      periods: params.periods,
    }),
  })
}
