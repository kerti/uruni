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
  const navigate = useNavigate()
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

  // This section owns exactly one dialog now - `tier:new` (#285 moved
  // editing to the golongan's own screen) - so any other `tier:` value is
  // a stale link to a dialog that no longer exists here. Strip it rather
  // than leave a parameter naming nothing: `clear` and never `close`, and
  // never a `foreign` value, both for the reasons Locations documents at
  // the same effect.
  useEffect(() => {
    if (target.kind === 'foreign' || target.kind === 'new') return
    if (state.status !== 'success') return
    clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, target.kind, state.status])

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
                  aria-label={text.cardAria(tier.name)}
                  onClick={() => navigate(`/dues-tiers?tier=${tier.id}`)}
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
