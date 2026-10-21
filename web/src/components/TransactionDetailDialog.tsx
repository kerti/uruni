import { ArrowDownLeft, ArrowUpRight, Undo2 } from 'lucide-react'
import type { ReactNode } from 'react'

import TransactionRowLabel from '@/components/TransactionRowLabel'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'
import { formatIsoDate, formatUnixSecondsWithTime } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { receiptUrl } from '@/lib/receipts'
import { noteForDisplay } from '@/lib/transactions'
import type { Transaction } from '@/lib/transactions'

const text = copy.transactionDetail

/**
 * One ledger entry read in full: everything the row already carries, nothing
 * truncated. Read-only on purpose - the row's controls (pos correction,
 * photos) stay on the row, and the one button it has, Batalkan transaksi,
 * posts nothing: it hands off to the form under the row. So this dialog never
 * becomes a second place to change money.
 *
 * No fetch: the Transaction the list holds is all that is shown. The stored
 * pos leads, as on the row (ADR-033); a corrected row adds the tag the money
 * is under now, with the correction dialog's "Pos saat ini" wording so the
 * two never disagree.
 */
export default function TransactionDetailDialog({
  transaction,
  purposeNames,
  onClose,
  onReverse,
  onCloseAutoFocus,
}: {
  transaction: Transaction | null
  purposeNames: Map<number, string>
  onClose: () => void
  /** Radix's own hook: a caller handing off to something on the page moves
   * focus there instead of the default return to the trigger. */
  onCloseAutoFocus?: (event: Event) => void
  /** Hands a reversible row off to the form under it (ADR-042). Optional, and
   * that is how Beranda stays inert: only Riwayat's Transaksi passes it. */
  onReverse?: (transaction: Transaction) => void
}) {
  return (
    <Dialog
      open={transaction !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close} onCloseAutoFocus={onCloseAutoFocus}>
        <DialogHeader>
          <DialogTitle>{text.heading}</DialogTitle>
        </DialogHeader>
        {transaction && <Detail transaction={transaction} purposeNames={purposeNames} onReverse={onReverse} />}
      </DialogContent>
    </Dialog>
  )
}

function Detail({
  transaction,
  purposeNames,
  onReverse,
}: {
  transaction: Transaction
  purposeNames: Map<number, string>
  onReverse?: (transaction: Transaction) => void
}) {
  const incoming = transaction.direction === 'in'
  const purposeName = purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown
  const corrected = transaction.effective_purpose_id !== undefined && transaction.effective_purpose_id !== transaction.purpose_id
  const note = noteForDisplay(transaction)
  const receiptIds = transaction.receipt_ids ?? []

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <span className="flex items-center gap-2 text-sm text-muted-foreground">
          {incoming ? (
            <ArrowDownLeft aria-hidden="true" className="size-4 text-success" />
          ) : (
            <ArrowUpRight aria-hidden="true" className="size-4 text-attention" />
          )}
          {incoming ? text.directionIn : text.directionOut}
        </span>
        <span className="tabular text-2xl font-semibold">{formatIDR(transaction.amount)}</span>
        <TransactionRowLabel transaction={transaction} wrap />
        {transaction.reversed && <span className="text-sm text-muted-foreground">{copy.dues.history.reversedBadge}</span>}
      </div>

      <dl className="flex flex-col gap-3 text-sm">
        <Field label={text.purposeLabel}>
          {purposeName}
          {corrected && transaction.effective_purpose_id !== undefined && (
            <span className="block text-muted-foreground">
              {copy.purposeCorrection.currentLabel(purposeNames.get(transaction.effective_purpose_id) ?? copy.home.purposeUnknown)}
            </span>
          )}
        </Field>
        {note && (
          <Field label={text.noteLabel}>
            <span className="break-words whitespace-pre-wrap">{note}</span>
          </Field>
        )}
        {transaction.account_name && <Field label={text.locationLabel}>{transaction.account_name}</Field>}
        <Field label={text.dateLabel}>{formatIsoDate(transaction.occurred_on)}</Field>
        <Field label={text.recordedAtLabel}>{formatUnixSecondsWithTime(transaction.created_at)}</Field>
        {receiptIds.length > 0 && (
          <Field label={text.receiptsLabel}>
            <span className="mt-1 flex flex-wrap gap-2">
              {receiptIds.map((id) => (
                <img key={id} src={receiptUrl(id)} alt="" className="size-16 rounded-md object-cover ring-1 ring-foreground/10" />
              ))}
            </span>
          </Field>
        )}
      </dl>

      {/* The one door to reversing a row (ADR-042), on the server's
          `reversible` - this dialog derives no rule. It posts nothing: it
          hands off to the form under the row (ADR-032). */}
      {onReverse && transaction.reversible && (
        <Button type="button" variant="outline" size="lg" onClick={() => onReverse(transaction)}>
          <Undo2 aria-hidden="true" />
          {copy.history.transactions.reverse}
        </Button>
      )}
    </div>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}
