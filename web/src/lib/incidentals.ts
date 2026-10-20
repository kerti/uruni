// Typed calls over apiFetch for the incidental-envelope routes
// (internal/http/incidentals.go). Contributions and disbursements are not
// their own route: they are ordinary POST /api/transactions calls tagged to
// the envelope's purpose (lib/transactions.ts).

import { apiFetch } from '@/lib/api'
import type { Member } from '@/lib/setup'

/** One incidental envelope (internal/http/incidentals.go). `target_amount` and
 * `closed_on` are nullable: a target is optional at opening (PRD section
 * 7.5), and an envelope stays open until deliberately closed.
 * `minimum_per_member` (ADR-034) is one figure every expected member is asked
 * to give, nullable the same way. */
export interface Incidental {
  purpose_id: number
  occasion: string
  target_amount: number | null
  opened_on: string
  closed_on: string | null
  created_at: number
  minimum_per_member: number | null
}

/** One member an envelope is for (ADR-034) - id and name together. */
export interface IncidentalRecipient {
  member_id: number
  member_name: string
}

/** An envelope plus the totals PRD section 7.5 shows for it. Both totals are
 * server-computed; nothing here re-derives them from a transaction list.
 * `recipients` rides only on this detail response, not the plain list
 * (ADR-034). */
export interface IncidentalDetail extends Incidental {
  collected_amount: number
  disbursed_amount: number
  /** What the envelope holds now (the purpose's balance, rolls included) -
   * not collected minus disbursed once a closed envelope is reopened. */
  balance_amount: number
  recipients: IncidentalRecipient[]
}

/**
 * GET /api/incidentals, optionally ?open=true for only the envelopes still
 * collecting.
 */
export function listIncidentals(openOnly = false): Promise<Incidental[]> {
  const query = openOnly ? '?open=true' : ''
  return apiFetch<Incidental[]>(`/api/incidentals${query}`)
}

/**
 * POST /api/incidentals - opens a new envelope for an occasion. There is no
 * separate name field: occasion doubles as the purpose's name.
 */
export function openIncidental(input: {
  occasion: string
  targetAmount: number | null
  openedOn: string
  minimumPerMember?: number | null
  recipientMemberIds?: number[]
}): Promise<Incidental> {
  return apiFetch<Incidental>('/api/incidentals', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      occasion: input.occasion,
      target_amount: input.targetAmount,
      opened_on: input.openedOn,
      minimum_per_member: input.minimumPerMember ?? null,
      recipient_member_ids: input.recipientMemberIds ?? [],
    }),
  })
}

/**
 * PATCH /api/incidentals/{purposeID} - the envelope's target, minimum and
 * recipients (ADR-034), the facets the occasion-rename route (renamePurpose)
 * does not reach. Every field fully replaces its facet, never an add/remove
 * delta: a nullable target or minimum clears with null, and
 * recipientMemberIds (empty or not) replaces the whole set - so a caller
 * always sends all three, or it clears what it left out.
 */
export function updateIncidentalParticipation(
  purposeId: number,
  input: { targetAmount: number | null; minimumPerMember: number | null; recipientMemberIds: number[] },
): Promise<Incidental> {
  return apiFetch<Incidental>(`/api/incidentals/${purposeId}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      target_amount: input.targetAmount,
      minimum_per_member: input.minimumPerMember,
      recipient_member_ids: input.recipientMemberIds,
    }),
  })
}

/** GET /api/incidentals/{purposeID} - one envelope with its totals. */
export function getIncidental(purposeId: number): Promise<IncidentalDetail> {
  return apiFetch<IncidentalDetail>(`/api/incidentals/${purposeId}`)
}

/**
 * POST /api/incidentals/{purposeID}/close - closes the envelope and squares
 * its balance to exactly zero (ADR-031). `rolled_amount` is signed: positive
 * rolled out to Kas Utama, negative covered a shortfall from Kas Utama, zero
 * means the envelope landed square and nothing posted. Closing an
 * already-closed envelope is the server's named 409
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
      // The server writes it to both legs of the roll, or to neither; an
      // untouched field is null, never "".
      note: input.note,
    }),
  }).then((res) => ({ incidental: res.incidental, rolledAmount: res.rolled_amount }))
}

/**
 * POST /api/incidentals/{purposeID}/reopen - the deliberate, visible way back
 * from a closed envelope (ADR-031): closed_on returns to null so a late entry
 * has somewhere to post, through the ordinary record form. No body. Reopening
 * an envelope that is not closed is the server's named 409
 * `incidental_not_closed`.
 */
export function reopenIncidental(purposeId: number): Promise<Incidental> {
  return apiFetch<Incidental>(`/api/incidentals/${purposeId}/reopen`, { method: 'POST' })
}

/** The schema's three participation states (ADR-034, internal/ledger's
 * MemberParticipationState): English on the wire, Indonesian only on screen
 * (ADR-014) - see copy.incidentals.participation.states. */
export type ParticipationStateKind = 'sudah' | 'belum' | 'kurang'

/** Who a participation row is about: a member without the roster's tier, rate
 * and arrears, which an envelope never reads (ADR-034, ADR-040). */
export type Participant = Pick<Member, 'id' | 'name' | 'joined_on' | 'inactive_on' | 'created_at'>

/** One expected member's row on GET /api/incidentals/{purposeID}/participation
 * (ADR-034): who was expected, how much they have given, and the state that
 * answers from it. */
export interface ParticipationState {
  member: Participant
  contributed_amount: number
  state: ParticipationStateKind
}

/** One row of the "Sumbangan lain" list (ADR-034): a contribution from
 * someone the envelope did not expect. No state - only an amount. */
export interface UnexpectedContribution {
  member: Participant
  contributed_amount: number
}

/** GET /api/incidentals/{purposeID}/participation's whole body (ADR-034): the
 * participation table, derived from the ledger at read time - never stored. */
export interface IncidentalParticipation {
  expected: ParticipationState[]
  unexpected: UnexpectedContribution[]
}

/**
 * GET /api/incidentals/{purposeID}/participation - PRD section 7.5's "who has
 * contributed and how much" (ADR-034). Fetched alongside the detail, not
 * folded into it: a derived table costs nothing extra only where it is asked
 * for.
 */
export function getIncidentalParticipation(purposeId: number): Promise<IncidentalParticipation> {
  return apiFetch<IncidentalParticipation>(`/api/incidentals/${purposeId}/participation`)
}
