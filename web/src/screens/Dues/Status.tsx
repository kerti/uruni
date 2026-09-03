import { useEffect, useState } from 'react'
import { Circle, CircleCheck, CircleDashed, Plus } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { getDuesStatus } from '@/lib/dues'
import { formatIDR } from '@/lib/money'
import { useApi } from '@/lib/useApi'
import type { DuesStatusKind, DuesStatusRow } from '@/lib/dues'

const text = copy.dues

/** Local YYYY-MM - never toISOString(), which is UTC and can read a month
 * early in WIB. Same reasoning as Setup.tsx's own currentISOMonth and
 * Reconcile.tsx's todayISODate. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

/** Rows the "belum bayar" filter isolates - unpaid and partial both still
 * owe something for the period (PRD §7.3's "simple 'not yet paid' view").
 * A plain filter, nothing else: no reminder, nudge, notification or chase
 * affordance is rendered anywhere near it, per PRD §7.3's own explicit
 * rule. */
const UNPAID_STATUSES: readonly DuesStatusKind[] = ['unpaid', 'partial']

/** Tone classes for one status badge, per the issue's own table - no
 * `--destructive` red anywhere on this screen. `paid_in_advance` reuses
 * `paid`'s success colors but adds a ring, so it reads as a variant of
 * "lunas" rather than an unrelated state, while still being visually
 * distinguished from the plain badge as the issue asks. */
const badgeTone: Record<DuesStatusKind, string> = {
  unpaid: 'bg-muted text-muted-foreground',
  partial: 'bg-attention-soft text-attention',
  paid: 'bg-success-soft text-success',
  paid_in_advance: 'bg-success-soft text-success ring-1 ring-success',
}

/** Icons stay as calm as the tones: an empty circle for what is not paid
 * yet, a dashed one for a part-payment in progress, a check for settled.
 * Deliberately no alert triangle - Reconcile.tsx uses that shape for a
 * selisih, and an ordinary unpaid or half-paid month is not a discrepancy
 * (Design-System.md: gentle semantic states, never an alarm for a normal
 * one). */
const badgeIcon: Record<DuesStatusKind, typeof Circle> = {
  unpaid: Circle,
  partial: CircleDashed,
  paid: CircleCheck,
  paid_in_advance: CircleCheck,
}

function StatusBadge({ status }: { status: DuesStatusKind }) {
  const Icon = badgeIcon[status]
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-sm font-medium ${badgeTone[status]}`}>
      <Icon aria-hidden="true" className="size-4" />
      {text.statuses[status]}
    </span>
  )
}

/**
 * The dues status roster (M6.12, PRD §7.3): who has paid / partially paid /
 * paid in advance for one period, read straight from GET
 * /api/dues-status?period=YYYY-MM - all four statuses are the server's own
 * derivation (DuesStatusForPeriod), this screen renders them and nothing
 * more.
 *
 * A member with no dues tier is absent from the response entirely (no dues
 * obligation) - never synthesized as a row, never shown as "paid" (the
 * issue's own explicit rule). This screen has no opinion on why a row is
 * missing; it renders exactly what the server returns.
 *
 * Router-agnostic, same contract as every other screen App.tsx mounts:
 * onBack is the caller's navigate('/'), not a Link this component owns.
 * onRecordPayment is the same contract for M6.13's payment form, which is
 * reached from here rather than from a second link on home - the shape of
 * navigation as a whole is settled once alpha.4's screens exist (#177).
 *
 * `refetchKey` changes whenever App.tsx navigates back here after a payment
 * was posted, so the roster reflects it without a manual refresh - the same
 * mechanism, and the same reasoning, as Home's own refetchKey.
 */
export default function DuesStatus({
  onBack,
  onRecordPayment,
  refetchKey,
  notice,
}: {
  onBack: () => void
  onRecordPayment: () => void
  refetchKey?: unknown
  notice?: string | null
}) {
  const [period, setPeriod] = useState(currentISOMonth)
  const [unpaidOnly, setUnpaidOnly] = useState(false)
  const [state, run] = useApi<DuesStatusRow[]>()

  useEffect(() => {
    void run(() => getDuesStatus(period))
    // run is a stable useCallback (useApi.ts); period is the deliberate
    // dependency that refetches whenever the selector changes, refetchKey
    // the one that reloads it after a payment was recorded.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, period, refetchKey])

  const rows = state.data ?? []
  const visibleRows = unpaidOnly ? rows.filter((row) => UNPAID_STATUSES.includes(row.status)) : rows

  return (
    <div className="mx-auto flex w-full max-w-sm flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold">{text.heading}</h1>
      </div>

      {notice && (
        <p role="status" className="flex items-center gap-2 text-success">
          <CircleCheck aria-hidden="true" />
          {notice}
        </p>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="dues-period">{text.periodLabel}</Label>
        <Input
          id="dues-period"
          type="month"
          className="h-11"
          value={period}
          onChange={(event) => setPeriod(event.target.value)}
        />
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="size-4 rounded border-input"
          checked={unpaidOnly}
          onChange={(event) => setUnpaidOnly(event.target.checked)}
        />
        {text.unpaidFilterLabel}
      </label>

      {(state.status === 'idle' || state.status === 'loading') && <Loading />}

      {state.status === 'error' && state.error && (
        <ErrorState error={state.error} onRetry={() => void run(() => getDuesStatus(period))} />
      )}

      {state.status === 'success' && (
        <>
          {visibleRows.length === 0 ? (
            <p className="text-muted-foreground">{unpaidOnly ? text.emptyFiltered : text.empty}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {visibleRows.map((row) => (
                <li
                  key={row.member.id}
                  className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10"
                >
                  <div className="flex items-center justify-between gap-3">
                    <span className="font-medium">{row.member.name}</span>
                    <StatusBadge status={row.status} />
                  </div>
                  <div className="tabular flex items-center justify-between text-sm text-muted-foreground">
                    <span>
                      {text.owedLabel}: {formatIDR(row.owed_amount)}
                    </span>
                    <span>
                      {text.paidLabel}: {formatIDR(row.paid_amount)}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </>
      )}

      <Button type="button" size="lg" onClick={onRecordPayment}>
        <Plus aria-hidden="true" />
        {text.recordLink}
      </Button>

      <Button type="button" variant="outline" size="lg" onClick={onBack}>
        {text.back}
      </Button>
    </div>
  )
}
