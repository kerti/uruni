// Typed calls over apiFetch for the incidental-envelope routes (M6.19), same
// idiom as lib/reimbursements.ts. The backend is already complete
// (internal/http/incidentals.go); this module is the frontend's typed
// surface over it. Contributions and disbursements are not their own route -
// they are ordinary POST /api/transactions calls tagged to the envelope's
// own purpose, so this module has no call for them; lib/transactions.ts
// already covers that.

import { apiFetch } from '@/lib/api'

/** One incidental envelope on its own (internal/http/incidentals.go). No
 * fund_id, same reasoning as every other response type in that package.
 * `target_amount` and `closed_on` are nullable: a target is optional at
 * opening (PRD §7.5), and an envelope stays open until deliberately closed. */
export interface Incidental {
  purpose_id: number
  occasion: string
  target_amount: number | null
  opened_on: string
  closed_on: string | null
  created_at: number
}

/** An envelope plus the totals PRD §7.5 shows for it - what
 * GET /api/incidentals/{purposeID} answers with. Both totals are server-
 * computed; nothing on this screen re-derives them from a transaction list. */
export interface IncidentalDetail extends Incidental {
  collected_amount: number
  disbursed_amount: number
}

/**
 * GET /api/incidentals, optionally ?open=true for only the envelopes still
 * collecting. An unparseable value is a 400 on the server; this module never
 * sends one.
 */
export function listIncidentals(openOnly = false): Promise<Incidental[]> {
  const query = openOnly ? '?open=true' : ''
  return apiFetch<Incidental[]>(`/api/incidentals${query}`)
}

/**
 * POST /api/incidentals - opens a new envelope for an occasion. There is no
 * separate name field: occasion doubles as the purpose's own name, the same
 * choice openIncidentalRequest's own comment explains.
 */
export function openIncidental(input: {
  occasion: string
  targetAmount: number | null
  openedOn: string
}): Promise<Incidental> {
  return apiFetch<Incidental>('/api/incidentals', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      occasion: input.occasion,
      target_amount: input.targetAmount,
      opened_on: input.openedOn,
    }),
  })
}

/** GET /api/incidentals/{purposeID} - one envelope with its collected and
 * disbursed totals. */
export function getIncidental(purposeId: number): Promise<IncidentalDetail> {
  return apiFetch<IncidentalDetail>(`/api/incidentals/${purposeId}`)
}

/**
 * POST /api/incidentals/{purposeID}/close - closes the envelope and squares
 * its balance to exactly zero (ADR-031). `rolled_amount` is signed:
 * positive rolled out to Kas Utama, negative covered a shortfall from Kas
 * Utama, zero means the envelope landed square and nothing posted. A second
 * close on an already-closed envelope is the server's named 409
 * `incidental_already_closed`.
 */
export function closeIncidental(
  purposeId: number,
  input: { accountId: number; closedOn: string; note: string | null },
): Promise<{ incidental: Incidental; rolledAmount: number }> {
  return apiFetch<{ incidental: Incidental; rolled_amount: number }>(`/api/incidentals/${purposeId}/close`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      account_id: input.accountId,
      closed_on: input.closedOn,
      // The server writes it to both legs of the roll, or to neither
      // (#210); an untouched field is null, never "".
      note: input.note,
    }),
  }).then((res) => ({ incidental: res.incidental, rolledAmount: res.rolled_amount }))
}

/**
 * POST /api/incidentals/{purposeID}/reopen - the deliberate, visible way
 * back from a closed envelope (ADR-031): closed_on returns to null so a
 * late entry has somewhere to post, through the ordinary record form with
 * no special path. No body - there is nothing to say beyond which envelope,
 * matching GET /api/incidentals/{purposeID}'s own no-body shape. Reopening
 * an envelope that is not closed is the server's named 409
 * `incidental_not_closed`.
 */
export function reopenIncidental(purposeId: number): Promise<Incidental> {
  return apiFetch<Incidental>(`/api/incidentals/${purposeId}/reopen`, { method: 'POST' })
}
