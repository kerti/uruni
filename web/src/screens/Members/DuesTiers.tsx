import { useEffect, useState, type FormEvent } from 'react'

import AmountInput from '@/components/money/AmountInput'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { createDuesRate, createDuesTier, deleteDuesRate, listDuesRates, listDuesTiers, renameDuesTier, updateDuesRate } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { DuesRate, DuesTier } from '@/lib/setup'

const text = copy.members.tiers

/** Local YYYY-MM - never toISOString(), which is UTC and can read a month
 * early in WIB. Same helper as Status.tsx's currentISOMonth. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}


/**
 * Dues tiers and their rates (M6.17, PRD §6: tiers are "a table, not an
 * enum" - the treasurer names and prices them).
 *
 * The rule this screen exists to express: **a rate is added, never repriced
 * in place.** A new price is a new row effective from a new month, because
 * the old one still explains the months it covered - `GetEffectiveDuesRate`
 * reads the latest row at or before the period being asked about, so
 * overwriting one would silently restate every settled month it touched.
 * `PATCH` here is only for a mistyped amount on the row just entered, and
 * `DELETE` for one filed against the wrong month entirely - which is what
 * makes that correctable at all, since UNIQUE (tier_id, effective_from)
 * refuses the corrected row while the wrong one stands.
 *
 * A tier with no rates yet renders plainly. It is a legal state (a tier
 * whose price is not decided), not an error.
 */
export default function DuesTiers({ onTiersChanged }: { onTiersChanged: () => void }) {
  const [state, run] = useApi<DuesTier[]>()

  useEffect(() => {
    void run(listDuesTiers)
  }, [run])

  /** Re-reads this section AND tells the screen, so the roster's tier picker
   * above stops showing a name this section just changed. */
  function reload() {
    void run(listDuesTiers)
    onTiersChanged()
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      {state.status === 'idle' || state.status === 'loading' ? (
        <Loading />
      ) : state.status === 'error' || !state.data ? (
        state.error && <ErrorState error={state.error} onRetry={reload} />
      ) : (
        <>
          {state.data.length === 0 ? (
            <p className="text-muted-foreground">{text.empty}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {state.data.map((tier) => (
                <TierRow key={tier.id} tier={tier} onRenamed={reload} />
              ))}
            </ul>
          )}
          <AddTier onAdded={reload} />
        </>
      )}
    </section>
  )
}

/** One tier: its name, its rate history, and the controls for both. The
 * rates are loaded per tier rather than in one call - there is no
 * fund-wide rates route, and a fund has a handful of tiers. */
function TierRow({ tier, onRenamed }: { tier: DuesTier; onRenamed: () => void }) {
  const [ratesState, ratesRun] = useApi<DuesRate[]>()
  const [nameState, nameRun] = useApi<DuesTier>()
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState(tier.name)

  useEffect(() => {
    void ratesRun(() => listDuesRates(tier.id))
  }, [ratesRun, tier.id])

  function reloadRates() {
    void ratesRun(() => listDuesRates(tier.id))
  }

  const renamingBusy = nameState.status === 'loading'

  function handleRename(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '' || trimmed === tier.name) {
      setRenaming(false)
      return
    }
    void nameRun(async () => {
      const updated = await renameDuesTier(tier.id, trimmed)
      setRenaming(false)
      onRenamed()
      return updated
    })
  }

  return (
    <li className="flex flex-col gap-3 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
      {renaming ? (
        <form className="flex flex-col gap-2" onSubmit={handleRename} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`tier-name-${tier.id}`}>{text.nameLabel}</Label>
            <Input
              id={`tier-name-${tier.id}`}
              type="text"
              value={name}
              autoFocus
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="flex gap-2">
            <Button type="submit" className="h-11" disabled={renamingBusy}>
              {renamingBusy ? text.saving : text.save}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="h-11"
              disabled={renamingBusy}
              onClick={() => {
                setName(tier.name)
                setRenaming(false)
              }}
            >
              {text.cancel}
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex items-center justify-between gap-3">
          <span className="min-w-0 truncate font-medium">{tier.name}</span>
          <Button type="button" variant="outline" className="h-11 shrink-0" disabled={renamingBusy} onClick={() => setRenaming(true)}>
            {text.edit}
          </Button>
        </div>
      )}
      {nameState.status === 'error' && nameState.error && <ErrorState error={nameState.error} />}

      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-medium text-muted-foreground">{text.ratesHeading}</h3>
        {ratesState.status === 'idle' || ratesState.status === 'loading' ? (
          <Loading />
        ) : ratesState.status === 'error' || !ratesState.data ? (
          ratesState.error && <ErrorState error={ratesState.error} onRetry={reloadRates} />
        ) : ratesState.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">{text.noRates}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {ratesState.data.map((rate) => (
              <RateRow key={rate.id} rate={rate} onChanged={reloadRates} />
            ))}
          </ul>
        )}
        <AddRate tierId={tier.id} onAdded={reloadRates} />
      </div>
    </li>
  )
}

/** One rate: the amount, the month it starts, and the two corrections the
 * API allows. No effective_from edit - a rate filed against the wrong month
 * is deleted and re-posted, never moved. */
function RateRow({ rate, onChanged }: { rate: DuesRate; onChanged: () => void }) {
  const [state, run] = useApi<unknown>()
  const [editing, setEditing] = useState(false)
  const [amount, setAmount] = useState(rate.amount)

  const busy = state.status === 'loading'

  async function submit(fn: () => Promise<unknown>) {
    await run(fn)
    onChanged()
  }

  function handleEdit(event: FormEvent) {
    event.preventDefault()
    if (amount === rate.amount || amount <= 0) {
      setEditing(false)
      return
    }
    void submit(async () => {
      const updated = await updateDuesRate(rate.id, amount)
      setEditing(false)
      return updated
    })
  }

  return (
    <li className="flex flex-col gap-2 rounded-lg bg-background px-3 py-2 ring-1 ring-foreground/10">
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={handleEdit} noValidate>
          <AmountInput id={`rate-amount-${rate.id}`} label={text.rateAmountLabel} value={amount} onChange={setAmount} disabled={busy} />
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
                setAmount(rate.amount)
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
            <span className="tabular font-medium">{formatIDR(rate.amount)}</span>
            <span className="shrink-0 text-sm text-muted-foreground">{text.effectiveFrom(formatPeriod(rate.effective_from))}</span>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={() => setEditing(true)}>
              {text.editRate}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="h-11 text-destructive"
              disabled={busy}
              onClick={() => void submit(() => deleteDuesRate(rate.id))}
            >
              {busy ? text.deletingRate : text.deleteRate}
            </Button>
          </div>
        </>
      )}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </li>
  )
}

/** A new price, effective from a month. Defaults to the current month: a
 * rate decided today normally starts today's month, and backdating one is a
 * deliberate act (#187 - a fund's history starts at adoption). */
function AddRate({ tierId, onAdded }: { tierId: number; onAdded: () => void }) {
  const [state, run] = useApi<DuesRate>()
  const [amount, setAmount] = useState(0)
  const [effectiveFrom, setEffectiveFrom] = useState(currentISOMonth)

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (amount <= 0 || effectiveFrom === '') return
    void run(async () => {
      const created = await createDuesRate(tierId, amount, effectiveFrom)
      setAmount(0)
      setEffectiveFrom(currentISOMonth())
      onAdded()
      return created
    })
  }

  return (
    <form aria-label={text.addRate} className="flex flex-col gap-2 rounded-lg border border-border p-3" onSubmit={handleSubmit} noValidate>
      <AmountInput id={`new-rate-amount-${tierId}`} label={text.rateAmountLabel} value={amount} onChange={setAmount} disabled={busy} />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`new-rate-from-${tierId}`}>{text.effectiveFromLabel}</Label>
        {/* type="month" - the wire format is YYYY-MM and so is this input's
            value, so there is nothing to convert either way. */}
        <Input
          id={`new-rate-from-${tierId}`}
          type="month"
          value={effectiveFrom}
          onChange={(event) => setEffectiveFrom(event.target.value)}
        />
      </div>
      <Button type="submit" className="h-11 self-start" disabled={busy || amount <= 0}>
        {busy ? text.addingRate : text.addRate}
      </Button>
      {/* A second rate for the same tier and month hits UNIQUE (tier_id,
          effective_from) and comes back 409 unique_violation, which the
          shared error copy already answers. */}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </form>
  )
}

function AddTier({ onAdded }: { onAdded: () => void }) {
  const [state, run] = useApi<DuesTier>()
  const [name, setName] = useState('')

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') return
    void run(async () => {
      const created = await createDuesTier(trimmed)
      setName('')
      onAdded()
      return created
    })
  }

  return (
    <form aria-label={text.add} className="flex flex-col gap-3 rounded-lg border border-border p-3" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-tier-name">{text.nameLabel}</Label>
        <Input id="new-tier-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
      </div>
      <Button type="submit" className="h-11 self-start" disabled={busy || name.trim() === ''}>
        {busy ? text.adding : text.add}
      </Button>
      {/* A duplicate name for the fund hits UNIQUE (fund_id, name) - 409
          unique_violation, answered by the shared copy. */}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </form>
  )
}
