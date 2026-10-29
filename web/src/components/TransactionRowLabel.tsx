import { ArrowLeftRight, CalendarCheck, Flag, HandCoins, HandHelping, Mail, Scale, Shuffle, Tags, Undo2 } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

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
function rowLabelFor(transaction: Transaction): { Icon: LucideIcon; srWord: string | null; text: string; wraps?: boolean } | null {
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
        Icon: HandHelping,
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
          return { Icon: Tags, srWord: label.kind, text: label.text(from, to) }
        }
        if (transaction.transfer_reason === 'allocation') {
          return { Icon: Shuffle, srWord: null, text: copy.rowLabels.transferPurposeMove.text(from, to) }
        }
        const label = copy.rowLabels.transferPurpose
        return { Icon: Mail, srWord: label.kind, text: label.text(from, to) }
      }
      if (transaction.transfer_kind === 'between_accounts') {
        return {
          Icon: ArrowLeftRight,
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
        return { Icon: Undo2, srWord: null, text: reversalText, wraps: true }
      }
      // Every other adjustment - a cek kas fix, or one she posted herself
      // (ADR-024) - is named for the line of the month's walk it sums into
      // (ADR-038), the word the report uses. Her note, if any, renders on its
      // own line.
      return {
        Icon: Scale,
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
 * One row's label line: a small, muted, decorative icon (aria-hidden) then
 * the visible text, with the kind word added for screen readers only where
 * the visible text does not say it. Renders nothing for a row she recorded
 * herself.
 */
export default function TransactionRowLabel({
  transaction,
  wrap = false,
}: {
  transaction: Transaction
  /** Wraps instead of truncating - the entry detail, where the whole label is
   * the point. */
  wrap?: boolean
}) {
  const label = rowLabelFor(transaction)
  if (!label) return null

  const { Icon, srWord, text } = label
  const wraps = wrap || label.wraps === true
  return (
    <span className={`flex min-w-0 gap-1 text-sm text-muted-foreground ${wraps ? 'items-start' : 'items-center'}`}>
      <Icon aria-hidden="true" className={`size-4 shrink-0 ${wraps ? 'mt-0.5' : ''}`} />
      {srWord && <span className="sr-only">{srWord}</span>}
      <span className={wraps ? 'break-words' : 'truncate'}>{text}</span>
    </span>
  )
}
