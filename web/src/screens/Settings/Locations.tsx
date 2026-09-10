import { useEffect, useRef, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { createAccount, deleteAccount, listAccounts, setAccountInactiveOn, updateAccount } from '@/lib/accounts'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Account } from '@/lib/accounts'

const text = copy.settings.locations

/** Local YYYY-MM-DD - never toISOString(), which is UTC and can read a day
 * early in WIB. Same helper as RecordTransaction.tsx's todayISODate. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

/** What `?edit=` names on this screen - a new location, or an existing one
 * by id. Anything else (a different prefix, a non-numeric id) is not this
 * screen's dialog to open. */
type EditTarget = { kind: 'new' } | { kind: 'edit'; id: number }

function parseEditTarget(value: string | null): EditTarget | null {
  if (value === null) return null
  const separator = value.indexOf(':')
  if (separator < 0) return null
  const prefix = value.slice(0, separator)
  const rest = value.slice(separator + 1)
  if (prefix !== 'location') return null
  if (rest === 'new') return { kind: 'new' }
  const id = Number(rest)
  return Number.isInteger(id) && id > 0 ? { kind: 'edit', id } : null
}

/**
 * The locations section of the settings screen (M6.15, converted to the
 * dialog primitive in M6.28 - ADR-032 "Every non-posting edit is a
 * dialog"): a card list, with add and edit both opening a bottom sheet
 * addressed by `?edit=location:<id>` / `?edit=location:new`.
 *
 * Every location the fund has is listed, retired ones included - a retired
 * location may still hold a balance, so home keeps showing it (M6.9) and
 * this screen is where it gets reinstated. What a retired location drops out
 * of is already handled elsewhere and needs nothing here: AccountPicker
 * filters `inactive_on` rows out of the record form, and Reconcile.tsx does
 * the same for the mandatory count.
 *
 * Name and kind are both editable, and for the same reason: they are labels
 * on the location, not posted facts. Nothing in internal/ledger branches on
 * kind - the schema's CHECK (kind IN ('cash','bank')) is the only rule it
 * carries - so a location entered as the wrong one is a typo like any
 * other, and correcting either moves nothing already posted.
 */
export default function Locations() {
  const [listState, listRun] = useApi<Account[]>()
  const { value, open, close, clear } = useDialogParam()

  useEffect(() => {
    // listRun is a stable useCallback (useApi.ts), so this fires once.
    void listRun(listAccounts)
  }, [listRun])

  function reload() {
    void listRun(listAccounts)
  }

  const target = parseEditTarget(value)
  const editingAccount = target?.kind === 'edit' ? (listState.data?.find((a) => a.id === target.id) ?? null) : null

  // ?edit= with an unknown id, a malformed value, or a different prefix:
  // strip it once the list has loaded, rather than flash an empty dialog.
  // `replace` because a dead link is not a real navigation to undo - and
  // clear(), never close(): right after a delete the reloaded list can land
  // before close()'s own back navigation does, and a second back would
  // leave the settings screen.
  useEffect(() => {
    if (value === null || target?.kind === 'new') return
    if (listState.status !== 'success') return
    if (target?.kind === 'edit' && editingAccount !== null) return
    clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, target?.kind, listState.status, editingAccount])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      {listState.status === 'idle' || listState.status === 'loading' ? (
        <Loading />
      ) : listState.status === 'error' || !listState.data ? (
        listState.error && <ErrorState error={listState.error} onRetry={reload} />
      ) : listState.data.length === 0 ? (
        <p className="text-muted-foreground">{text.empty}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {listState.data.map((account) => (
            <li key={account.id}>
              <button
                type="button"
                aria-label={text.editAria(account.name)}
                onClick={() => open(`location:${account.id}`)}
                className="flex min-h-11 w-full flex-col gap-1 rounded-lg bg-card px-4 py-3 text-left ring-1 ring-foreground/10 transition-colors hover:bg-muted/40"
              >
                <span className="flex w-full items-baseline justify-between gap-3">
                  <span className="min-w-0 truncate font-medium">{account.name}</span>
                  <span className="shrink-0 text-sm text-muted-foreground">
                    {account.kind === 'bank' ? text.kindBank : text.kindCash}
                  </span>
                </span>
                {account.inactive_on !== null && (
                  <span className="text-sm text-muted-foreground">{text.inactiveBadge}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}

      <Button type="button" variant="outline" className="h-11 self-start" onClick={() => open('location:new')}>
        {text.add}
      </Button>

      <AddLocationDialog
        open={target?.kind === 'new'}
        onClose={close}
        onAdded={() => {
          close()
          reload()
        }}
      />
      <EditLocationDialog
        account={editingAccount}
        open={target?.kind === 'edit' && editingAccount !== null}
        onClose={close}
        onChanged={() => {
          close()
          reload()
        }}
      />
    </section>
  )
}

/** Adding a location after setup (#78: setup asks for the first batch, this
 * is what adds to them afterward). No opening balance field here - that is
 * #230's named exception to "dialogs never post", not this slice's. */
function AddLocationDialog({ open, onClose, onAdded }: { open: boolean; onClose: () => void; onAdded: () => void }) {
  const [state, run] = useApi<Account>()
  const [kind, setKind] = useState<'cash' | 'bank'>('cash')
  const [name, setName] = useState('')

  const busy = state.status === 'loading'

  // A fresh form every time the sheet opens, so a location added a moment
  // ago does not leave its name sitting in the field for the next one.
  useEffect(() => {
    if (open) {
      setKind('cash')
      setName('')
    }
  }, [open])

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') return
    void run(async () => {
      const created = await createAccount(kind, trimmed)
      onAdded()
      return created
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent title={text.add} closeLabel={copy.common.close}>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-location-kind">{text.kindLabel}</Label>
            <Select value={kind} onValueChange={(next) => setKind(next as 'cash' | 'bank')}>
              <SelectTrigger id="new-location-kind" aria-label={text.kindLabel}>
                <SelectValue>{kind === 'bank' ? text.kindBank : text.kindCash}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="cash">{text.kindCash}</SelectItem>
                <SelectItem value="bank">{text.kindBank}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-location-name">{text.nameLabel}</Label>
            <Input id="new-location-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <Button type="submit" className="h-11" disabled={busy || name.trim() === ''}>
            {busy ? text.adding : text.add}
          </Button>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Which of the dialog's two destructive actions the footer is confirming,
 * if either - inline, in place, never a second dialog and never
 * `window.confirm()` (ADR-032). */
type ConfirmKind = 'deactivate' | 'delete' | null

/**
 * Rename, retire and delete, all in one sheet. `account` is null only while
 * closing (the row that opened it may already be gone from `listState` by
 * the time the exit animation plays) - the last known account is kept on
 * screen for that window rather than blanking the form mid-close.
 */
function EditLocationDialog({
  account,
  open,
  onClose,
  onChanged,
}: {
  account: Account | null
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const lastAccountRef = useRef<Account | null>(null)
  if (account) lastAccountRef.current = account
  const shown = account ?? lastAccountRef.current

  const [state, run] = useApi<unknown>()
  const [name, setName] = useState(shown?.name ?? '')
  const [kind, setKind] = useState<'cash' | 'bank'>(shown?.kind === 'bank' ? 'bank' : 'cash')
  const [confirming, setConfirming] = useState<ConfirmKind>(null)

  const busy = state.status === 'loading'
  const inactive = shown?.inactive_on !== null && shown?.inactive_on !== undefined

  // A fresh copy of the account's own fields each time the sheet opens for
  // it, and the confirm footer starts closed - reopening a dialog never
  // shows a stale edit or a confirm left mid-flight from last time.
  useEffect(() => {
    if (open && shown) {
      setName(shown.name)
      setKind(shown.kind === 'bank' ? 'bank' : 'cash')
      setConfirming(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, shown?.id])

  // onChanged closes the dialog, so it runs only once the request has
  // succeeded. useApi's run never throws - a 409 lands in `state` - and
  // closing on it would hide the deleteRefused sentence she needs to see.
  async function submit(fn: () => Promise<unknown>) {
    await run(async () => {
      const result = await fn()
      onChanged()
      return result
    })
  }

  function handleSave(event: FormEvent) {
    event.preventDefault()
    if (!shown) return
    const trimmed = name.trim()
    // Nothing changed, or nothing left to change it to: close rather than
    // spend a request saying so.
    if (trimmed === '' || (trimmed === shown.name && kind === shown.kind)) {
      onClose()
      return
    }
    void submit(() =>
      updateAccount(shown.id, {
        name: trimmed === shown.name ? undefined : trimmed,
        kind: kind === shown.kind ? undefined : kind,
      }),
    )
  }

  if (!shown) return null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent title={text.editTitle} closeLabel={copy.common.close}>
        <div className="flex flex-col gap-4">
          <form className="flex flex-col gap-3" onSubmit={handleSave} noValidate>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-location-name">{text.nameLabel}</Label>
              <Input
                id="edit-location-name"
                type="text"
                value={name}
                disabled={confirming !== null}
                onChange={(event) => setName(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-location-kind">{text.kindLabel}</Label>
              <Select value={kind} onValueChange={(next) => setKind(next as 'cash' | 'bank')} disabled={confirming !== null}>
                <SelectTrigger id="edit-location-kind" aria-label={text.kindLabel}>
                  <SelectValue>{kind === 'bank' ? text.kindBank : text.kindCash}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="cash">{text.kindCash}</SelectItem>
                  <SelectItem value="bank">{text.kindBank}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {confirming === null && (
              <div className="flex gap-2">
                <Button type="submit" className="h-11" disabled={busy}>
                  {busy ? text.saving : text.save}
                </Button>
                <Button type="button" variant="ghost" className="h-11" disabled={busy} onClick={onClose}>
                  {text.cancel}
                </Button>
              </div>
            )}
          </form>

          <div className="border-t border-border pt-4">
            {confirming === null ? (
              <div className="flex flex-wrap gap-2">
                {inactive ? (
                  <Button
                    type="button"
                    variant="outline"
                    className="h-11"
                    disabled={busy}
                    onClick={() => void submit(() => setAccountInactiveOn(shown.id, null))}
                  >
                    {busy ? text.reinstating : text.reinstate}
                  </Button>
                ) : (
                  <Button type="button" variant="outline" className="h-11" onClick={() => setConfirming('deactivate')}>
                    {text.deactivate}
                  </Button>
                )}
                <Button type="button" variant="ghost" className="h-11 text-destructive" onClick={() => setConfirming('delete')}>
                  {text.delete}
                </Button>
              </div>
            ) : (
              // The inline confirm, swapped into this same footer - never a
              // second dialog and never window.confirm() (ADR-032). The
              // consequence is named in terracotta, never alarm-red.
              <div className="flex flex-col gap-3">
                <p className="text-sm text-attention">
                  {confirming === 'deactivate' ? text.deactivateConfirm : text.deleteConfirm}
                </p>
                <div className="flex gap-2">
                  <Button
                    type="button"
                    variant={confirming === 'delete' ? 'destructive' : 'outline'}
                    className="h-11"
                    disabled={busy}
                    onClick={() =>
                      void submit(() =>
                        confirming === 'deactivate'
                          ? setAccountInactiveOn(shown.id, todayISODate())
                          : deleteAccount(shown.id),
                      )
                    }
                  >
                    {confirming === 'deactivate'
                      ? busy
                        ? text.deactivating
                        : text.deactivateConfirmAction
                      : busy
                        ? text.deleting
                        : text.deleteConfirmAction}
                  </Button>
                  <Button type="button" variant="ghost" className="h-11" disabled={busy} onClick={() => setConfirming(null)}>
                    {text.cancel}
                  </Button>
                </div>
              </div>
            )}
          </div>

          {/* The 409 gets its own sentence rather than the shared error
              copy: "sudah punya riwayat - nonaktifkan, bukan hapus" tells
              her what to do next, which is the whole difference between a
              refusal and a failure. Everything else falls through to
              ErrorState. */}
          {state.status === 'error' &&
            state.error &&
            (state.error instanceof ApiError && state.error.code === 'referenced_by_other_records' ? (
              <p role="alert" className="text-sm text-attention">
                {text.deleteRefused}
              </p>
            ) : (
              <ErrorState error={state.error} />
            ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
