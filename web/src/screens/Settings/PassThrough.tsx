import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { createPassThroughPurpose, listPurposes, renamePassThroughPurpose } from '@/lib/purposes'
import { useApi } from '@/lib/useApi'
import type { Purpose } from '@/lib/purposes'

const text = copy.settings.passThrough

/**
 * The pass-through section of the settings screen (M6.15, PRD §7.6): "record
 * money collected on behalf of the parent org (e.g. Kas Bidang)."
 *
 * Add and rename, no delete. The name is a label - a posted transaction
 * references the purpose by id and nothing in the ledger reads the text -
 * so a typo is correctable exactly like a location's name; but money that
 * passed through is not unsaid, so the row itself stays. The kind is pinned
 * server-side and appears on no form here.
 *
 * GET /api/purposes answers every tag the fund has, so the list is filtered
 * to `pass_through` here: the fund's own 'main' purpose and any incidental
 * already opened are not this section's business (incidentals are M6.19's).
 */
export default function PassThrough() {
  const [listState, listRun] = useApi<Purpose[]>()

  useEffect(() => {
    void listRun(listPurposes)
  }, [listRun])

  function reload() {
    void listRun(listPurposes)
  }

  const passThrough = listState.data?.filter((purpose) => purpose.kind === 'pass_through') ?? []

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
            <PassThroughRow key={purpose.id} purpose={purpose} onChanged={reload} />
          ))}
        </ul>
      )}

      <AddPassThrough onAdded={reload} />
    </section>
  )
}

/** One pass-through purpose, with its rename. Owns its own request state
 * so a failure on one row never blanks the others - the same shape
 * LocationRow uses. */
function PassThroughRow({ purpose, onChanged }: { purpose: Purpose; onChanged: () => void }) {
  const [state, run] = useApi<Purpose>()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(purpose.name)

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '' || trimmed === purpose.name) {
      setEditing(false)
      return
    }
    void run(async () => {
      const updated = await renamePassThroughPurpose(purpose.id, trimmed)
      setEditing(false)
      onChanged()
      return updated
    })
  }

  return (
    <li className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`pass-through-name-${purpose.id}`}>{text.nameLabel}</Label>
            <Input
              id={`pass-through-name-${purpose.id}`}
              type="text"
              value={name}
              autoFocus
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="flex gap-2">
            <Button type="submit" className="h-11" disabled={busy}>
              {busy ? text.saving : text.save}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="h-11"
              disabled={busy}
              onClick={() => {
                setName(purpose.name)
                setEditing(false)
              }}
            >
              {text.cancel}
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex items-center justify-between gap-3">
          <span className="min-w-0 truncate">{purpose.name}</span>
          <Button type="button" variant="outline" className="h-11 shrink-0" disabled={busy} onClick={() => setEditing(true)}>
            {text.edit}
          </Button>
        </div>
      )}

      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </li>
  )
}

function AddPassThrough({ onAdded }: { onAdded: () => void }) {
  const [state, run] = useApi<Purpose>()
  const [name, setName] = useState('')

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') return
    void run(async () => {
      const created = await createPassThroughPurpose(trimmed)
      setName('')
      onAdded()
      return created
    })
  }

  return (
    /* Named as a region for the reason the locations add-form is: its
       "Nama titipan" label is the same one every row being renamed shows. */
    <form aria-label={text.add} className="flex flex-col gap-3 rounded-lg border border-border p-3" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-pass-through-name">{text.nameLabel}</Label>
        <Input id="new-pass-through-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
      </div>
      <Button type="submit" className="h-11 self-start" disabled={busy || name.trim() === ''}>
        {busy ? text.adding : text.add}
      </Button>
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </form>
  )
}
