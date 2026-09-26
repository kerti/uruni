import { MoreVertical } from 'lucide-react'
import { useEffect, useState } from 'react'

import ReceiptPicker from '@/components/ReceiptPicker'
import ReceiptViewer from '@/components/ReceiptViewer'
import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
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
  // The photo currently open in the full-screen viewer, if any (#154
  // follow-up) - a separate id from replacingId/confirmingDeleteId, since
  // the viewer and the menu's replace/delete flow are never open together
  // (tapping the photo opens the viewer; the menu sits on the same photo
  // but is a distinct control).
  const [viewingId, setViewingId] = useState<number | null>(null)
  // Where on the photo it was tapped, so the viewer opens centred there.
  const [viewAnchor, setViewAnchor] = useState<{ fx: number; fy: number } | null>(null)
  // Whether a photo's "more" menu is open, so a tap outside the dialog
  // closes only the menu (see onPointerDownOutside below).
  const [menuOpen, setMenuOpen] = useState(false)

  // A fresh dialog every time it opens: no form left over from the last
  // photo it showed.
  useEffect(() => {
    if (!open) return
    setNewFile(null)
    setMenuOpen(false)
    setReplacingId(null)
    setReplacementFile(null)
    setConfirmingDeleteId(null)
    setViewingId(null)
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
      <DialogContent
        closeLabel={copy.common.close}
        // The menus are non-modal (modal={false} below), so an outside tap
        // reaches this dialog too; with a menu open that tap only closes
        // the menu. The next outside tap closes the dialog - two taps, not
        // the three a modal menu's pointer-events lock used to cost.
        onPointerDownOutside={(event) => {
          if (menuOpen) event.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>{text.dialogHeading}</DialogTitle>
        </DialogHeader>

        {/* min-w-0: DialogContent is a grid, and a grid item is as wide as
            its longest unbreakable content - a phone gallery's UUID file
            name pushed the whole dialog past the screen before the
            picker's own `truncate` could ellipsize it. */}
        <div className="flex min-w-0 flex-col gap-3">
          {receiptIds.length === 0 && (
            <p className="text-muted-foreground">
              {kind === 'reimbursements' ? text.emptyReimbursement : text.emptyTransaction}
            </p>
          )}
          {receiptIds.map((id) => (
            <div key={id} className="flex flex-col gap-2 rounded-lg bg-muted p-2">
              <div className="relative">
                {/* The photo itself opens the full-screen viewer
                    (ReceiptViewer.tsx) - a button wrapper rather than a
                    plain onClick on the <img>, so it carries its own
                    accessible name and a visible focus ring. */}
                <button
                  type="button"
                  aria-label={text.zoomAria}
                  onClick={(event) => {
                    // A keyboard press has no pointer position (detail 0):
                    // null lets the viewer open on the photo's centre.
                    const rect = event.currentTarget.getBoundingClientRect()
                    setViewAnchor(
                      event.detail > 0 && rect.width > 0 && rect.height > 0
                        ? { fx: (event.clientX - rect.left) / rect.width, fy: (event.clientY - rect.top) / rect.height }
                        : null,
                    )
                    setViewingId(id)
                  }}
                  className="block w-full cursor-zoom-in rounded-md outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  {/* Full frame width, height from the photo's own aspect ratio -
                      no letterboxing. A long receipt makes a tall card; the
                      dialog body already scrolls. */}
                  <img src={receiptUrl(id)} alt="" className="block h-auto w-full rounded-md" />
                </button>

                {/* The per-photo actions, moved off a button row under the
                    image and onto the image itself (#154 follow-up): a
                    dark circular "more" button in the corner, legible over
                    any receipt including a plain white one. The menu only
                    opens Ganti foto/Hapus foto's own flow below - the
                    replace picker and the delete confirm both still render
                    under the photo, never inside the menu itself. */}
                <DropdownMenu modal={false} onOpenChange={setMenuOpen}>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      aria-label={text.photoMenuAria}
                      disabled={busy}
                      className="absolute top-1 right-1 flex size-11 items-center justify-center rounded-full bg-black/50 text-white outline-none transition-colors hover:bg-black/60 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
                    >
                      <MoreVertical aria-hidden="true" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem
                      onSelect={() => {
                        setReplacingId(id)
                        setConfirmingDeleteId(null)
                      }}
                    >
                      {text.change}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      variant="destructive"
                      onSelect={() => {
                        setConfirmingDeleteId(id)
                        setReplacingId(null)
                      }}
                    >
                      {text.delete}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>

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
              ) : null}
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
      </DialogContent>

      <ReceiptViewer
        open={viewingId !== null}
        src={viewingId !== null ? receiptUrl(viewingId) : null}
        anchor={viewAnchor}
        onClose={() => setViewingId(null)}
      />
    </Dialog>
  )
}
