import { useRef, useState, type FormEvent } from 'react'

import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import { confirmRestore, inspectRestoreUpload } from '@/lib/restore'
import { useApi } from '@/lib/useApi'
import type { FundRestorePreview, RestorePreview } from '@/lib/restore'

const text = copy.settings.backup
const confirmText = copy.restoreConfirm

/**
 * The whole upload-then-restore flow (M6.39, #325, ADR-012), as one
 * component the Cadangan card mounts always-present: a hidden file input
 * this component owns, an inspect step that runs the instant a file is
 * picked, and - once the server has answered with a preview - the confirm
 * dialog itself (date, funds, total, password). Two server calls
 * (inspectRestoreUpload, confirmRestore), never the raw zip sent twice -
 * lib/restore.ts's own comment has the reasoning.
 *
 * open/preview/token all live here rather than in Backup.tsx: the trigger
 * button is the only piece the card itself needs to render, so this
 * component's own exported surface is just the button plus the dialog it
 * owns.
 */
export default function RestoreDialog() {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [inspectState, runInspect] = useApi<{ token: string; preview: RestorePreview }>()
  const [confirmState, runConfirm] = useApi<void>()
  const [password, setPassword] = useState('')
  const [open, setOpen] = useState(false)

  const inspecting = inspectState.status === 'loading'
  const confirming = confirmState.status === 'loading'
  const staged = inspectState.status === 'success' ? inspectState.data : undefined

  function handleFileChosen(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    // The input is reset regardless, so picking the exact same file twice
    // in a row (after cancelling) still fires a change event the second
    // time.
    event.target.value = ''
    if (!file) return

    setPassword('')
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

      {inspectState.status === 'error' && inspectState.error && <ErrorState error={inspectState.error} />}

      <Dialog open={open} onOpenChange={(next) => !next && handleClose()}>
        <DialogContent closeLabel={copy.common.close}>
          <DialogHeader>
            <DialogTitle>{confirmText.heading}</DialogTitle>
          </DialogHeader>

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

              {confirmState.status === 'error' && confirmState.error && <ErrorState error={confirmState.error} />}

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
}

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
