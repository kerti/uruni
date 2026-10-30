import { forwardRef, useImperativeHandle, useRef, useState, type FormEvent } from 'react'

import RecordSummary from '@/components/RecordSummary'
import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import PasswordInput from '@/components/PasswordInput'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { confirmRestore, inspectRestoreUpload, inspectStoredRestore } from '@/lib/restore'
import { useApi } from '@/lib/useApi'
import type { FundRestorePreview, RestorePreview } from '@/lib/restore'

const text = copy.settings.backup
const confirmText = copy.restoreConfirm

/** RestoreDialog's imperative surface: the Cadangan card's per-row "pulihkan"
 * control has no file to pick, so it reaches past the file-picker trigger and
 * starts the identical inspect-then-confirm flow directly, against a stored
 * backup's server-side name instead of an uploaded file. */
export interface RestoreDialogHandle {
  openForStoredBackup: (name: string) => void
}

/**
 * The whole upload-then-restore flow (ADR-012), as one component the Cadangan
 * card mounts always-present: a hidden file input, an inspect step that runs
 * the instant a file is picked, and - once the server answers with a preview -
 * the confirm dialog (date, funds, total, password). Two server calls
 * (inspectRestoreUpload, confirmRestore), never the raw zip sent twice - see
 * lib/restore.ts.
 *
 * The auto-backup list's "pulihkan" row action, one per current-format stored
 * dump, calls openForStoredBackup (exposed via ref) instead of picking a file;
 * the preview/confirm step is the same one, so there is no second restore
 * implementation.
 *
 * open/preview/token live here rather than in Backup.tsx: the card needs only
 * the trigger button, so this component's surface is the button plus the
 * dialog it owns (and the ref handle above).
 */
const RestoreDialog = forwardRef<RestoreDialogHandle>(function RestoreDialog(_props, ref) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [inspectState, runInspect] = useApi<{ token: string; preview: RestorePreview }>()
  const [confirmState, runConfirm] = useApi<void>()
  const [password, setPassword] = useState('')
  const [open, setOpen] = useState(false)
  // useApi has no reset, and this dialog instance outlives every restore it
  // runs: without this flag a wrong-password error from a cancelled attempt
  // would sit under the next preview's password field.
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
    // The input is reset regardless, so picking the same file twice in a row
    // (after cancelling) still fires a change event.
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
      // Every session is gone now, including this one - the only correct next
      // step is a full reload, which lands on the login screen once
      // GET /api/session reports authenticated: false. Nothing in this SPA's
      // state is worth preserving through that.
      window.location.reload()
    })
  }

  return (
    <>
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip"
        // The button below is the visible control; this input only carries the
        // name for assistive tech and tests (as ReceiptPicker.tsx) and stays out
        // of the tab order so keyboard users meet one control, not two.
        aria-label={text.restoreLabel}
        tabIndex={-1}
        className="sr-only"
        onChange={handleFileChosen}
      />
      <Button
        type="button"
        variant="outline"
        size="lg"
        className="w-full justify-center gap-2"
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
              <RecordSummary
                items={[
                  { label: confirmText.dateLabel, value: formatIsoDate(staged.preview.date) },
                  {
                    label: confirmText.totalLabel,
                    value: <span className="tabular font-medium whitespace-nowrap">{formatIDR(staged.preview.total)}</span>,
                  },
                ]}
              />

              <ul className="flex flex-col gap-1">
                {staged.preview.funds.map((fund) => (
                  <li key={fund.fund_id} className="text-sm text-muted-foreground">
                    {fundLine(fund)}
                  </li>
                ))}
              </ul>

              <p className="text-sm text-attention-text">{confirmText.warning}</p>

              <div className="flex flex-col gap-1.5">
                <Label htmlFor="restore-password">{confirmText.passwordLabel}</Label>
                <PasswordInput
                  id="restore-password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  disabled={confirming}
                />
              </div>

              {confirmAttempted && confirmState.status === 'error' && confirmState.error && <ErrorState error={confirmState.error} />}

              <DialogFooter className="mt-1">
                <Button type="button" variant="outline" size="lg" disabled={confirming} onClick={handleClose}>
                  {copy.common.close}
                </Button>
                <Button type="submit" size="lg" disabled={confirming || password === ''}>
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

/** One preview line's wording, chosen by status and - for a kept fund - whether
 * the file carries a cutoff date for it (fundLine's three "kept" branches
 * mirror the three copy strings restoreConfirm offers for that split). */
function fundLine(fund: FundRestorePreview): string {
  if (fund.status === 'removed') return confirmText.fundRemoved(fund.name)
  if (fund.status === 'added') return confirmText.fundAdded(fund.name)
  if (fund.transactions_lost === 0) return confirmText.fundKeptSafe(fund.name)
  if (fund.cutoff_date) return confirmText.fundKeptWithCutoff(fund.name, fund.transactions_lost, formatIsoDate(fund.cutoff_date))
  return confirmText.fundKeptNoCutoffDate(fund.name, fund.transactions_lost)
}
