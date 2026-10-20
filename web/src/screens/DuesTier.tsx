import { ArrowLeft } from 'lucide-react'
import { useCallback, useEffect, useState, type FormEvent } from 'react'

import MonthField from '@/components/MonthField'
import AmountInput from '@/components/money/AmountInput'
import SectionDivider from '@/components/SectionDivider'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatPeriod, periodBounds } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { createDuesRate, deleteDuesRate, deleteDuesTier, listDuesRates, listDuesTiers, renameDuesTier, updateDuesRate } from '@/lib/setup'
import { rateSpan } from '@/lib/rateSpan'
import { useApi } from '@/lib/useApi'
import { useResetWhen } from '@/lib/useResetWhen'
import type { DuesRate, DuesTier } from '@/lib/setup'

const text = copy.settings.tiers

/** Local YYYY-MM - never toISOString(), which is UTC and can read a month
 * early in WIB. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

/** One golongan and its rates, fetched together - the screen needs both. */
interface TierData {
  tier: DuesTier | null
  rates: DuesRate[]
}

/**
 * One golongan's screen, reached as `/dues-tiers?tier=<id>` from the Golongan
 * card in Pengaturan - the shape ADR-032 kept for the envelope detail view at
 * `/incidentals?purpose=<id>`, with the same single entry point and the same
 * redirect to Pengaturan when the parameter names nothing.
 *
 * It is a screen and not a dialog because a golongan holds a name and a price
 * history, and splitting them across dialogs does not fix two problems. The
 * rate history is an unbounded list, and ADR-032 draws the line itself -
 * "tabs when every panel is an unbounded list, sections when the panels are
 * short and read together" - so a growing history in a modal is on the wrong
 * side. And correcting a row inside a dialog gives her two different backs: a
 * Batal that returns to the list, and an Esc or backdrop tap that closes
 * everything and discards the correction, with nothing saying which is about
 * to happen - the ambiguity ADR-032's "no nested dialogs, ever" exists to
 * prevent. On a screen there is one way back, so a row's correction is an
 * ordinary inline form.
 *
 * Why only a rate's amount is correctable: a new price is a new row
 * effective from a new month, because the old row still explains the months it
 * covered. PATCH is for a mistyped amount (ADR-040): it appends a correction
 * that deliberately reprices every month the rate covers, from its own month to
 * the month before the next rate that stands, so the screen names that span
 * and asks first. DELETE withdraws a rate filed against the wrong month, which
 * is how a wrong month is fixed: delete it, add one at the right month. Either
 * way the row that stood is replaced by a new id, so the list is reloaded after
 * every write and a stale id is answered with dues_rate_superseded.
 *
 * The rate history is deliberately not paged. A golongan carries a handful of
 * rates for all of 0.x, and paging for a volume that does not exist is the
 * creep CLAUDE.md's prime directive names.
 */
export default function DuesTierScreen({ tierId, onBack }: { tierId: number; onBack: () => void }) {
  const [state, run] = useApi<TierData>()

  const load = useCallback(async (): Promise<TierData> => {
    const [tiers, rates] = await Promise.all([listDuesTiers(), listDuesRates(tierId)])
    return { tier: tiers.find((candidate) => candidate.id === tierId) ?? null, rates }
  }, [tierId])

  useEffect(() => {
    void run(load)
  }, [run, load])

  function reload() {
    void run(load)
  }

  // After a write whose response was lost, re-read in place (silent, so the
  // form about to show the uncertain message is not unmounted).
  const refresh = () => run(load, { silent: true })

  const tier = state.data?.tier ?? null

  // A `tier` id naming no golongan - a stale bookmark, or one deleted from
  // another device - goes back to Pengaturan, the screen that owns the list.
  // The answer App.tsx gives a missing or unparseable parameter, decided one
  // step later because only a fetch can tell.
  useEffect(() => {
    if (state.status !== 'success') return
    if (tier === null) onBack()
    // oxlint-disable-next-line react/exhaustive-deps -- onBack is an inline arrow from the router; leave once when the load lands, not on every parent render
  }, [state.status, tier])

  if (state.status === 'idle' || state.status === 'loading') return <Loading />
  if (state.status === 'error' || !state.data) {
    return state.error ? <ErrorState error={state.error} onRetry={reload} /> : null
  }
  if (tier === null) return null

  return (
    <div className="flex flex-col gap-6">
      {/* A way back, not an action (#319) - the same quiet link as an
          envelope's own detail screen. */}
      <Button type="button" variant="link" className="h-auto min-h-11 self-start p-0 text-muted-foreground" onClick={onBack}>
        <ArrowLeft aria-hidden="true" />
        {text.backToSettings}
      </Button>

      <h1 className="text-xl font-semibold">{tier.name}</h1>

      {/* Three independent sections, a hairline between each (#319). */}
      <TierName tier={tier} onRenamed={reload} refresh={refresh} />
      <SectionDivider />
      <TierRates tier={tier} rates={state.data.rates} onChanged={reload} refresh={refresh} />
      <SectionDivider />
      <DeleteTier tier={tier} onDeleted={onBack} refresh={refresh} />
    </div>
  )
}

/** The name, on its own, with one action row. */
function TierName({ tier, onRenamed, refresh }: { tier: DuesTier; onRenamed: () => void; refresh: () => Promise<void> }) {
  const [state, run] = useApi<DuesTier>()
  const [name, setName] = useState(tier.name)
  // Follows a rename that lands from the server (the reload after saving) -
  // during render, never in an effect that could fire after her first
  // keystroke and put the old name back under her typing.
  useResetWhen(tier.name, () => setName(tier.name))

  const busy = state.status === 'loading'
  const trimmed = name.trim()
  const unchanged = trimmed === tier.name

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (trimmed === '' || unchanged) return
    void run(
      async () => {
        const updated = await renameDuesTier(tier.id, trimmed)
        onRenamed()
        return updated
      },
      { settle: refresh },
    )
  }

  return (
    <form className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
      <h2 className="text-base font-semibold">{text.nameHeading}</h2>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="tier-name">{text.nameLabel}</Label>
        <Input id="tier-name" type="text" value={name} onChange={(event) => setName(event.target.value)} disabled={busy} />
      </div>
      {/* A duplicate name for the fund hits UNIQUE (fund_id, name) - 409
          unique_violation, answered by the shared copy. */}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
      <Button type="submit" size="lg" className="self-end" disabled={busy || trimmed === '' || unchanged}>
        {busy ? text.saving : text.saveName}
      </Button>
    </form>
  )
}

/** The price history, and the fields for adding to it. */
function TierRates({
  tier,
  rates,
  onChanged,
  refresh,
}: {
  tier: DuesTier
  rates: DuesRate[]
  onChanged: () => void
  refresh: () => Promise<void>
}) {
  // A row superseded from another screen unmounts when the list reloads,
  // taking any message of its own with it, so the notice lives here.
  const [superseded, setSuperseded] = useState(false)

  // The reload is silent (refresh): the screen stays mounted, so this notice
  // survives it. A loud one would drop the whole screen to Loading.
  function handleSuperseded() {
    setSuperseded(true)
    void refresh()
  }

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-base font-semibold">{text.ratesHeading}</h2>
      {superseded && (
        <p role="alert" className="text-sm text-attention-text">
          {copy.common.errors.dues_rate_superseded}
        </p>
      )}
      {rates.length === 0 ? (
        <p className="text-sm text-muted-foreground">{text.noRates}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {rates.map((rate) => (
            <RateRow
              key={rate.id}
              rate={rate}
              rates={rates}
              onChanged={() => {
                setSuperseded(false)
                onChanged()
              }}
              onSuperseded={handleSuperseded}
              refresh={refresh}
            />
          ))}
        </ul>
      )}
      <AddRate tierId={tier.id} onAdded={onChanged} refresh={refresh} />
    </section>
  )
}

/** One rate: what it costs, the month it starts, and the two corrections the
 * API allows. Correcting happens in the row - there is one back on this
 * screen, so an inline form has no escape route to compete with. */
function RateRow({
  rate,
  rates,
  onChanged,
  onSuperseded,
  refresh,
}: {
  rate: DuesRate
  rates: DuesRate[]
  onChanged: () => void
  onSuperseded: () => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<unknown>()
  const [editing, setEditing] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [amount, setAmount] = useState(rate.amount)

  const busy = state.status === 'loading'

  // A refused write on an id that no longer stands: the section reloads the
  // list and shows the notice (this row is about to be replaced).
  const supersededError = state.status === 'error' && state.error?.code === 'dues_rate_superseded'
  useEffect(() => {
    if (supersededError) onSuperseded()
    // oxlint-disable-next-line react/exhaustive-deps -- once per refusal; onSuperseded is a fresh closure every render
  }, [supersededError])

  // The months this correction would reprice, named before it is sent.
  const span = rateSpan(rates, rate)
  const from = formatPeriod(span.from)
  const spanLabel =
    span.until === null ? text.rateSpanOpen(from) : span.until === span.from ? from : text.rateSpanRange(from, formatPeriod(span.until))

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (amount === rate.amount || amount <= 0) {
      setEditing(false)
      return
    }
    // A changed amount asks first; the request goes on the confirm.
    setConfirming(true)
  }

  function applyCorrection() {
    void run(
      async () => {
        const updated = await updateDuesRate(rate.id, amount)
        setEditing(false)
        setConfirming(false)
        onChanged()
        return updated
      },
      { settle: refresh },
    )
  }

  return (
    <li className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10">
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
          <AmountInput
            id={`rate-amount-${rate.id}`}
            label={text.rateAmountLabel}
            value={amount}
            onChange={setAmount}
            disabled={busy || confirming}
          />
          {confirming && (
            <p className="text-sm text-attention-text">{text.rateCorrectConfirm(formatIDR(rate.amount), formatIDR(amount), spanLabel)}</p>
          )}
          {/* One row, primary right (#235). Deleting lives here rather than
              on the reading row: a rate is removed to re-file it against
              the right month, which is a correction, not a glance. */}
          {confirming ? (
            <div className="flex items-center gap-2">
              <Button type="button" variant="outline" size="lg" className="ml-auto" disabled={busy} onClick={() => setConfirming(false)}>
                {text.cancel}
              </Button>
              <Button type="button" size="lg" disabled={busy} onClick={applyCorrection}>
                {busy ? text.rateCorrecting : text.rateCorrectConfirmAction}
              </Button>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="ghost"
                size="lg"
                className="text-attention-text"
                disabled={busy}
                onClick={() =>
                  void run(
                    async () => {
                      await deleteDuesRate(rate.id)
                      setEditing(false)
                      onChanged()
                    },
                    { settle: refresh },
                  )
                }
              >
                {busy ? text.deletingRate : text.deleteRate}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="lg"
                className="ml-auto"
                disabled={busy}
                onClick={() => {
                  setAmount(rate.amount)
                  setEditing(false)
                }}
              >
                {text.cancel}
              </Button>
              <Button type="submit" size="lg" disabled={busy}>
                {busy ? text.saving : text.saveRate}
              </Button>
            </div>
          )}
        </form>
      ) : (
        <div className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 flex-col">
            <span className="tabular font-medium">{formatIDR(rate.amount)}</span>
            <span className="text-sm text-muted-foreground">{text.effectiveFrom(formatPeriod(rate.effective_from))}</span>
          </span>
          <Button type="button" variant="outline" size="lg" className="shrink-0" disabled={busy} onClick={() => setEditing(true)}>
            {text.editRate}
          </Button>
        </div>
      )}
      {state.status === 'error' && state.error && !supersededError && <ErrorState error={state.error} />}
    </li>
  )
}

/** A new price, effective from a month. Defaults to the current month: a rate
 * decided today normally starts this month, and backdating one is deliberate
 * (a fund's history starts at adoption), which is why the month stays a free
 * field. PRD section 7.1's live-arrears escape hatch depends on setting it in
 * the past. */
function AddRate({ tierId, onAdded, refresh }: { tierId: number; onAdded: () => void; refresh: () => Promise<void> }) {
  const [state, run] = useApi<DuesRate>()
  const [amount, setAmount] = useState(0)
  const [effectiveFrom, setEffectiveFrom] = useState(currentISOMonth)

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (amount <= 0 || effectiveFrom === '') return
    void run(
      async () => {
        const created = await createDuesRate(tierId, amount, effectiveFrom)
        setAmount(0)
        setEffectiveFrom(currentISOMonth())
        onAdded()
        return created
      },
      { settle: refresh },
    )
  }

  return (
    <form aria-label={text.addRate} className="flex flex-col gap-2" onSubmit={handleSubmit} noValidate>
      <h3 className="text-sm font-medium text-muted-foreground">{text.addRateHeading}</h3>
      <AmountInput id={`new-rate-amount-${tierId}`} label={text.rateAmountLabel} value={amount} onChange={setAmount} disabled={busy} />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`new-rate-from-${tierId}`}>{text.effectiveFromLabel}</Label>
        {/* MonthField speaks YYYY-MM, the wire format, so there is nothing
            to convert either way (#197). */}
        <MonthField
          id={`new-rate-from-${tierId}`}
          value={effectiveFrom}
          onChange={setEffectiveFrom}
          bounds={periodBounds.rate()}
          disabled={busy}
        />
      </div>
      {/* A second rate for the same tier and month hits UNIQUE (tier_id,
          effective_from) and comes back 409 unique_violation, which the
          shared error copy already answers. */}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
      <Button type="submit" size="lg" className="self-end" disabled={busy || amount <= 0}>
        {busy ? text.addingRate : text.addRate}
      </Button>
    </form>
  )
}

/** Deleting the golongan itself, confirmed in place - never window.confirm(),
 * and on a screen there is no dialog to nest one inside. A golongan a member
 * is in comes back 409 referenced_by_other_records from that member's foreign
 * key, and the copy names why rather than restating the wire message
 * (ADR-014). */
function DeleteTier({ tier, onDeleted, refresh }: { tier: DuesTier; onDeleted: () => void; refresh: () => Promise<void> }) {
  const [state, run] = useApi<void>()
  const [confirming, setConfirming] = useState(false)

  const busy = state.status === 'loading'
  const error = state.status === 'error' ? (state.error ?? null) : null

  function errorTextFor(code: string): string {
    if (code === 'referenced_by_other_records') return text.deleteRefused
    return copy.common.errors[code as keyof typeof copy.common.errors] ?? copy.common.unknownError
  }

  return (
    <section className="flex flex-col gap-2">
      {/* The consequence, named just above the button that does it -
          terracotta, not alarm-red, the same as Lokasi's own confirms. */}
      {confirming && <p className="text-sm text-attention-text">{text.deleteConfirm}</p>}
      {error !== null && (
        <p role="alert" className="text-sm text-attention-text">
          {errorTextFor(error.code)}
        </p>
      )}
      {confirming ? (
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" size="lg" className="ml-auto" disabled={busy} onClick={() => setConfirming(false)}>
            {text.cancel}
          </Button>
          <Button
            type="button"
            size="lg"
            className="bg-attention-text text-primary-foreground hover:bg-attention-text/90"
            disabled={busy}
            onClick={() =>
              void run(
                async () => {
                  await deleteDuesTier(tier.id)
                  onDeleted()
                },
                { settle: refresh },
              )
            }
          >
            {busy ? text.deleting : text.deleteConfirmAction}
          </Button>
        </div>
      ) : (
        <Button type="button" variant="ghost" size="lg" className="self-start text-attention-text" onClick={() => setConfirming(true)}>
          {text.delete}
        </Button>
      )}
    </section>
  )
}
