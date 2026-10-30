import { Camera, Eraser, HandCoins, Pencil, Trash2, Undo2 } from 'lucide-react'
import { useRef } from 'react'

import DetailField from '@/components/DetailField'
import { ACCEPTED_TYPES } from '@/components/ReceiptPicker'
import ErrorState from '@/components/states/ErrorState'
import { useToast } from '@/components/toast/useToast'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { uploadReceipt } from '@/lib/receipts'
import { useApi } from '@/lib/useApi'
import type { Reimbursement } from '@/lib/reimbursements'

const text = copy.reimbursements

/** What a claim's detail can hand off to or run; the screen owns each one. */
export interface ClaimDetailActions {
  onSettle: (claim: Reimbursement) => void
  onCorrect: (claim: Reimbursement) => void
  onWaive: (claim: Reimbursement) => void
  onUnwaive: (claim: Reimbursement) => void
  onDelete: (claim: Reimbursement) => void
  onViewReceipts: (claim: Reimbursement) => void
  /** Called after a first nota is uploaded, so the screen re-reads its list. */
  onReceiptsChanged: () => void
}

/**
 * One Talangan claim read in full (#768): the card is one tap target, and
 * every action lives here, each shown only where it applies. Bayar, Ubah and
 * Hapus hand off (the screen closes this dialog first, then opens the next
 * one or navigates - never two dialogs at once, ADR-032); Putihkan and
 * Batalkan pemutihan are in-place writes the screen runs. The nota follows
 * TransactionDetailDialog: a first photo opens the picker right here, an
 * existing one hands off to the receipt viewer.
 *
 * `error` is a write's refusal (a 409), said here where she is looking.
 */
export default function ClaimDetailDialog({
  claim,
  memberName,
  purposeName,
  busy,
  error,
  onClose,
  onCloseAutoFocus,
  ...actions
}: {
  claim: Reimbursement | null
  memberName: string
  purposeName: string
  busy: boolean
  error: string | null
  onClose: () => void
  onCloseAutoFocus?: (event: Event) => void
} & ClaimDetailActions) {
  return (
    <Dialog
      open={claim !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close} onCloseAutoFocus={onCloseAutoFocus}>
        <DialogHeader>
          <DialogTitle>{text.detail.heading}</DialogTitle>
        </DialogHeader>
        {claim && <Detail claim={claim} memberName={memberName} purposeName={purposeName} busy={busy} error={error} {...actions} />}
      </DialogContent>
    </Dialog>
  )
}

function Detail({
  claim,
  memberName,
  purposeName,
  busy,
  error,
  onSettle,
  onCorrect,
  onWaive,
  onUnwaive,
  onDelete,
  onViewReceipts,
  onReceiptsChanged,
}: {
  claim: Reimbursement
  memberName: string
  purposeName: string
  busy: boolean
  error: string | null
} & ClaimDetailActions) {
  const [upload, runUpload] = useApi<unknown>()
  const toast = useToast()
  const fileInput = useRef<HTMLInputElement>(null)
  const receiptIds = claim.receipt_ids ?? []
  const waived = claim.waived_on !== null
  const open = !waived && !claim.settled
  const status = waived ? text.status.waived : claim.settled ? text.status.settled : text.status.outstanding
  const uploading = upload.status === 'loading'

  function handlePick(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0] ?? null
    // Cleared so picking the same file again after a failure still fires.
    event.target.value = ''
    if (!file) return
    void runUpload(
      async () => {
        const result = await uploadReceipt('reimbursements', claim.id, file)
        onReceiptsChanged()
        toast.success(copy.receipts.added)
        return result
      },
      // A lost response may still have stored the photo; re-read before she retries.
      { settle: onReceiptsChanged },
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {/* The same reading layout as Rincian transaksi: where a transaction
          says Uang masuk or Uang keluar, a claim says where it stands; the
          amount is the headline; then each fact with its label above it. */}
      <div className="flex flex-col gap-1">
        <span className="text-sm text-muted-foreground">{status}</span>
        <span className="tabular text-2xl font-semibold">{formatIDR(claim.amount)}</span>
      </div>
      <dl className="flex flex-col gap-3 text-sm">
        <DetailField label={text.record.memberLabel}>{memberName}</DetailField>
        <DetailField label={text.record.purposeLabel}>{purposeName}</DetailField>
        {claim.note && (
          <DetailField label={copy.transactionDetail.noteLabel}>
            <span className="whitespace-pre-wrap">{claim.note}</span>
          </DetailField>
        )}
        <DetailField label={text.record.dateLabel}>{formatIsoDate(claim.incurred_on)}</DetailField>
      </dl>
      {error && (
        <p role="alert" className="text-sm text-attention-text">
          {error}
        </p>
      )}

      <div className="flex flex-col gap-2">
        {open && (
          <>
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => onSettle(claim)}>
              <HandCoins aria-hidden="true" />
              {text.actions.settle}
            </Button>
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => onCorrect(claim)}>
              <Pencil aria-hidden="true" />
              {text.actions.correct}
            </Button>
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => onWaive(claim)}>
              <Eraser aria-hidden="true" />
              {text.actions.waive}
            </Button>
          </>
        )}
        {waived && (
          <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => onUnwaive(claim)}>
            <Undo2 aria-hidden="true" />
            {text.actions.unwaive}
          </Button>
        )}
        {receiptIds.length > 0 ? (
          <Button type="button" variant="outline" size="lg" onClick={() => onViewReceipts(claim)}>
            <Camera aria-hidden="true" />
            {copy.receipts.viewReceipt}
          </Button>
        ) : (
          <>
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
        )}
        {upload.status === 'error' && upload.error && <ErrorState error={upload.error} />}
        {open && (
          <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => onDelete(claim)}>
            <Trash2 aria-hidden="true" />
            {text.actions.delete}
          </Button>
        )}
      </div>
    </div>
  )
}
