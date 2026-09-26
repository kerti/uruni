import { useEffect, useState } from 'react'

import ReceiptPicker from '@/components/ReceiptPicker'
import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'
import { deleteReceipt, receiptUrl, uploadReceipt } from '@/lib/receipts'
import { useApi } from '@/lib/useApi'
import type { ReceiptParentKind } from '@/lib/receipts'

const text = copy.receipts

/**
 * The viewer/attach/replace/delete dialog for one transaction's or one
 * reimbursement claim's photos (#154, ADR-011). Opened from the row
 * indicator (TransactionList.tsx, History/Reimbursements.tsx) whether the
 * row already has a photo or not - the same dialog covers "add a first
 * photo" and "look at, replace or remove one already there", since a row
 * with several receipt_ids still opens straight to the list rather than a
 * picker.
 *
 * `onChanged` is called after every successful upload or delete so the
 * caller can refetch the list this dialog's own receiptIds prop came from -
 * this component holds no list data of its own, matching
 * CorrectPurposeDialog.tsx's own contract. The dialog stays open across a
 * change (unlike CorrectPurposeDialog, which closes on success): a
 * treasurer replacing a wrong photo, or attaching a second one, stays on
 * this screen to see the result rather than needing to reopen it.
 *
 * `receiptIds` is read fresh from the caller's own list state on every
 * render, so a change lands here the moment the caller's refetch resolves -
 * there is no separate "loading" state for the ids themselves.
 */
export default function ReceiptDialog({
  kind,
  parentId,
  receiptIds,
  open,
  onClose,
  onChanged,
}: {
  kind: ReceiptParentKind
  parentId: number | null
  receiptIds: number[]
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const [state, run] = useApi<unknown>()
  const [newFile, setNewFile] = useState<File | null>(null)
  // The one receipt currently offering its "Ganti foto" picker, if any -
  // never more than one at a time, the same single-open-form shape
  // History/Reimbursements.tsx already uses for settle/correct.
  const [replacingId, setReplacingId] = useState<number | null>(null)
  const [replacementFile, setReplacementFile] = useState<File | null>(null)
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<number | null>(null)

  // A fresh dialog every time it opens: no form left over from the last
  // photo it showed.
  useEffect(() => {
    if (!open) return
    setNewFile(null)
    setReplacingId(null)
    setReplacementFile(null)
    setConfirmingDeleteId(null)
  }, [open, parentId])

  const busy = state.status === 'loading'

  function handleAdd() {
    if (parentId === null || !newFile) return
    void run(async () => {
      const result = await uploadReceipt(kind, parentId, newFile)
      setNewFile(null)
      onChanged()
      return result
    })
  }

  function handleReplace(oldId: number) {
    if (parentId === null || !replacementFile) return
    void run(async () => {
      // Delete-then-upload (no combined replace route): if the upload
      // fails after the delete succeeds, the row is left with one fewer
      // photo rather than a wrong one - the safer of the two ways this can
      // go half-done, and "Tambah foto nota" below still recovers it.
      await deleteReceipt(oldId)
      const result = await uploadReceipt(kind, parentId, replacementFile)
      setReplacingId(null)
      setReplacementFile(null)
      onChanged()
      return result
    })
  }

  function handleDelete(id: number) {
    void run(async () => {
      await deleteReceipt(id)
      setConfirmingDeleteId(null)
      onChanged()
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.dialogHeading}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          {receiptIds.map((id) => (
            <div key={id} className="flex flex-col gap-2 rounded-lg bg-muted p-2">
              <img src={receiptUrl(id)} alt="" className="max-h-64 w-full rounded-md object-contain" />

              {replacingId === id ? (
                <div className="flex flex-col gap-2">
                  <ReceiptPicker id={`receipt-replace-${id}`} value={replacementFile} onChange={setReplacementFile} disabled={busy} />
                  <div className="flex gap-2">
                    <Button type="button" size="lg" disabled={busy || !replacementFile} onClick={() => handleReplace(id)}>
                      {busy ? text.uploading : text.change}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="lg"
                      disabled={busy}
                      onClick={() => {
                        setReplacingId(null)
                        setReplacementFile(null)
                      }}
                    >
                      {text.cancel}
                    </Button>
                  </div>
                </div>
              ) : confirmingDeleteId === id ? (
                <div className="flex flex-col gap-2 rounded-lg bg-attention-soft p-2">
                  <p className="text-sm">{text.deleteConfirm}</p>
                  <div className="flex gap-2">
                    <Button type="button" variant="destructive" size="lg" disabled={busy} onClick={() => handleDelete(id)}>
                      {busy ? text.deleting : text.delete}
                    </Button>
                    <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => setConfirmingDeleteId(null)}>
                      {text.cancel}
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="flex gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="lg"
                    disabled={busy}
                    onClick={() => {
                      setReplacingId(id)
                      setConfirmingDeleteId(null)
                    }}
                  >
                    {text.change}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="lg"
                    className="text-destructive"
                    disabled={busy}
                    onClick={() => {
                      setConfirmingDeleteId(id)
                      setReplacingId(null)
                    }}
                  >
                    {text.delete}
                  </Button>
                </div>
              )}
            </div>
          ))}

          {/* Attaching a first photo and attaching another are the same
              action, so the same picker + button covers both - never
              rendered at the same time as a replace form, so the dialog
              never shows two open file pickers at once. */}
          {replacingId === null && (
            <div className="flex flex-col gap-2">
              <ReceiptPicker id="receipt-add" value={newFile} onChange={setNewFile} disabled={busy} />
              {newFile && (
                <Button type="button" size="lg" disabled={busy} onClick={handleAdd}>
                  {busy ? text.uploading : text.addFromRow}
                </Button>
              )}
            </div>
          )}

          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
        </div>

        <DialogFooter className="mt-1">
          <Button type="button" variant="outline" className="h-11 w-full" onClick={onClose}>
            {copy.common.close}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
