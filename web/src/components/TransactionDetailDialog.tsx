import { ArrowDownLeft, ArrowUpRight } from 'lucide-react'
import type { ReactNode } from 'react'

import TransactionRowLabel from '@/components/TransactionRowLabel'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'
import { formatIsoDate, formatUnixSecondsWithTime } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { receiptUrl } from '@/lib/receipts'
import { noteForDisplay } from '@/lib/transactions'
import type { Transaction } from '@/lib/transactions'

const text = copy.transactionDetail

/**
 * One ledger entry read in full (#359): everything the row already carries,
 * nothing truncated. Read-only on purpose - the row's own controls
 * (peruntukan correction, photos, contribution undo) stay on the row, so
 * this dialog never becomes a second place to change money.
 *
 * No fetch: the Transaction the list already holds is the whole of what is
 * shown. The stored peruntukan leads, as on the row (ADR-033); a corrected
 * row adds the tag the money is under now, with the correction dialog's own
 * "Peruntukan saat ini" wording so the two never disagree.
 */
export default function TransactionDetailDialog({
  transaction,
  purposeNames,
  onClose,
}: {
  transaction: Transaction | null
  purposeNames: Map<number, string>
  onClose: () => void
}) {
  return (
    <Dialog
      open={transaction !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.heading}</DialogTitle>
        </DialogHeader>
        {transaction && <Detail transaction={transaction} purposeNames={purposeNames} />}
      </DialogContent>
    </Dialog>
  )
}

function Detail({ transaction, purposeNames }: { transaction: Transaction; purposeNames: Map<number, string> }) {
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
