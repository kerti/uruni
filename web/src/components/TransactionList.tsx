import { ArrowDownLeft, ArrowUpRight, Tags } from 'lucide-react'

import TransactionRowLabel from '@/components/TransactionRowLabel'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { canCorrectPurpose, noteForDisplay } from '@/lib/transactions'
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
  onCorrectPurpose,
}: {
  transactions: Transaction[]
  /** transaction.purpose_id -> its name - a row carries only the id, and
   * GET /api/balances already answers with every purpose the fund has,
   * names included (see Home.tsx's own comment on this lookup). */
  purposeNames: Map<number, string>
  emptyMessage: string
  /** Opens the correction dialog for one row (#276, ADR-033). Optional,
   * and that is the whole of how Beranda stays inert: Riwayat's Transaksi
   * tab passes it, Home does not, so the same row markup is a control in
   * the list she came to work in and plain text in the summary she came to
   * read. A row this handler is passed for may still be ineligible -
   * canCorrectPurpose decides that per row, so an ineligible one renders
   * its peruntukan as text rather than a tap that would only be refused. */
  onCorrectPurpose?: (transaction: Transaction) => void
}) {
  if (transactions.length === 0) {
    return <p className="text-muted-foreground">{emptyMessage}</p>
  }

  return (
    <ul className="flex flex-col gap-2">
      {transactions.map((transaction) => {
        const note = noteForDisplay(transaction)
        return (
          <li key={transaction.id} className="flex items-start justify-between gap-3 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
            <span className="flex min-w-0 items-start gap-2">
              {transaction.direction === 'in' ? (
                <ArrowDownLeft aria-hidden="true" className="mt-0.5 shrink-0 text-success" />
              ) : (
                <ArrowUpRight aria-hidden="true" className="mt-0.5 shrink-0 text-attention" />
              )}
              <span className="flex min-w-0 flex-col">
                {/* The purpose tag is what an entry *was*; a row the app
                    created explains itself on the next line (#257 - a
                    label, built from the row's own facts, never stored
                    text); her own note, when she typed one, comes after
                    that (for a settlement, the settled claim's own note -
                    see noteForDisplay). Date is always last, so the row
                    still answers "what is this?" at a glance. */}
                <PurposeLine
                  transaction={transaction}
                  purposeNames={purposeNames}
                  onCorrectPurpose={onCorrectPurpose}
                />
                <TransactionRowLabel transaction={transaction} />
                {note && <span className="truncate text-sm text-muted-foreground">{note}</span>}
                <span className="text-sm text-muted-foreground">{formatIsoDate(transaction.occurred_on)}</span>
              </span>
            </span>
            <span className="tabular shrink-0 font-medium">{formatIDR(transaction.amount)}</span>
          </li>
        )
      })}
    </ul>
  )
}

/**
 * The row's first line: what this entry was for, and - on a row whose
 * peruntukan may still be corrected - the control that corrects it
 * (#276, ADR-033).
 *
 * The peruntukan IS the control, rather than a button beside it. The whole
 * row is not tappable because eligibility varies, so a tappable row would
 * be a dead tap on a dues row and a live one on the expense beneath it with
 * nothing visible to tell them apart; and a button of its own would either
 * break the amount column's right alignment - the column she scans down -
 * or add 44px to every eligible row on the app's densest unbounded list.
 * Tapping the noun that is wrong is also simply what she means.
 *
 * `py-1.5 -my-1.5` is the 44px target without changing the row's height:
 * the padding grows the hit area and the negative margin gives the space
 * back to the layout. A dotted underline is what makes it read as tappable
 * at all, since text that taps and text that does not are otherwise
 * identical.
 *
 * The stored peruntukan is what renders, never the effective one, even
 * after a correction has moved the money elsewhere: the ledger sums stored
 * tags, so a row showing its effective tag would leave the screen out of
 * step with the balances (ADR-033). The Tags glyph is what says a
 * correction exists - the same glyph its two legs carry a few rows down,
 * which is what ties them together without either saying so - and it is
 * hidden from screen readers, with the sentence beside it in sr-only text,
 * the split #257 already uses for every other icon in this list.
 */
function PurposeLine({
  transaction,
  purposeNames,
  onCorrectPurpose,
}: {
  transaction: Transaction
  purposeNames: Map<number, string>
  onCorrectPurpose?: (transaction: Transaction) => void
}) {
  const name = purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown
  const corrected =
    transaction.effective_purpose_id !== undefined && transaction.effective_purpose_id !== transaction.purpose_id

  const marker = corrected ? (
    <>
      <Tags aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <span className="sr-only">{copy.purposeCorrection.corrected}</span>
    </>
  ) : null

  if (!onCorrectPurpose || !canCorrectPurpose(transaction)) {
    return (
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate">{name}</span>
        {marker}
      </span>
    )
  }

  return (
    <button
      type="button"
      aria-label={copy.purposeCorrection.controlAria(name)}
      onClick={() => onCorrectPurpose(transaction)}
      className="-my-1.5 flex min-w-0 items-center gap-1.5 self-start py-1.5 text-left"
    >
      <span className="truncate underline decoration-dotted underline-offset-4">{name}</span>
      {marker}
    </button>
  )
}
