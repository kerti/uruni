import { ArrowLeftRight, CalendarCheck, Flag, HandHelping, Mail, Scale, Undo2 } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

import { copy } from '@/copy/id'
import { formatPeriod } from '@/lib/dates'
import type { Transaction } from '@/lib/transactions'

/**
 * The kind -> icon/copy map for a row the app itself created (#257,
 * TransactionList.tsx's own row layout). One place, so a new labelled kind
 * (or a change of icon) never has to be found twice. Every case reads
 * facts GET /api/transactions already carries on the row - see
 * internal/http/transactions.go's toTransactionsPageResponse - never a
 * second request.
 *
 * srWord is the kind word a screen reader needs because the icon is
 * decorative. It is null where the visible text already starts with that
 * word (Pembatalan, Saldo awal, Penyesuaian), so it is never read twice.
 *
 * Order inside 'adjustment' matters: a dues reversal and a reconciliation
 * fix are the schema's two disjoint shapes of that one kind (a reversal
 * always carries member_id+dues_period+reverses_transaction_id; a fix is
 * named by reconciliation_line.adjustment_transaction_id instead), so
 * checking reverses_transaction_id first is exact, never a guess between
 * the two.
 */
function rowLabelFor(transaction: Transaction): { Icon: LucideIcon; srWord: string | null; text: string } | null {
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
        return {
          Icon: Mail,
          srWord: copy.rowLabels.transferPurpose.kind,
          text: copy.rowLabels.transferPurpose.text(transaction.transfer_from_name ?? '', transaction.transfer_to_name ?? ''),
        }
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
        return {
          Icon: Undo2,
          srWord: null,
          text: copy.rowLabels.duesReversal.text(formatPeriod(transaction.dues_period ?? ''), transaction.member_name ?? ''),
        }
      }
      if (transaction.is_reconciliation_fix) {
        return {
          Icon: Scale,
          srWord: null,
          text: copy.rowLabels.reconciliationFix.text(transaction.account_name ?? ''),
        }
      }
      // An ordinary adjustment (ADR-024) - her own row, her own note.
      return null
    default:
      // 'normal', including a reconciliation entry_added fix - the entry
      // she forgot, entered by her, with her own purpose and note.
      return null
  }
}

/**
 * One row's label line: a small, muted, decorative icon (aria-hidden) then
 * the visible text, with the kind word added for screen readers only where
 * the visible text does not already say it. Renders nothing for a row she
 * recorded herself.
 */
export default function TransactionRowLabel({ transaction }: { transaction: Transaction }) {
  const label = rowLabelFor(transaction)
  if (!label) return null

  const { Icon, srWord, text } = label
  return (
    <span className="flex min-w-0 items-center gap-1 text-sm text-muted-foreground">
      <Icon aria-hidden="true" className="size-4 shrink-0" />
      {srWord && <span className="sr-only">{srWord}</span>}
      <span className="truncate">{text}</span>
    </span>
  )
}
