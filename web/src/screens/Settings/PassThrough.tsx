import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { createPassThroughPurpose, listPurposes, renamePurpose } from '@/lib/purposes'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import { useResetWhen } from '@/lib/useResetWhen'
import type { Purpose } from '@/lib/purposes'

const text = copy.settings.passThrough

/**
 * The pass-through section of the settings screen (PRD section 7.6: "record
 * money collected on behalf of the parent org (e.g. Kas Bidang)"), as a
 * card-plus-dialog (ADR-032 "Every non-posting edit is a dialog"): add and edit
 * both open a dialog addressed by `?edit=pass-through:<id>` /
 * `?edit=pass-through:new`.
 *
 * Add and rename, no delete. The name is a label - a posted transaction
 * references the purpose by id and nothing in the ledger reads the text - so a
 * typo is correctable like a location's name; but money that passed through is
 * not unsaid, so the row stays. The kind is pinned server-side and appears on
 * no form here.
 *
 * GET /api/purposes answers every tag the fund has, so the list is filtered to
 * `pass_through`: the fund's 'main' purpose is not this section's business,
 * and an incidental belongs to the section below.
 */
export default function PassThrough() {
  const [listState, listRun] = useApi<Purpose[]>()
  const { value, open, close, clear } = useDialogParam()

  useEffect(() => {
    void listRun(listPurposes)
  }, [listRun])

  function reload() {
    void listRun(listPurposes)
  }

  // After a write whose response was lost, re-read in place.
  const refresh = () => listRun(listPurposes, { silent: true })

  const passThrough = listState.data?.filter((purpose) => purpose.kind === 'pass_through') ?? []

  // 'pass-through' is this section's prefix (dialogTarget.ts) - the
  // identifier's words, hyphenated for the URL, never the Indonesian label.
  // Anything else on `?edit=` belongs to a sibling section and comes back
  // `foreign`, to be left where it is.
  const target = parseDialogTarget('pass-through', value)
  const editingPurpose = target.kind === 'edit' ? (passThrough.find((p) => p.id === target.id) ?? null) : null

  // A `pass-through:` value with an unknown or malformed id: strip it once the
  // list has loaded, rather than flash an empty dialog. Never a `foreign`
  // value, and `clear` rather than `close`, for the reasons Locations
  // documents.
  useEffect(() => {
    if (target.kind === 'foreign' || target.kind === 'new') return
    if (listState.status !== 'success') return
    if (target.kind === 'edit' && editingPurpose !== null) return
    clear()
  }, [value, target.kind, listState.status, editingPurpose, clear])

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
      ) : passThrough.length === 0 ? (
        <p className="text-muted-foreground">{text.empty}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {passThrough.map((purpose) => (
            <li key={purpose.id}>
              <button
                type="button"
                aria-label={text.editAria(purpose.name)}
                onClick={() => open(`pass-through:${purpose.id}`)}
                className="flex min-h-11 w-full items-center rounded-lg bg-card px-4 py-3 text-left shadow-card ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
              >
                <span className="min-w-0 truncate font-medium">{purpose.name}</span>
              </button>
            </li>
          ))}
        </ul>
      )}

      <Button type="button" variant="outline" size="lg" className="self-start" onClick={() => open('pass-through:new')}>
        {text.add}
      </Button>

      <AddPassThroughDialog
        open={target.kind === 'new'}
        onClose={close}
        onAdded={() => {
          close()
          reload()
        }}
        refresh={refresh}
      />
      <EditPassThroughDialog
        purpose={editingPurpose}
        open={target.kind === 'edit' && editingPurpose !== null}
        onClose={close}
        onChanged={() => {
          close()
          reload()
        }}
        refresh={refresh}
      />
    </section>
  )
}

function AddPassThroughDialog({
  open,
  onClose,
  onAdded,
  refresh,
}: {
  open: boolean
  onClose: () => void
  onAdded: () => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<Purpose>()
  const [name, setName] = useState('')

  // A fresh field every time the dialog opens, so a titipan added a moment ago
  // does not leave its name there for the next one.
  useResetWhen(open, () => {
    if (open) setName('')
  })

  const busy = state.status === 'loading'
  const trimmed = name.trim()

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (trimmed === '') return
    void run(
      async () => {
        const created = await createPassThroughPurpose(trimmed)
        onAdded()
        return created
      },
      { settle: refresh },
    )
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
          <DialogTitle>{text.add}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-pass-through-name">{text.nameLabel}</Label>
            <Input id="new-pass-through-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" size="lg" disabled={busy || trimmed === ''}>
              {busy ? text.adding : text.add}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Rename, and nothing else - no delete, deliberately: money that passed
 * through is not unsaid. `purpose` is null only while closing, the window
 * EditLocationDialog documents.
 */
function EditPassThroughDialog({
  purpose,
  open,
  onClose,
  onChanged,
  refresh,
}: {
  purpose: Purpose | null
  open: boolean
  onClose: () => void
  onChanged: () => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<Purpose>()
  const [name, setName] = useState('')

  useResetWhen(open ? purpose : null, () => {
    if (open && purpose !== null) setName(purpose.name)
  })

  const busy = state.status === 'loading'
  const trimmed = name.trim()
  const unchanged = purpose !== null && trimmed === purpose.name

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (purpose === null || trimmed === '' || unchanged) return
    void run(
      async () => {
        const updated = await renamePurpose(purpose.id, trimmed)
        onChanged()
        return updated
      },
      { settle: refresh },
    )
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
          <DialogTitle>{text.editTitle}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pass-through-name">{text.nameLabel}</Label>
            <Input id="pass-through-name" type="text" value={name} onChange={(event) => setName(event.target.value)} disabled={busy} />
          </div>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" size="lg" disabled={busy || trimmed === '' || unchanged}>
              {busy ? text.saving : text.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
