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
import {
  createDuesRate,
  deleteDuesRate,
  deleteDuesTier,
  listDuesRates,
  listDuesTiers,
  renameDuesTier,
  updateDuesRate,
} from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { DuesRate, DuesTier } from '@/lib/setup'

const text = copy.settings.tiers

/** Local YYYY-MM - never toISOString(), which is UTC and can read a month
 * early in WIB. Same helper as Status.tsx's currentISOMonth. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

/** One golongan and its rates, fetched together - the screen needs both and
 * neither is useful here without the other. */
interface TierData {
  tier: DuesTier | null
  rates: DuesRate[]
}

/**
 * One golongan's own screen (#285), reached as `/dues-tiers?tier=<id>` from
 * the Golongan card in Pengaturan - the same shape ADR-032 kept for the
 * envelope detail view at `/incidentals?purpose=<id>` when it retired that
 * list screen (#263), with the same single entry point and the same
 * redirect to Pengaturan when the parameter names nothing.
 *
 * It is a screen and not a dialog, and that is the whole point of the
 * change. A golongan holds a name and a price history, and #232 put both in
 * one dialog: a rename form with its own Save, a per-rate form with its own
 * Save/Cancel, a bordered add-tarif card, and a footer belonging to none of
 * them. Splitting that into two dialogs was tried and rejected for two
 * reasons a second dialog cannot fix. The rate history is an unbounded list,
 * and ADR-032 draws the line itself - "tabs when every panel is an
 * unbounded list, sections when the panels are short and read together" - so
 * a growing price history in a modal is on the wrong side of the app's own
 * classification. And correcting a row inside a dialog gives her two
 * different backs: a Batal that returns to the list, and an Esc or backdrop
 * tap that closes everything and discards the correction, with nothing on
 * screen saying which is about to happen. That ambiguity is exactly what
 * ADR-032's "no nested dialogs, ever" exists to prevent, reached without
 * technically nesting anything.
 *
 * On a screen there is one way back - the control at the top, and the
 * footer nav - so a row's correction is an ordinary inline form.
 *
 * Why only a rate's amount is correctable: a new price is a new row
 * effective from a new month, because the old row still explains the months
 * it covered - GetEffectiveDuesRate reads the latest row at or before the
 * period being asked about, so overwriting one would silently restate every
 * settled month it touched. PATCH is for a mistyped amount on the row just
 * entered, and DELETE for one filed against the wrong month entirely, which
 * is what makes that correctable at all: UNIQUE (tier_id, effective_from)
 * refuses the corrected row while the wrong one stands.
 *
 * The rate history is deliberately not paged. A golongan carries a handful
 * of rates for all of 0.x, and building paging for a volume that does not
 * exist is the creep CLAUDE.md's prime directive names. What the screen
 * buys is somewhere paging can go if that ever changes, which a dialog
 * does not have.
 */
export default function DuesTierScreen({ tierId, onBack }: { tierId: number; onBack: () => void }) {
  const [state, run] = useApi<TierData>()

  async function load(): Promise<TierData> {
    const [tiers, rates] = await Promise.all([listDuesTiers(), listDuesRates(tierId)])
    return { tier: tiers.find((candidate) => candidate.id === tierId) ?? null, rates }
  }

  useEffect(() => {
    void run(load)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, tierId])

  function reload() {
    void run(load)
  }

  const tier = state.data?.tier ?? null

  // A `tier` id naming no golongan - a stale bookmark, or one deleted from
  // another device - goes back to Pengaturan, the screen that owns the
  // list. Same answer App.tsx gives a missing or unparseable parameter,
  // just decided one step later because only a fetch can tell.
  useEffect(() => {
    if (state.status !== 'success') return
    if (tier === null) onBack()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.status, tier])

  if (state.status === 'idle' || state.status === 'loading') return <Loading />
  if (state.status === 'error' || !state.data) {
    return state.error ? <ErrorState error={state.error} onRetry={reload} /> : null
  }
  if (tier === null) return null

  return (
    <div className="flex flex-col gap-6">
      <Button type="button" variant="outline" size="lg" className="self-start" onClick={onBack}>
        {text.backToSettings}
      </Button>

      <h1 className="text-2xl font-semibold">{tier.name}</h1>

      <TierName tier={tier} onRenamed={reload} />
      <TierRates tier={tier} rates={state.data.rates} onChanged={reload} />
      <DeleteTier tier={tier} onDeleted={onBack} />
    </div>
  )
}

/** The name, on its own, with one action row. */
function TierName({ tier, onRenamed }: { tier: DuesTier; onRenamed: () => void }) {
  const [state, run] = useApi<DuesTier>()
  const [name, setName] = useState(tier.name)

  useEffect(() => {
    setName(tier.name)
  }, [tier.name])

  const busy = state.status === 'loading'
  const trimmed = name.trim()
  const unchanged = trimmed === tier.name

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (trimmed === '' || unchanged) return
    void run(async () => {
      const updated = await renameDuesTier(tier.id, trimmed)
      onRenamed()
      return updated
    })
  }

  return (
    <form className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
      <h2 className="text-base font-semibold">{text.nameHeading}</h2>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="tier-name">{text.nameLabel}</Label>
        <Input
          id="tier-name"
          type="text"
          value={name}
          onChange={(event) => setName(event.target.value)}
          disabled={busy}
        />
      </div>
      {/* A duplicate name for the fund hits UNIQUE (fund_id, name) - 409
          unique_violation, answered by the shared copy. */}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
      <Button type="submit" className="h-11 self-end" disabled={busy || trimmed === '' || unchanged}>
        {busy ? text.saving : text.saveName}
      </Button>
    </form>
  )
}

/** The price history, and the fields for adding to it. */
function TierRates({ tier, rates, onChanged }: { tier: DuesTier; rates: DuesRate[]; onChanged: () => void }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-base font-semibold">{text.ratesHeading}</h2>
      {rates.length === 0 ? (
        <p className="text-sm text-muted-foreground">{text.noRates}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {rates.map((rate) => (
            <RateRow key={rate.id} rate={rate} onChanged={onChanged} />
          ))}
        </ul>
      )}
      <AddRate tierId={tier.id} onAdded={onChanged} />
    </section>
  )
}

/** One rate: what it costs, the month it starts, and the two corrections the
 * API allows. Correcting happens in the row itself - there is one back on
 * this screen, so an inline form here has no escape route to compete with
 * (#285). */
function RateRow({ rate, onChanged }: { rate: DuesRate; onChanged: () => void }) {
  const [state, run] = useApi<unknown>()
  const [editing, setEditing] = useState(false)
  const [amount, setAmount] = useState(rate.amount)

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (amount === rate.amount || amount <= 0) {
      setEditing(false)
      return
    }
    void run(async () => {
      const updated = await updateDuesRate(rate.id, amount)
      setEditing(false)
      onChanged()
      return updated
    })
  }

  return (
    <li className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
          <AmountInput
            id={`rate-amount-${rate.id}`}
            label={text.rateAmountLabel}
            value={amount}
            onChange={setAmount}
            disabled={busy}
          />
          {/* One row, primary right (#235). Deleting lives here rather than
              on the reading row: a rate is removed to re-file it against
              the right month, which is a correction, not a glance. */}
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              className="h-11 text-attention"
              disabled={busy}
              onClick={() => void run(async () => {
                await deleteDuesRate(rate.id)
                setEditing(false)
                onChanged()
              })}
            >
              {busy ? text.deletingRate : text.deleteRate}
            </Button>
            <Button
              type="button"
              variant="outline"
              className="ml-auto h-11"
              disabled={busy}
              onClick={() => {
                setAmount(rate.amount)
                setEditing(false)
              }}
            >
              {text.cancel}
            </Button>
            <Button type="submit" className="h-11" disabled={busy}>
              {busy ? text.saving : text.saveRate}
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 flex-col">
            <span className="tabular font-medium">{formatIDR(rate.amount)}</span>
            <span className="text-sm text-muted-foreground">{text.effectiveFrom(formatPeriod(rate.effective_from))}</span>
          </span>
          <Button
            type="button"
            variant="outline"
            className="h-11 shrink-0"
            disabled={busy}
            onClick={() => setEditing(true)}
          >
            {text.editRate}
          </Button>
        </div>
      )}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </li>
  )
}

/** A new price, effective from a month. Defaults to the current month: a
 * rate decided today normally starts today's month, and backdating one is a
 * deliberate act (#187 - a fund's history starts at adoption), which is why
 * the month stays a free field rather than being pinned to today. PRD
 * section 7.1's live-arrears escape hatch depends on being able to set it in
 * the past. */
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
    <form aria-label={text.addRate} className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
      <h3 className="text-sm font-medium text-muted-foreground">{text.addRateHeading}</h3>
      <AmountInput
        id={`new-rate-amount-${tierId}`}
        label={text.rateAmountLabel}
        value={amount}
        onChange={setAmount}
        disabled={busy}
      />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`new-rate-from-${tierId}`}>{text.effectiveFromLabel}</Label>
        {/* type="month" - the wire format is YYYY-MM and so is this input's
            value, so there is nothing to convert either way. */}
        <Input
          id={`new-rate-from-${tierId}`}
          type="month"
          value={effectiveFrom}
          onChange={(event) => setEffectiveFrom(event.target.value)}
          disabled={busy}
        />
      </div>
      {/* A second rate for the same tier and month hits UNIQUE (tier_id,
          effective_from) and comes back 409 unique_violation, which the
          shared error copy already answers. */}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
      <Button type="submit" className="h-11 self-end" disabled={busy || amount <= 0}>
        {busy ? text.addingRate : text.addRate}
      </Button>
    </form>
  )
}

/** Deleting the golongan itself, confirmed in place - never window.confirm(),
 * and on a screen there is no dialog to nest one inside. A golongan a member
 * is in comes back 409 referenced_by_other_records from that member's own
 * foreign key, and the copy names why rather than restating the wire message
 * (ADR-014). */
function DeleteTier({ tier, onDeleted }: { tier: DuesTier; onDeleted: () => void }) {
  const [state, run] = useApi<void>()
  const [confirming, setConfirming] = useState(false)

  const busy = state.status === 'loading'
  const error = state.status === 'error' ? (state.error ?? null) : null

  function errorTextFor(code: string): string {
    if (code === 'referenced_by_other_records') return text.deleteRefused
    return copy.common.errors[code as keyof typeof copy.common.errors] ?? copy.common.unknownError
  }

  return (
    <section className="flex flex-col gap-2 border-t border-border pt-4">
      {/* The consequence, named just above the button that does it -
          terracotta, not alarm-red, the same as Lokasi's own confirms. */}
      {confirming && <p className="text-sm text-attention">{text.deleteConfirm}</p>}
      {error !== null && (
        <p role="alert" className="text-sm text-attention">
          {errorTextFor(error.code)}
        </p>
      )}
      {confirming ? (
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" className="ml-auto h-11" disabled={busy} onClick={() => setConfirming(false)}>
            {text.cancel}
          </Button>
          <Button
            type="button"
            className="h-11 bg-attention text-attention-foreground hover:bg-attention/90"
            disabled={busy}
            onClick={() =>
              void run(async () => {
                await deleteDuesTier(tier.id)
                onDeleted()
              })
            }
          >
            {busy ? text.deleting : text.deleteConfirmAction}
          </Button>
        </div>
      ) : (
        <Button type="button" variant="ghost" className="h-11 self-start text-attention" onClick={() => setConfirming(true)}>
          {text.delete}
        </Button>
      )}
    </section>
  )
}
