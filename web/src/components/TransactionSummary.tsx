import RecordSummary, { type SummaryItem } from '@/components/RecordSummary'
import { copy } from '@/copy/id'
import { formatIsoDate, formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { noteForDisplay } from '@/lib/transactions'
import type { Transaction } from '@/lib/transactions'

const text = copy.transactionDetail

/**
 * One transaction as the record a dialog acts on (ADR-046): who, which pos,
 * which month, where, when, the note, and the amount last. `purposeName` is
 * the pos as it reads now (the effective one after a correction); a caller
 * with no pos names to hand - a member's dues payments - leaves it out.
 */
export default function TransactionSummary({ transaction, purposeName }: { transaction: Transaction; purposeName?: string }) {
  const note = noteForDisplay(transaction)
  const items: SummaryItem[] = []
  if (transaction.member_name) items.push({ label: text.memberLabel, value: transaction.member_name })
  if (purposeName) items.push({ label: text.purposeLabel, value: purposeName })
  if (transaction.dues_period) items.push({ label: text.periodLabel, value: formatPeriod(transaction.dues_period) })
  if (transaction.account_name) items.push({ label: text.locationLabel, value: transaction.account_name })
  items.push({ label: text.dateLabel, value: formatIsoDate(transaction.occurred_on) })
  if (note) items.push({ label: text.noteLabel, value: note, block: true })
  items.push({
    label: text.amountLabel,
    value: <span className="tabular font-medium whitespace-nowrap">{formatIDR(transaction.amount)}</span>,
  })
  return <RecordSummary items={items} />
}
