import { ArrowDownLeft, ArrowUpRight } from 'lucide-react'

import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import type { Transaction } from '@/lib/transactions'

/**
 * One transaction row's markup (M6.23) - extracted out of Home.tsx so
 * Riwayat's Transaksi tab (Transactions.tsx) can render the exact same rows
 * instead of a second copy of this list, per the issue's own instruction.
 * Home decides which slice of the list to pass in (its own recent-five) and
 * Transactions passes the whole thing; this component has no opinion on
 * count or order, only on how one row reads.
 */
export default function TransactionList({
  transactions,
  purposeNames,
  emptyMessage,
}: {
  transactions: Transaction[]
  /** transaction.purpose_id -> its name - a row carries only the id, and
   * GET /api/balances already answers with every purpose the fund has,
   * names included (see Home.tsx's own comment on this lookup). */
  purposeNames: Map<number, string>
  emptyMessage: string
}) {
  if (transactions.length === 0) {
    return <p className="text-muted-foreground">{emptyMessage}</p>
  }

  return (
    <ul className="flex flex-col gap-2">
      {transactions.map((transaction) => (
        <li key={transaction.id} className="flex items-start justify-between gap-3 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
          <span className="flex min-w-0 items-start gap-2">
            {transaction.direction === 'in' ? (
              <ArrowDownLeft aria-hidden="true" className="mt-0.5 shrink-0 text-success" />
            ) : (
              <ArrowUpRight aria-hidden="true" className="mt-0.5 shrink-0 text-attention" />
            )}
            <span className="flex min-w-0 flex-col">
              {/* The purpose tag is what an entry *was*; the note is
                  whatever she typed to remember it by, and is optional
                  (PRD section 6). Date drops to the second line so the row
                  still answers "what is this?" at a glance. */}
              <span className="truncate">{purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown}</span>
              {transaction.note && <span className="truncate text-sm text-muted-foreground">{transaction.note}</span>}
              <span className="text-sm text-muted-foreground">{formatIsoDate(transaction.occurred_on)}</span>
            </span>
          </span>
          <span className="tabular shrink-0 font-medium">{formatIDR(transaction.amount)}</span>
        </li>
      ))}
    </ul>
  )
}
