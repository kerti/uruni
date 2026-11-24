// Typed call over apiFetch for POST /api/transactions, and the Transaction row
// shape every route that answers a posted row uses.

import { apiFetch } from '@/lib/api'
import { copy } from '@/copy/id'
import { formatShortDate } from '@/lib/dates'
import type { Direction, PurposeKind, TransactionKind, TransferKind } from '@/lib/enums'

/** The posted transaction row, also GET /api/transactions' row shape. The
 * fields after created_at carry the facts a system-created row's display
 * label needs; they are optional because a route that posts one row
 * (POST /api/transactions, /api/dues-payments, a settlement, a transfer leg)
 * never sets them - only the list route does. */
export interface Transaction {
  id: number
  account_id: number
  purpose_id: number
  direction: Direction
  amount: number
  occurred_on: string
  kind: TransactionKind
  member_id: number | null
  dues_period: string | null
  reimbursement_id: number | null
  transfer_id: number | null
  reverses_transaction_id: number | null
  note: string | null
  created_at: number

  // List-only fields - see the doc comment above.
  account_name?: string
  /** The kind of this row's purpose; tells an envelope contribution from the rest. */
  purpose_kind?: PurposeKind
  member_name?: string | null
  claim_note?: string | null
  /** The day the list places this row on (ADR-051): its occurred_on, except a
   * settled talangan's payout, which a flow dates by its claim's incurred_on
   * and the Lokasi filter (a cash view) by its own date. Month headers, the
   * row's date line and the cursor read it. */
  placed_on?: string
  /** The settled claim's incurred_on, only on a kind='reimbursement' row: with
   * occurred_on, the payout's other date. */
  claim_incurred_on?: string | null
  transfer_kind?: TransferKind | null
  transfer_from_name?: string | null
  transfer_to_name?: string | null
  /** Non-null only on a purpose correction's two legs (ADR-033); nil on every
   * other transfer leg, a roll's included, which is what tells the two apart:
   * both are kind='transfer' with transfer_kind='reclass_purpose'. */
  transfer_corrects_transaction_id?: number | null
  /** Why a purpose pair that corrects nothing moved (ADR-036): 'allocation'
   * is the treasurer's "Pindah pos", 'roll' an envelope closing. Null on a
   * correction, a location transfer, and a roll from before the column
   * existed - which reads as a roll. */
  transfer_reason?: 'roll' | 'allocation' | null
  /** The tag this row's money is under now: its purpose_id until a
   * correction moves it, then the latest correction's target. The list still
   * renders purpose_id - the ledger sums stored tags, so showing the
   * effective one would put the screen out of step with the balances
   * (ADR-033). Drives the "sudah diperbaiki" marker (effective !== stored)
   * and the correction dialog, which must build its pair from where the
   * money actually is. */
  effective_purpose_id?: number
  /** Whether a reversal points at this row (ADR-042): the server's flag, so a row
   * reversed on an unfetched page still reads reversed. */
  reversed?: boolean
  /** Whether the ledger would accept a reversal of this row now - the server's
   * answer, so the client keeps no copy of the eligibility rule. */
  reversible?: boolean
  /** The named-error code behind a false `reversible` (#809), from the same
   * server call; null when reversible. The detail explains a few of them. */
  reversal_refusal?: string | null
  /** Whether the ledger would accept a pos correction of this row to some
   * other pos (#821) - the server's answer, like `reversible`. */
  correctable?: boolean
  /** The named-error code behind a false `correctable`, from the same server
   * call; null when correctable. */
  correction_refusal?: string | null
  /** Whether a cek kas has counted this row, which picks the date a reversal
   * would take: its own day (false) or today (true). */
  counted?: boolean
  /** On a reversal: the date of the row it reverses, so a label can state it
   * though the original sits on another page. Null on every other row. */
  reverses_occurred_on?: string | null
  /** Every receipt attached to this row, oldest first. Optional so existing
   * test fixtures need not grow the field; TransactionList reads it as
   * `transaction.receipt_ids ?? []`. */
  receipt_ids?: number[]
  /** The dues payment this row stands for (ADR-048): `id` is the payment's
   * anchor row (its earliest month) and `amount` the sum of its months. Null
   * on every other row. List-only. */
  dues_payment_id?: number | null
  /** The months of a dues payment, oldest first; a dues-reversal group lists
   * the months it undoes (all reversed: false). [] on every non-dues row. */
  periods?: DuesPeriodPart[]
  /** How much of a dues payment is undone: none, some months, or all. */
  reversal_state?: 'none' | 'partial' | 'all'
}

/** One month of a dues payment on the list's wire. */
export interface DuesPeriodPart {
  dues_period: string
  amount: number
  reversed: boolean
}

/** POST /api/transactions's body (transactionRequest), camelCase on this side
 * of the wire boundary. */
export interface CreateTransactionInput {
  accountId: number
  purposeId: number
  direction: Direction
  amount: number
  occurredOn: string
  note: string | null
  /** Who a contribution into an open envelope is from (ADR-034) - Catat's
   * "Dari siapa? (opsional)". Omitted or null for every other posting. */
  memberId?: number | null
}

/**
 * POST /api/transactions - one ordinary entry (kind='normal').
 * is_adjustment is always false here: the reconcile flow is the only caller
 * that posts kind='adjustment', through its own request.
 */
export function createTransaction(input: CreateTransactionInput): Promise<Transaction> {
  return apiFetch<Transaction>('/api/transactions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      account_id: input.accountId,
      purpose_id: input.purposeId,
      direction: input.direction,
      amount: input.amount,
      occurred_on: input.occurredOn,
      note: input.note,
      is_adjustment: false,
      member_id: input.memberId ?? null,
    }),
  })
}

/** GET /api/transactions's optional query parameters (ADR-032 "Lists: paging
 * and search"). cursor is a previous page's nextCursor - omit it for the
 * first page. memberId/duesPeriod are exact-match filters for
 * Dues/MemberPayments.tsx alone, not a Riwayat UI filter (ADR-032 holds
 * filters to M7) - there is deliberately no copy or UI surface naming them.
 *
 * purposeId does have a UI surface: ADR-032 makes Riwayat -> Transaksi
 * filtered to a purpose the only route to a closed envelope, so
 * History/Transactions.tsx renders it from `?purpose=<id>` as a filter it
 * can clear, but offers no chooser. All of these narrow the same list `q`
 * searches. */
export interface ListTransactionsInput {
  cursor?: string
  q?: string
  memberId?: number
  duesPeriod?: string
  purposeId?: number
  /** One location (#784): a transfer shows when either leg is on it. */
  accountId?: number
  /** "YYYY-MM" - the rows dated in that month. */
  month?: string
  direction?: Direction
}

/** One page of GET /api/transactions, camelCase on this side of the wire
 * boundary. nextCursor is null once there is no further page. */
export interface TransactionsPage {
  transactions: Transaction[]
  nextCursor: string | null
}

/**
 * GET /api/transactions (keyset-paged, searchable): newest-first, 25 rows a
 * page. Home and Reconcile take their first five rows off the first page,
 * MemberPayments passes memberId/duesPeriod instead of filtering
 * client-side, and History/Transactions follows nextCursor for "load more".
 */
export function listTransactions(input: ListTransactionsInput = {}): Promise<TransactionsPage> {
  const params = new URLSearchParams()
  if (input.cursor) params.set('cursor', input.cursor)
  if (input.q) params.set('q', input.q)
  if (input.memberId !== undefined) params.set('member_id', String(input.memberId))
  if (input.duesPeriod) params.set('dues_period', input.duesPeriod)
  if (input.purposeId !== undefined) params.set('purpose_id', String(input.purposeId))
  if (input.accountId !== undefined) params.set('account_id', String(input.accountId))
  if (input.month) params.set('month', input.month)
  if (input.direction) params.set('direction', input.direction)
  const query = params.toString()

  return apiFetch<{ transactions: Transaction[]; next_cursor: string | null }>(`/api/transactions${query ? `?${query}` : ''}`).then(
    (page) => ({ transactions: page.transactions, nextCursor: page.next_cursor }),
  )
}

/** GET /api/transactions/summary's body (#751): the public report's walk
 * for one month under Riwayat's filters (ADR-038). `full` says the balances
 * apply - the whole fund, one pos, one location (#784) or a pos in a
 * location; under a member or direction filter only
 * `in` and `out` mean anything, and the dates are null. Every figure is the
 * server's: nothing here is summed from rows. */
export interface TransactionSummary {
  full: boolean
  start_on: string | null
  end_on: string | null
  start: number
  openings: number
  in: number
  out: number
  adjustments: number
  moved: number
  /** The two talangan lines (ADR-051), each unsigned on the wire: payouts
   * counted in this month's out but paid after it (added in the walk), and
   * payouts paid in it that an earlier month counted (subtracted). Zero under a
   * location filter and where `full` is false. */
  paid_later: number
  paid_earlier: number
  end: number
}

/** GET /api/transactions/summary - the month summary for Riwayat's filters;
 * `month` is required. */
export function getTransactionSummary(
  input: Pick<ListTransactionsInput, 'purposeId' | 'accountId' | 'memberId' | 'direction'> & { month: string },
): Promise<TransactionSummary> {
  const params = new URLSearchParams({ month: input.month })
  if (input.memberId !== undefined) params.set('member_id', String(input.memberId))
  if (input.purposeId !== undefined) params.set('purpose_id', String(input.purposeId))
  if (input.accountId !== undefined) params.set('account_id', String(input.accountId))
  if (input.direction) params.set('direction', input.direction)
  return apiFetch<TransactionSummary>(`/api/transactions/summary?${params.toString()}`)
}

/** The day the list places a row on (ADR-051): placed_on, or occurred_on on a
 * row from a route that does not place (a write's response). */
export function placedOn(transaction: Transaction): string {
  return transaction.placed_on ?? transaction.occurred_on
}

/** The day a settled talangan was fronted, when the fund paid it on another
 * day (ADR-051); null for a same-day payout and for every row that is no
 * payout. The one test behind the row's second line and the detail's two
 * dates (#858). */
export function frontedOnAnotherDay(transaction: Transaction): string | null {
  const fronted = transaction.claim_incurred_on
  if (transaction.kind !== 'reimbursement' || !fronted || fronted === transaction.occurred_on) return null
  return fronted
}

/**
 * A settled talangan's second line (ADR-051): where else the row reads. Placed
 * on the day it was fronted, it says when and from where it was paid; placed on
 * the day it was paid (the Lokasi filter), when it was fronted. Null for a claim
 * fronted and paid on one day, and for every row that is no payout. Twin of the
 * report's payoutNote, which never names the location (ADR-035).
 */
export function payoutNote(transaction: Transaction): string | null {
  const fronted = frontedOnAnotherDay(transaction)
  if (!fronted) return null
  const placed = placedOn(transaction)
  if (placed === fronted)
    return copy.rowLabels.settlement.paidOn(formatShortDate(transaction.occurred_on, placed), transaction.account_name ?? '')
  return copy.rowLabels.settlement.frontedOn(formatShortDate(fronted, placed))
}

/**
 * The text a row's note line shows. A settlement's (kind='reimbursement')
 * Note is always nil server-side - nothing generated is ever stored - so its
 * "note" is the settled claim's note; every other row shows what she typed.
 */
export function noteForDisplay(transaction: Transaction): string | null {
  if (transaction.kind === 'reimbursement') {
    return transaction.claim_note ?? null
  }
  return transaction.note
}

/**
 * POST /api/transactions/{id}/reversal - undo a dues payment or a normal
 * transaction recorded in error (PRD section 7.3, ADR-042). {id} is the row
 * being reversed.
 *
 * Deliberately narrow: only a note is sent. The ledger copies amount, location,
 * purpose (the effective one), member and period from the original and picks the date
 * itself: the original's own day, or today once a cek kas has counted it. A new
 * linked entry is posted (CLAUDE.md rule 3).
 *
 * `note` holds only what the treasurer typed; blank sends null, never a generated default.
 */
export function reverseTransaction(transactionId: number, note: string | null): Promise<Transaction> {
  return apiFetch<Transaction>(`/api/transactions/${transactionId}/reversal`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note }),
  })
}

/**
 * POST /api/transactions/{id}/purpose-correction (ADR-033) - moves a posted
 * row's pos to the tag it should always have had, by posting a value-neutral
 * reclass_purpose pair on the row's account and date.
 *
 * One field, deliberately: the amount, account and date are read off the row
 * server-side and cannot be sent, which keeps this a correction of that row
 * rather than an edit of an immutable entry. The server decides the pair's
 * legs from the row's EFFECTIVE pos, so correcting an already-corrected row
 * moves money out of the tag that actually holds it.
 *
 * Answers the transfer row. Every refusal is a named 409 the caller maps
 * through copy.common.errors (purpose_correction_*).
 */
export function correctPurpose(transactionId: number, purposeId: number): Promise<{ id: number; kind: string; created_at: number }> {
  return apiFetch(`/api/transactions/${transactionId}/purpose-correction`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ purpose_id: purposeId }),
  })
}

/**
 * Whether this row's pos may be corrected at all (ADR-033's eligibility
 * list): the server's `correctable`, computed by the same rule the
 * correction write applies (#821), so the door is never offered on a row the
 * write would refuse. A row the server did not compute it for is not.
 */
export function canCorrectPurpose(transaction: Transaction): boolean {
  return transaction.correctable === true
}
