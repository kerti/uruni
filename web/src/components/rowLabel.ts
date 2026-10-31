import {
  ArrowUUpLeft,
  ArrowsLeftRight,
  CalendarCheck,
  Envelope,
  Flag,
  HandCoins,
  HandHeart,
  PencilLine,
  Shuffle,
  SlidersHorizontal,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'

import { copy } from '@/copy/id'
import { formatIsoDate, formatPeriod } from '@/lib/dates'
import type { Transaction } from '@/lib/transactions'

/**
 * The kind -> icon/copy map for a row the app itself created (see
 * TransactionList.tsx's row layout). One place, so a new labelled kind or a
 * change of icon is found once. Every case reads facts GET /api/transactions
 * already carries on the row - never a second request.
 *
 * srWord is the kind word a screen reader needs because the icon is
 * decorative. It is null where the visible text already starts with that word
 * (Pembatalan, Saldo awal, Penyesuaian), so it is never read twice.
 *
 * Order inside 'adjustment' matters: a reversal and a reconciliation fix are
 * disjoint shapes of that kind (a reversal always carries
 * reverses_transaction_id; a fix is named by
 * reconciliation_line.adjustment_transaction_id), so checking
 * reverses_transaction_id first is exact. Anything that is not a reversal - a
 * cek kas fix or a standalone adjustment (ADR-024) - is Penyesuaian, the
 * report's word for it (ADR-038).
 */
export function rowLabelFor(transaction: Transaction): { Icon: Icon; srWord: string | null; text: string; wraps?: boolean } | null {
  switch (transaction.kind) {
    case 'dues':
      return {
        Icon: CalendarCheck,
        srWord: copy.rowLabels.dues.kind,
        text: copy.rowLabels.dues.text(formatPeriod(transaction.dues_period ?? ''), transaction.member_name ?? ''),
      }
    case 'opening':
      return {
        Icon: Flag,
        srWord: null,
        text: copy.rowLabels.opening.text(transaction.account_name ?? ''),
      }
    case 'reimbursement':
      return {
        Icon: HandHeart,
        srWord: copy.rowLabels.settlement.kind,
        text: copy.rowLabels.settlement.text(transaction.member_name ?? ''),
      }
    case 'transfer':
      if (transaction.transfer_kind === 'reclass_purpose') {
        // Three shapes share this wire shape (ADR-033, ADR-036). A purpose
        // correction is the one that sets corrects_transaction_id. Of the
        // rest, transfer_reason tells the treasurer's "Pindah pos"
        // ('allocation') from an envelope closing ('roll'), and a null reason
        // is a roll too: the row predates the column, and closing was then the
        // only thing that could post an uncorrected reclass pair.
        const from = transaction.transfer_from_name ?? ''
        const to = transaction.transfer_to_name ?? ''
        if (transaction.transfer_corrects_transaction_id != null) {
          const label = copy.rowLabels.transferPurposeCorrection
          return { Icon: PencilLine, srWord: label.kind, text: label.text(from, to) }
        }
        if (transaction.transfer_reason === 'allocation') {
          const label = copy.rowLabels.transferPurposeMove
          return { Icon: Shuffle, srWord: label.kind, text: label.text(from, to) }
        }
        const label = copy.rowLabels.transferPurpose
        return { Icon: Envelope, srWord: label.kind, text: label.text(from, to) }
      }
      if (transaction.transfer_kind === 'between_accounts') {
        return {
          Icon: ArrowsLeftRight,
          srWord: copy.rowLabels.transferLocation.kind,
          text: copy.rowLabels.transferLocation.text(transaction.transfer_from_name ?? '', transaction.transfer_to_name ?? ''),
        }
      }
      return null
    case 'adjustment':
      if (transaction.reverses_transaction_id !== null) {
        // A reversal copies its original's dues_period (ADR-029, ADR-034): a
        // dues reversal carries one, a contribution reversal never does. A
        // reversal of a row naming nobody states the original's date instead
        // (ADR-042).
        let reversalText: string
        if (transaction.dues_period !== null) {
          reversalText = copy.rowLabels.duesReversal.text(formatPeriod(transaction.dues_period), transaction.member_name ?? '')
        } else if (transaction.member_name) {
          reversalText = copy.rowLabels.contributionReversal.text(transaction.member_name)
        } else {
          reversalText = copy.rowLabels.unnamedReversal.text(
            transaction.reverses_occurred_on ? formatIsoDate(transaction.reverses_occurred_on) : '',
          )
        }
        // Wraps even in a list: an unnamed reversal ends in the date that
        // says which row it undoes, the part truncation would cut at 390px.
        return { Icon: ArrowUUpLeft, srWord: null, text: reversalText, wraps: true }
      }
      // Every other adjustment - a cek kas fix, or one she posted herself
      // (ADR-024) - is named for the line of the month's walk it sums into
      // (ADR-038), the word the report uses. Her note, if any, renders on its
      // own line.
      return {
        Icon: SlidersHorizontal,
        srWord: null,
        text: copy.rowLabels.adjustment.text(transaction.account_name ?? ''),
      }
    case 'normal':
      // A named contribution (ADR-034) says who gave, as a dues row does; any
      // other 'normal' row - including a reconciliation entry_added fix - is
      // hers, with her purpose and note.
      if (transaction.member_id !== null) {
        return {
          Icon: HandCoins,
          srWord: copy.rowLabels.contribution.kind,
          text: copy.rowLabels.contribution.text(transaction.member_name ?? ''),
        }
      }
      // A contribution nobody gave a name for: money in to an envelope.
      if (transaction.direction === 'in' && transaction.purpose_kind === 'incidental') {
        return { Icon: HandCoins, srWord: copy.rowLabels.contribution.kind, text: copy.rowLabels.contribution.anonymous }
      }
      return null
    default:
      return null
  }
}

/**
 * A transfer is one listed row for two ledger rows (#738), so the row stands
 * for a movement, not for the one leg the list happened to pick: its own
 * direction, account and pos say nothing about the movement. Everything that
 * draws a row therefore asks this first, and draws a transfer neutrally - no
 * in/out arrow, no pos of the leg - from the label's from -> to names, which
 * are the same whichever leg it is.
 */
export function isTransfer(transaction: Transaction): boolean {
  return transaction.kind === 'transfer'
}

/** A pair between two pos (Pindah pos, Tutup amplop, Perbaikan pos), whose legs
 * are tagged to different pos: neither leg's pos is the row's. A location
 * transfer's legs share one pos, which is the row's. */
export function isPurposePair(transaction: Transaction): boolean {
  return isTransfer(transaction) && transaction.transfer_kind === 'reclass_purpose'
}

/**
 * A transfer's kind word - Pindah lokasi, Pindah pos, Tutup amplop, Perbaikan
 * pos - which every transfer row and detail shows as its first line (#738),
 * the from -> to under it. null for any row that is not a labelled transfer.
 */
export function transferKind(transaction: Transaction): string | null {
  if (!isTransfer(transaction)) return null
  return rowLabelFor(transaction)?.srWord ?? null
}

/**
 * A transfer's label as one plain string, for what has no room for the icon
 * and the sr-only word: a card's accessible name, or a bare row's headline.
 * null for any row that is not a labelled transfer.
 */
export function transferHeadline(transaction: Transaction): string | null {
  if (!isTransfer(transaction)) return null
  const label = rowLabelFor(transaction)
  if (!label) return null
  return label.srWord ? `${label.srWord}: ${label.text}` : label.text
}
