import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { formatIDR } from '@/lib/money'
import { createDuesTier, listDuesRates, listDuesTiers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import { useResetWhen } from '@/lib/useResetWhen'
import type { DuesRate, DuesTier } from '@/lib/setup'
import { currentPeriod } from '@/lib/dates'

const text = copy.settings.tiers

/** Every tier the fund has, each with its rate history. One shape for the
 * section: the card list reads the current price off it, and the dialog reads
 * the same rows rather than fetching them again. */
interface TiersData {
  tiers: DuesTier[]
  /** tier id -> that tier's rates, oldest first (listDuesRates' order). */
  rates: Map<number, DuesRate[]>
}

/**
 * The rate in force for `month` - the latest row at or before it, as the
 * server's GetEffectiveDuesRate reads. Null when the tier has no rate yet, or
 * none that has started: both are legal states (PRD section 6's madya), not
 * errors.
 */
function effectiveRate(rates: DuesRate[], month: string): DuesRate | null {
  let found: DuesRate | null = null
  for (const rate of rates) {
    if (rate.effective_from <= month) found = rate
  }
  return found
}

/**
 * Dues tiers and their rates, as a Pengaturan card list with dialog editing.
 * Moved here from Anggota: once the roster row shows the tier's effect, the
 * two are read together on the roster and written together only rarely.
 * Naming and pricing a tier is rare admin; the roster is an everyday list.
 *
 * **A new price is added; a mistyped one is corrected.** A new price is a new
 * row effective from a new month, because the old one still explains the
 * months it covered. A correction (PATCH of the amount, on the golongan's
 * screen) is the deliberate exception: it reprices every month the rate
 * covers and leaves a superseded row behind (ADR-040), which is why that
 * screen names the span and asks first. DELETE withdraws a rate filed against
 * the wrong month, so it can be added again at the right one.
 *
 * Rates load with the tiers rather than per card, so opening a tier's dialog
 * costs no further request and the card can show what the tier costs today.
 */
export default function DuesTiers() {
  const [state, run] = useApi<TiersData>()
  const navigate = useNavigate()
  const { value, open, close, clear } = useDialogParam()

  async function load(): Promise<TiersData> {
    const tiers = await listDuesTiers()
    const rateLists = await Promise.all(tiers.map((tier) => listDuesRates(tier.id)))
    return { tiers, rates: new Map(tiers.map((tier, index) => [tier.id, rateLists[index]])) }
  }

  useEffect(() => {
    void run(load)
  }, [run])

  function reload() {
    void run(load)
  }

  // After a write whose response was lost, re-read in place.
  const refresh = () => run(load, { silent: true })

  const tiers = state.data?.tiers ?? []
  const target = parseDialogTarget('tier', value)

  // This section owns one dialog - `tier:new` (editing lives on the golongan's
  // screen) - so any other `tier:` value is a stale link to a dialog that no
  // longer exists here. Strip it rather than leave a parameter naming nothing:
  // `clear` and never `close`, and never a `foreign` value, for the reasons
  // Locations documents at the same effect.
  useEffect(() => {
    if (target.kind === 'foreign' || target.kind === 'new') return
    if (state.status !== 'success') return
    clear()
  }, [value, target.kind, state.status, clear])

  const thisMonth = currentPeriod()

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
                  aria-label={text.cardAria(tier.name)}
                  onClick={() => navigate(`/dues-tiers?tier=${tier.id}`)}
                  className="flex min-h-11 w-full items-center justify-between gap-3 rounded-lg bg-card px-4 py-3 text-left shadow-card ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
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

      <Button type="button" variant="outline" size="lg" className="self-start" onClick={() => open('tier:new')}>
        {text.add}
      </Button>

      <AddTierDialog
        open={target.kind === 'new'}
        onClose={close}
        onAdded={() => {
          close()
          reload()
        }}
        refresh={refresh}
      />
    </section>
  )
}

function AddTierDialog({
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
  const [state, run] = useApi<DuesTier>()
  const [name, setName] = useState('')

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
        const created = await createDuesTier(trimmed)
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
            <Label htmlFor="new-tier-name">{text.nameLabel}</Label>
            <Input id="new-tier-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          {/* A duplicate name for the fund hits UNIQUE (fund_id, name) - 409
              unique_violation, answered by the shared copy. */}
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
