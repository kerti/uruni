import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { createAccount, deleteAccount, listAccounts, setAccountInactiveOn, updateAccount } from '@/lib/accounts'
import { useApi } from '@/lib/useApi'
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

/**
 * The locations section of the settings screen (M6.15): the whole
 * account-lifecycle surface M6.1 built, finally reachable.
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

  useEffect(() => {
    // listRun is a stable useCallback (useApi.ts), so this fires once.
    void listRun(listAccounts)
  }, [listRun])

  function reload() {
    void listRun(listAccounts)
  }

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
            <LocationRow key={account.id} account={account} onChanged={reload} />
          ))}
        </ul>
      )}

      <AddLocation onAdded={reload} />
    </section>
  )
}

/**
 * One location, with its three lifecycle actions. Each row owns its own
 * request state so a failure on one location never blanks the others - the
 * list itself is only re-read once a write succeeds.
 */
function LocationRow({ account, onChanged }: { account: Account; onChanged: () => void }) {
  const [state, run] = useApi<unknown>()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(account.name)
  const [kind, setKind] = useState<'cash' | 'bank'>(account.kind === 'bank' ? 'bank' : 'cash')

  const busy = state.status === 'loading'
  const inactive = account.inactive_on !== null

  async function submit(fn: () => Promise<unknown>) {
    await run(fn)
    onChanged()
  }

  function handleEdit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    // Nothing changed, or nothing left to change it to: close the form
    // rather than spend a request saying so.
    if (trimmed === '' || (trimmed === account.name && kind === account.kind)) {
      setEditing(false)
      return
    }
    void submit(async () => {
      const updated = await updateAccount(account.id, {
        name: trimmed === account.name ? undefined : trimmed,
        kind: kind === account.kind ? undefined : kind,
      })
      setEditing(false)
      return updated
    })
  }

  return (
    <li className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={handleEdit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`location-name-${account.id}`}>{text.nameLabel}</Label>
            <Input
              id={`location-name-${account.id}`}
              type="text"
              value={name}
              autoFocus
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`location-kind-${account.id}`}>{text.kindLabel}</Label>
            <Select value={kind} onValueChange={(next) => setKind(next as 'cash' | 'bank')}>
              <SelectTrigger id={`location-kind-${account.id}`} aria-label={text.kindLabel}>
                <SelectValue>{kind === 'bank' ? text.kindBank : text.kindCash}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="cash">{text.kindCash}</SelectItem>
                <SelectItem value="bank">{text.kindBank}</SelectItem>
              </SelectContent>
            </Select>
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
                setName(account.name)
                setKind(account.kind)
                setEditing(false)
              }}
            >
              {text.cancel}
            </Button>
          </div>
        </form>
      ) : (
        <>
          <div className="flex items-baseline justify-between gap-3">
            <span className="min-w-0 truncate font-medium">{account.name}</span>
            <span className="shrink-0 text-sm text-muted-foreground">
              {account.kind === 'bank' ? text.kindBank : text.kindCash}
            </span>
          </div>
          {inactive && <span className="text-sm text-muted-foreground">{text.inactiveBadge}</span>}
          {/* Three controls at the 44px minimum, not the `sm` variant: this
              is a phone screen and every one of them is a thumb target. */}
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={() => setEditing(true)}>
              {text.edit}
            </Button>
            {inactive ? (
              <Button
                type="button"
                variant="outline"
                className="h-11"
                disabled={busy}
                onClick={() => void submit(() => setAccountInactiveOn(account.id, null))}
              >
                {busy ? text.reinstating : text.reinstate}
              </Button>
            ) : (
              <Button
                type="button"
                variant="outline"
                className="h-11"
                disabled={busy}
                onClick={() => void submit(() => setAccountInactiveOn(account.id, todayISODate()))}
              >
                {busy ? text.deactivating : text.deactivate}
              </Button>
            )}
            <Button
              type="button"
              variant="ghost"
              className="h-11 text-destructive"
              disabled={busy}
              onClick={() => void submit(() => deleteAccount(account.id))}
            >
              {busy ? text.deleting : text.delete}
            </Button>
          </div>
        </>
      )}

      {/* The 409 gets its own sentence rather than the shared error copy:
          "sudah punya riwayat - nonaktifkan, bukan hapus" tells her what to
          do next, which is the whole difference between a refusal and a
          failure. Everything else falls through to ErrorState. */}
      {state.status === 'error' &&
        state.error &&
        (state.error instanceof ApiError && state.error.code === 'referenced_by_other_records' ? (
          <p role="alert" className="text-sm text-attention">
            {text.deleteRefused}
          </p>
        ) : (
          <ErrorState error={state.error} />
        ))}
    </li>
  )
}

/** Adding a location after setup (#78: setup asks for the first batch, this
 * is what adds to them afterward). */
function AddLocation({ onAdded }: { onAdded: () => void }) {
  const [state, run] = useApi<Account>()
  const [kind, setKind] = useState<'cash' | 'bank'>('cash')
  const [name, setName] = useState('')

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') return
    void run(async () => {
      const created = await createAccount(kind, trimmed)
      setName('')
      onAdded()
      return created
    })
  }

  return (
    /* aria-label names the form as a region, so "Nama lokasi" here and the
       same label on a row being renamed are distinguishable - to a screen
       reader and to a test alike. */
    <form
      aria-label={text.add}
      className="flex flex-col gap-3 rounded-lg border border-border p-3"
      onSubmit={handleSubmit}
      noValidate
    >
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
      <Button type="submit" className="h-11 self-start" disabled={busy || name.trim() === ''}>
        {busy ? text.adding : text.add}
      </Button>
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </form>
  )
}
