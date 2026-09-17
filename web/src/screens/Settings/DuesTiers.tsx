import { useEffect, useState, type FormEvent } from 'react'

import AmountInput from '@/components/money/AmountInput'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatPeriod } from '@/lib/dates'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { formatIDR } from '@/lib/money'
import {
  createDuesRate,
  createDuesTier,
  deleteDuesRate,
  deleteDuesTier,
  listDuesRates,
  listDuesTiers,
  renameDuesTier,
  updateDuesRate,
} from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { DuesRate, DuesTier } from '@/lib/setup'

const text = copy.settings.tiers

/** Local YYYY-MM - never toISOString(), which is UTC and can read a month
 * early in WIB. Same helper as Status.tsx's currentISOMonth. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

/** Every tier the fund has, each with its own rate history. One shape for
 * the whole section: the card list reads the current price off it, and the
 * dialog reads the same rows rather than fetching them a second time. */
interface TiersData {
  tiers: DuesTier[]
  /** tier id -> that tier's rates, oldest first (listDuesRates' own order). */
  rates: Map<number, DuesRate[]>
}

/**
 * The rate in force for `month` - the latest row at or before it, which is
 * exactly what the server's own GetEffectiveDuesRate reads. Null when the
 * tier has no rate yet, or none that has started: both are legal states
 * (PRD section 6's madya), not errors.
 */
function effectiveRate(rates: DuesRate[], month: string): DuesRate | null {
  let found: DuesRate | null = null
  for (const rate of rates) {
    if (rate.effective_from <= month) found = rate
  }
  return found
}

/**
 * Dues tiers and their rates (M6.17, moved here from Anggota by M6.31 -
 * #232), as a Pengaturan card list with dialog editing.
 *
 * The move supersedes M6.16/M6.17's own placement, whose reason was that "a
 * member's tier is set on the member, so the two are read and edited in one
 * sitting". That stops holding once the roster row shows the tier's effect
 * (#233): the two are then read together on the roster and written together
 * only rarely. Naming and pricing a tier is rare admin; the roster is an
 * everyday list.
 *
 * The rule this section exists to express is unchanged by the move: **a rate
 * is added, never repriced in place.** A new price is a new row effective
 * from a new month, because the old one still explains the months it covered
 * - GetEffectiveDuesRate reads the latest row at or before the period being
 * asked about, so overwriting one would silently restate every settled month
 * it touched. PATCH is only for a mistyped amount on the row just entered,
 * and DELETE for one filed against the wrong month entirely - which is what
 * makes that correctable at all, since UNIQUE (tier_id, effective_from)
 * refuses the corrected row while the wrong one stands.
 *
 * Rates load with the tiers rather than per card, so opening a tier's dialog
 * costs no further request and the card can show what the tier costs today.
 */
export default function DuesTiers() {
  const [state, run] = useApi<TiersData>()
  const { value, open, close, clear } = useDialogParam()

  async function load(): Promise<TiersData> {
    const tiers = await listDuesTiers()
    const rateLists = await Promise.all(tiers.map((tier) => listDuesRates(tier.id)))
    return { tiers, rates: new Map(tiers.map((tier, index) => [tier.id, rateLists[index]])) }
  }

  useEffect(() => {
    void run(load)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run])

  function reload() {
    void run(load)
  }

  const tiers = state.data?.tiers ?? []
  const target = parseDialogTarget('tier', value)
  const editingTier = target.kind === 'edit' ? (tiers.find((tier) => tier.id === target.id) ?? null) : null

  // A `tier:` id naming nothing: strip it once the list has loaded, rather
  // than flash an empty dialog. Never a `foreign` value - that belongs to a
  // sibling section on this same screen - and `clear` rather than `close`,
  // both for the reasons Locations documents at the same effect.
  useEffect(() => {
    if (target.kind === 'foreign' || target.kind === 'new') return
    if (state.status !== 'success') return
    if (target.kind === 'edit' && editingTier !== null) return
    clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, target.kind, state.status, editingTier])

  const thisMonth = currentISOMonth()

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
      ) : tiers.length === 0 ? (
        <p className="text-muted-foreground">{text.empty}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {tiers.map((tier) => {
            const current = effectiveRate(state.data?.rates.get(tier.id) ?? [], thisMonth)
            return (
              <li key={tier.id}>
                <button
                  type="button"
                  aria-label={text.editAria(tier.name)}
                  onClick={() => open(`tier:${tier.id}`)}
                  className="flex min-h-11 w-full items-center justify-between gap-3 rounded-lg bg-card px-4 py-3 text-left ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
                >
                  <span className="min-w-0 truncate font-medium">{tier.name}</span>
                  {/* What it costs this month, which is the one number she
                      came to check. A tier with no rate yet says so rather
                      than showing a zero it does not mean. */}
                  <span className="shrink-0 text-sm text-muted-foreground">
                    {current === null ? text.noRates : <span className="tabular">{formatIDR(current.amount)}</span>}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}

      <Button type="button" variant="outline" className="h-11 self-start" onClick={() => open('tier:new')}>
        {text.add}
      </Button>

      <AddTierDialog
        open={target.kind === 'new'}
        onClose={close}
        onAdded={() => {
          close()
          reload()
        }}
      />
      <EditTierDialog
        tier={editingTier}
        rates={editingTier === null ? [] : (state.data?.rates.get(editingTier.id) ?? [])}
        open={target.kind === 'edit' && editingTier !== null}
        onClose={close}
        onChanged={reload}
        onDeleted={() => {
          close()
          reload()
        }}
      />
    </section>
  )
}

function AddTierDialog({ open, onClose, onAdded }: { open: boolean; onClose: () => void; onAdded: () => void }) {
  const [state, run] = useApi<DuesTier>()
  const [name, setName] = useState('')

  useEffect(() => {
    if (open) setName('')
  }, [open])

  const busy = state.status === 'loading'
  const trimmed = name.trim()

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (trimmed === '') return
    void run(async () => {
      const created = await createDuesTier(trimmed)
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
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.add}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-tier-name">{text.nameLabel}</Label>
            <Input id="new-tier-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          {/* A duplicate name for the fund hits UNIQUE (fund_id, name) - 409
              unique_violation, answered by the shared copy. */}
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" className="h-11" disabled={busy || trimmed === ''}>
              {busy ? text.adding : text.add}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * One tier's whole life in one dialog: its name, its rate history, a new
 * rate, and deleting the tier itself.
 *
 * Deleting confirms inline in this dialog's own footer, never a second
 * dialog and never window.confirm() - the same shape EditLocationDialog
 * uses, and the reason dialog.tsx forbids nesting. A tier a member is in
 * comes back 409 referenced_by_other_records from that member's own foreign
 * key, and the copy names why rather than restating the wire message
 * (ADR-014).
 */
function EditTierDialog({
  tier,
  rates,
  open,
  onClose,
  onChanged,
  onDeleted,
}: {
  tier: DuesTier | null
  rates: DuesRate[]
  open: boolean
  onClose: () => void
  onChanged: () => void
  onDeleted: () => void
}) {
  const [nameState, nameRun] = useApi<DuesTier>()
  const [deleteState, deleteRun] = useApi<void>()
  const [name, setName] = useState('')
  const [confirmingDelete, setConfirmingDelete] = useState(false)

  useEffect(() => {
    if (open && tier !== null) {
      setName(tier.name)
      setConfirmingDelete(false)
    }
  }, [open, tier])

  const busy = nameState.status === 'loading' || deleteState.status === 'loading'
  const trimmed = name.trim()
  const unchanged = tier !== null && trimmed === tier.name

  function handleRename(event: FormEvent) {
    event.preventDefault()
    if (tier === null || trimmed === '' || unchanged) return
    void nameRun(async () => {
      const updated = await renameDuesTier(tier.id, trimmed)
      onChanged()
      return updated
    })
  }

  function handleDelete() {
    if (tier === null) return
    void deleteRun(async () => {
      await deleteDuesTier(tier.id)
      onDeleted()
    })
  }

  // useApi's error is optional even in the 'error' state, so this narrows to
  // a value both branches below can read.
  const deleteError = deleteState.status === 'error' ? (deleteState.error ?? null) : null

  /** The 409 the server answers when a member still references this tier -
   * named in Indonesian, never the English wire message (ADR-014). */
  function deleteErrorText(code: string): string {
    if (code === 'referenced_by_other_records') return text.deleteRefused
    return copy.common.errors[code as keyof typeof copy.common.errors] ?? copy.common.unknownError
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

        <form className="flex flex-col gap-3" onSubmit={handleRename} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tier-name">{text.nameLabel}</Label>
            <Input id="tier-name" type="text" value={name} onChange={(event) => setName(event.target.value)} disabled={busy} />
          </div>
          {nameState.status === 'error' && nameState.error && <ErrorState error={nameState.error} />}
          {!unchanged && trimmed !== '' && (
            <Button type="submit" className="h-11 self-start" disabled={busy}>
              {nameState.status === 'loading' ? text.saving : text.save}
            </Button>
          )}
        </form>

        <div className="flex flex-col gap-2">
          <h3 className="text-sm font-medium text-muted-foreground">{text.ratesHeading}</h3>
          {rates.length === 0 ? (
            <p className="text-sm text-muted-foreground">{text.noRates}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {rates.map((rate) => (
                <RateRow key={rate.id} rate={rate} onChanged={onChanged} />
              ))}
            </ul>
          )}
          {tier !== null && <AddRate tierId={tier.id} onAdded={onChanged} />}
        </div>

        {/* The consequence, named just above the button that does it -
            terracotta, not alarm-red, the same as Lokasi's own confirms. */}
        {confirmingDelete && <p className="text-sm text-attention">{text.deleteConfirm}</p>}
        {deleteError !== null && (
          <p role="alert" className="text-sm text-attention">
            {deleteErrorText(deleteError.code)}
          </p>
        )}

        <DialogFooter className="mt-1">
          {confirmingDelete ? (
            <>
              <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={() => setConfirmingDelete(false)}>
                {text.cancel}
              </Button>
              <Button type="button" className="h-11 bg-attention text-attention-foreground hover:bg-attention/90" disabled={busy} onClick={handleDelete}>
                {deleteState.status === 'loading' ? text.deleting : text.deleteConfirmAction}
              </Button>
            </>
          ) : (
            <>
              <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={onClose}>
                {copy.common.close}
              </Button>
              <Button
                type="button"
                variant="ghost"
                className="h-11 text-attention"
                disabled={busy}
                onClick={() => setConfirmingDelete(true)}
              >
                {text.delete}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
