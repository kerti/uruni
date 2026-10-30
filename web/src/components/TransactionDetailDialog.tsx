import { ArrowDownLeft, ArrowUpRight, Camera, PenLine, Undo2 } from 'lucide-react'
import { useRef } from 'react'
import type { ReactNode } from 'react'

import { isPurposePair, isTransfer, rowLabelFor, transferKind } from '@/components/rowLabel'
import { ACCEPTED_TYPES } from '@/components/ReceiptPicker'
import ErrorState from '@/components/states/ErrorState'
import { useToast } from '@/components/toast/useToast'
import TransactionRowLabel from '@/components/TransactionRowLabel'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'
import { formatIsoDate, formatUnixSecondsWithTime } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { receiptUrl, uploadReceipt } from '@/lib/receipts'
import { canCorrectPurpose, noteForDisplay } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Transaction } from '@/lib/transactions'

const text = copy.transactionDetail

/**
 * One ledger entry read in full: everything the row already carries, nothing
 * truncated. The row is one tap target (#739), so every action lives here, at
 * the bottom: Perbaiki pos and Nota hand off to their own dialog (or, for a
 * first nota, the photo picker), and Batalkan transaksi hands off to the form
 * under the row. None of them changes money from inside this dialog.
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
  onCorrectPurpose,
  onViewReceipts,
  onReceiptsChanged,
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
  /** Hands an eligible row to the correction dialog (ADR-033). Optional like
   * onReverse: Beranda and the envelope's activity omit it. */
  onCorrectPurpose?: (transaction: Transaction) => void
  /** Hands a row that has notas to the receipt viewer. */
  onViewReceipts?: (transaction: Transaction) => void
  /** Wired where receipts are: gives a row with no nota its Tambah foto nota,
   * and is called after the upload so the caller refetches its list. */
  onReceiptsChanged?: () => void
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
        {transaction && (
          <Detail
            transaction={transaction}
            purposeNames={purposeNames}
            onReverse={onReverse}
            onCorrectPurpose={onCorrectPurpose}
            onViewReceipts={onViewReceipts}
            onReceiptsChanged={onReceiptsChanged}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function Detail({
  transaction,
  purposeNames,
  onReverse,
  onCorrectPurpose,
  onViewReceipts,
  onReceiptsChanged,
}: {
  transaction: Transaction
  purposeNames: Map<number, string>
  onReverse?: (transaction: Transaction) => void
  onCorrectPurpose?: (transaction: Transaction) => void
  onViewReceipts?: (transaction: Transaction) => void
  onReceiptsChanged?: () => void
}) {
  const [upload, runUpload] = useApi<unknown>()
  const toast = useToast()
  const fileInput = useRef<HTMLInputElement>(null)
  const incoming = transaction.direction === 'in'
  // A transfer is one row for two legs (#738): it has no direction, and its
  // from -> to is in the label under the amount. The leg the list picked
  // would mislead on the direction, on a location transfer's location, and on
  // a pos pair's pos, so those three are left out.
  const transfer = isTransfer(transaction)
  const TransferIcon = transfer ? rowLabelFor(transaction)?.Icon : undefined
  const purposeName = purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown
  const corrected = transaction.effective_purpose_id !== undefined && transaction.effective_purpose_id !== transaction.purpose_id
  const note = noteForDisplay(transaction)
  const receiptIds = transaction.receipt_ids ?? []
  const canCorrect = onCorrectPurpose !== undefined && !transfer && canCorrectPurpose(transaction)
  const canReverse = onReverse !== undefined && transaction.reversible && !transfer
  const uploading = upload.status === 'loading'

  function handlePick(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0] ?? null
    // Cleared so picking the same file again after a failure still fires.
    event.target.value = ''
    if (!file) return
    void runUpload(
      async () => {
        const result = await uploadReceipt('transactions', transaction.id, file)
        onReceiptsChanged?.()
        toast.success(copy.receipts.added)
        return result
      },
      // A lost response may still have stored the photo; re-read before she retries.
      { settle: onReceiptsChanged },
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        {/* Where an entry says Masuk or Keluar, a transfer says what kind of
            movement it is (#738), in the same place and the same words as its
            row. */}
        {transfer ? (
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            {TransferIcon && <TransferIcon aria-hidden="true" className="size-4" />}
            {transferKind(transaction)}
          </span>
        ) : (
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            {incoming ? (
              <ArrowDownLeft aria-hidden="true" className="size-4 text-success" />
            ) : (
              <ArrowUpRight aria-hidden="true" className="size-4 text-attention" />
            )}
            {incoming ? text.directionIn : text.directionOut}
          </span>
        )}
        <span className="tabular text-2xl font-semibold">{formatIDR(transaction.amount)}</span>
        <TransactionRowLabel transaction={transaction} wrap bare={transfer} kindShown={transfer} />
        {transaction.reversed && <span className="text-sm text-muted-foreground">{copy.dues.history.reversedBadge}</span>}
      </div>

      <dl className="flex flex-col gap-3 text-sm">
        {!isPurposePair(transaction) && (
          <Field label={text.purposeLabel}>
            {purposeName}
            {corrected && transaction.effective_purpose_id !== undefined && (
              <span className="block text-muted-foreground">
                {copy.purposeCorrection.currentLabel(purposeNames.get(transaction.effective_purpose_id) ?? copy.home.purposeUnknown)}
              </span>
            )}
          </Field>
        )}
        {note && (
          <Field label={text.noteLabel}>
            <span className="break-words whitespace-pre-wrap">{note}</span>
          </Field>
        )}
        {transaction.account_name && !(transfer && transaction.transfer_kind === 'between_accounts') && (
          <Field label={text.locationLabel}>{transaction.account_name}</Field>
        )}
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

      {/* Every action of the row, stacked, destructive last (#739). Perbaiki
          pos on the row's eligibility (the one PurposeLine used to apply,
          never a transfer); Nota only where receipts are wired; Batalkan on
          the server's `reversible` (ADR-042). */}
      {(canCorrect || onReceiptsChanged || canReverse) && (
        <div className="flex flex-col gap-2">
          {canCorrect && (
            <Button type="button" variant="outline" size="lg" onClick={() => onCorrectPurpose?.(transaction)}>
              <PenLine aria-hidden="true" />
              {copy.purposeCorrection.heading}
            </Button>
          )}
          {onReceiptsChanged &&
            (receiptIds.length > 0 ? (
              <Button type="button" variant="outline" size="lg" onClick={() => onViewReceipts?.(transaction)}>
                <Camera aria-hidden="true" />
                {copy.receipts.viewReceipt}
              </Button>
            ) : (
              <>
                {/* No dialog for a first nota: the picker opens directly and
                    the upload runs on pick, as ReceiptDialog's add does. */}
                <Button type="button" variant="outline" size="lg" disabled={uploading} onClick={() => fileInput.current?.click()}>
                  <Camera aria-hidden="true" />
                  {uploading ? copy.receipts.uploading : copy.receipts.addFromRow}
                </Button>
                {/* Hidden from the accessibility tree: the button above is the one
                    control, and an exposed file input reads as a second
                    "Tambah foto nota" button. */}
                <input
                  ref={fileInput}
                  type="file"
                  accept={ACCEPTED_TYPES}
                  aria-label={copy.receipts.addFromRow}
                  aria-hidden="true"
                  tabIndex={-1}
                  className="sr-only"
                  disabled={uploading}
                  onChange={handlePick}
                />
              </>
            ))}
          {upload.status === 'error' && upload.error && <ErrorState error={upload.error} />}
          {canReverse && (
            <Button type="button" variant="outline" size="lg" onClick={() => onReverse?.(transaction)}>
              <Undo2 aria-hidden="true" />
              {copy.history.transactions.reverse}
            </Button>
          )}
        </div>
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
