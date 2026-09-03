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
