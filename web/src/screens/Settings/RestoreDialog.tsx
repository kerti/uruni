import { forwardRef, useImperativeHandle, useRef, useState, type FormEvent } from 'react'

import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import { confirmRestore, inspectRestoreUpload, inspectStoredRestore } from '@/lib/restore'
import { useApi } from '@/lib/useApi'
import type { FundRestorePreview, RestorePreview } from '@/lib/restore'

const text = copy.settings.backup
const confirmText = copy.restoreConfirm

/** RestoreDialog's own imperative surface (#326): the Cadangan card's
 * per-row "pulihkan" control has no file to pick, so it reaches past the
 * file-picker trigger this component renders and starts the identical
 * inspect-then-confirm flow directly, against a stored backup's own
 * server-side name instead of an uploaded file. */
export interface RestoreDialogHandle {
  openForStoredBackup: (name: string) => void
}

/**
 * The whole upload-then-restore flow (M6.39, #325, ADR-012), as one
 * component the Cadangan card mounts always-present: a hidden file input
 * this component owns, an inspect step that runs the instant a file is
 * picked, and - once the server has answered with a preview - the confirm
 * dialog itself (date, funds, total, password). Two server calls
 * (inspectRestoreUpload, confirmRestore), never the raw zip sent twice -
 * lib/restore.ts's own comment has the reasoning.
 *
 * #326 adds a second way into the same dialog: the auto-backup list's own
 * "pulihkan" row action, one per current-format stored dump, calls
 * openForStoredBackup (exposed via ref) instead of picking a file - the
 * preview/confirm step that follows is exactly this component's existing
 * one, unchanged, so there is no second restore implementation for a
 * stored backup to run through.
 *
 * open/preview/token all live here rather than in Backup.tsx: the trigger
 * button is the only piece the card itself needs to render, so this
 * component's own exported surface is just the button plus the dialog it
 * owns (plus, now, the ref handle above).
 */
const RestoreDialog = forwardRef<RestoreDialogHandle>(function RestoreDialog(_props, ref) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [inspectState, runInspect] = useApi<{ token: string; preview: RestorePreview }>()
  const [confirmState, runConfirm] = useApi<void>()
  const [password, setPassword] = useState('')
  const [open, setOpen] = useState(false)
  // useApi has no reset, and this one dialog instance outlives every restore
  // it runs: without this flag a wrong-password error from a cancelled
  // attempt would still sit under the next preview's password field.
  const [confirmAttempted, setConfirmAttempted] = useState(false)

  const inspecting = inspectState.status === 'loading'
  const confirming = confirmState.status === 'loading'
  const staged = inspectState.status === 'success' ? inspectState.data : undefined

  useImperativeHandle(ref, () => ({
    openForStoredBackup(name: string) {
      setPassword('')
      setConfirmAttempted(false)
      setOpen(true)
      void runInspect(async () => {
        const result = await inspectStoredRestore(name)
        return { token: result.token, preview: result.preview }
      })
    },
  }))

  function handleFileChosen(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    // The input is reset regardless, so picking the exact same file twice
    // in a row (after cancelling) still fires a change event the second
    // time.
    event.target.value = ''
    if (!file) return

    setPassword('')
    setConfirmAttempted(false)
    setOpen(true)
    void runInspect(async () => {
      const result = await inspectRestoreUpload(file)
      return { token: result.token, preview: result.preview }
    })
  }

  function handleClose() {
    if (confirming) return
    setOpen(false)
    setPassword('')
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (!staged || password === '') return
    setConfirmAttempted(true)
    void runConfirm(async () => {
      await confirmRestore(staged.token, password)
      // Every session is gone now, including this one - the only correct
      // next step is a full reload, which lands on the login screen once
      // GET /api/session reports authenticated: false. There is nothing
      // left in this SPA's own state worth preserving through that.
      window.location.reload()
    })
  }

  return (
    <>
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip"
        // The button below is the visible control; this input only carries
        // the name for assistive tech and tests (ReceiptPicker.tsx's own
        // pattern), and stays out of the tab order so keyboard users meet
        // one control, not two.
        aria-label={text.restoreLabel}
        tabIndex={-1}
        className="sr-only"
        onChange={handleFileChosen}
      />
      <Button
        type="button"
        variant="outline"
        className="h-11 w-full justify-center gap-2"
        disabled={inspecting}
        onClick={() => fileInputRef.current?.click()}
      >
        {inspecting ? text.inspecting : text.restoreLabel}
      </Button>

      <Dialog open={open} onOpenChange={(next) => !next && handleClose()}>
        <DialogContent closeLabel={copy.common.close}>
          <DialogHeader>
            <DialogTitle>{confirmText.heading}</DialogTitle>
          </DialogHeader>

          {/* Inspect runs with the dialog already open (either entry point),
              so its progress and its failure - e.g. a stored backup pruned
              by retention since the list loaded - belong in here, not
              behind the overlay. */}
          {inspecting && <p className="text-sm text-muted-foreground">{text.inspecting}</p>}
          {inspectState.status === 'error' && inspectState.error && <ErrorState error={inspectState.error} />}

          {staged && (
            <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
              <p className="text-sm text-muted-foreground">{confirmText.dateLabel(staged.preview.date)}</p>
              <p className="text-sm font-medium">{confirmText.totalLabel(formatIDR(staged.preview.total))}</p>

              <ul className="flex flex-col gap-1">
                {staged.preview.funds.map((fund) => (
                  <li key={fund.fund_id} className="text-sm text-muted-foreground">
                    {fundLine(fund)}
                  </li>
                ))}
              </ul>

              <p className="text-sm text-attention">{confirmText.warning}</p>

              <div className="flex flex-col gap-1.5">
                <Label htmlFor="restore-password">{confirmText.passwordLabel}</Label>
                <Input
                  id="restore-password"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  disabled={confirming}
                />
              </div>

              {confirmAttempted && confirmState.status === 'error' && confirmState.error && <ErrorState error={confirmState.error} />}

              <DialogFooter className="mt-1">
                <Button type="button" variant="outline" className="h-11" disabled={confirming} onClick={handleClose}>
                  {confirmText.cancel}
                </Button>
                <Button type="submit" className="h-11" disabled={confirming || password === ''}>
                  {confirming ? confirmText.confirming : confirmText.confirm}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
})

export default RestoreDialog

/** One preview line's own wording, chosen by status and - for a kept fund -
 * whether the file carries a cutoff date for it at all (fundLine's own
 * three branches for "kept" mirror the three copy strings restoreConfirm
 * offers for exactly that split). */
function fundLine(fund: FundRestorePreview): string {
  if (fund.status === 'removed') return confirmText.fundRemoved(fund.name)
  if (fund.status === 'added') return confirmText.fundAdded(fund.name)
  if (fund.transactions_lost === 0) return confirmText.fundKeptSafe(fund.name)
  if (fund.cutoff_date) return confirmText.fundKeptWithCutoff(fund.name, fund.transactions_lost, fund.cutoff_date)
  return confirmText.fundKeptNoCutoffDate(fund.name, fund.transactions_lost)
}
