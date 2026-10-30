import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ChevronDown, Circle, CircleCheck, CircleDashed } from 'lucide-react'

import MonthField from '@/components/MonthField'
import { segmentedItemClass, segmentedTrackClass } from '@/components/segmented'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import MemberPayments from '@/screens/Dues/MemberPayments'
import DuesStatusGrid from '@/screens/Dues/StatusGrid'
import { getDuesStatus } from '@/lib/dues'
import { currentPeriod, formatPeriod, periodBounds } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { useApi } from '@/lib/useApi'
import type { DuesStatusKind, DuesStatusRow } from '@/lib/dues'

const text = copy.dues
const gridText = copy.dues.grid
const historyText = copy.dues.history
const arrearsText = copy.members.roster

/** A period read from the URL: "YYYY-MM" inside the selector's bounds, else
 * null (the caller falls back to this month). */
function periodFromUrl(value: string | null): string | null {
  if (value === null || !/^\d{4}-(0[1-9]|1[0-2])$/.test(value)) return null
  const { min, max } = periodBounds.status()
  return value >= min && value <= max ? value : null
}

/** Rows the "belum bayar" filter isolates - unpaid and partial both still owe
 * something for the period (PRD section 7.3's "simple 'not yet paid' view").
 * A plain filter: no reminder, nudge, notification or chase affordance
 * anywhere near it, per PRD section 7.3's explicit rule. */
const UNPAID_STATUSES: readonly DuesStatusKind[] = ['unpaid', 'partial']

/** Tone classes for one status badge - no `--destructive` red anywhere on this
 * screen. `paid_in_advance` reuses `paid`'s success colors but adds a ring, so
 * it reads as a variant of "lunas" while staying distinguishable. */
const badgeTone: Record<DuesStatusKind, string> = {
  unpaid: 'bg-muted text-muted-foreground',
  partial: 'bg-attention-soft text-attention-text',
  paid: 'bg-success-soft text-success',
  paid_in_advance: 'bg-success-soft text-success ring-1 ring-success',
}

/** Icons stay as calm as the tones: an empty circle for what is not paid yet,
 * a dashed one for a part-payment in progress, a check for settled. No alert
 * triangle - Reconcile.tsx uses that shape for a selisih, and an ordinary
 * unpaid or half-paid month is not a discrepancy (Design-System.md: gentle
 * semantic states). */
const badgeIcon: Record<DuesStatusKind, typeof Circle> = {
  unpaid: Circle,
  partial: CircleDashed,
  paid: CircleCheck,
  paid_in_advance: CircleCheck,
}

function StatusBadge({ status }: { status: DuesStatusKind }) {
  const Icon = badgeIcon[status]
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-sm font-medium whitespace-nowrap ${badgeTone[status]}`}
    >
      <Icon aria-hidden="true" className="size-4" />
      {text.statuses[status]}
    </span>
  )
}

/**
 * The dues status roster (PRD section 7.3): who has paid / partially paid /
 * paid in advance for one period, read from GET /api/dues-status?period=YYYY-MM
 * - all four statuses are the server's derivation (DuesStatusForPeriod); this
 * screen renders them and nothing more.
 *
 * A member with no dues tier is absent from the response (no dues obligation)
 * - never synthesized as a row, never shown as "paid". This screen has no
 * opinion on why a row is missing; it renders what the server returns.
 *
 * Router-agnostic, like every screen App.tsx mounts: onBack is the caller's
 * navigate back to Riwayat's Iuran tab, not a Link this component owns.
 * onRecordPayment is the same contract for the payment form, reached from
 * here rather than from a second link on home.
 *
 * `refetchKey` changes whenever App.tsx navigates back here after a payment
 * was posted, so the roster reflects it without a manual refresh - as Home's
 * does.
 */
export default function DuesStatus({
  onBack,
  onRecordPayment,
  refetchKey,
  notice,
}: {
  onBack: () => void
  /** Opens the payment form. A card's "Catat iuran" names the member and the
   * card's month; the bottom button names neither. */
  onRecordPayment: (target?: { memberId: number; period: string }) => void
  refetchKey?: unknown
  notice?: string | null
}) {
  // The view lives in the URL (?view=grid), a pushed entry like Reimbursements'
  // ?show=all: back returns to the view she came from. The list is the default.
  const [searchParams, setSearchParams] = useSearchParams()
  const showGrid = searchParams.get('view') === 'grid'
  function showView(next: 'list' | 'grid') {
    if ((next === 'grid') === showGrid) return
    setSearchParams((current) => {
      const params = new URLSearchParams(current)
      if (next === 'grid') params.set('view', 'grid')
      else params.delete('view')
      return params
    })
  }

  // The month lives in the URL too (?period=YYYY-MM), so coming back from the
  // payment form lands on the month she left. Replaced, not pushed: browsing
  // months is not a trail she walks back through.
  const period = periodFromUrl(searchParams.get('period')) ?? currentPeriod()
  function setPeriod(next: string) {
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current)
        if (next === currentPeriod()) params.delete('period')
        else params.set('period', next)
        return params
      },
      { replace: true },
    )
  }
  const [unpaidOnly, setUnpaidOnly] = useState(false)
  // Which member's payment history is open - one at a time, and never open
  // across a period change, since the history is period-scoped.
  const [expandedMemberId, setExpandedMemberId] = useState<number | null>(null)
  const [state, run] = useApi<DuesStatusRow[]>()

  useEffect(() => {
    void run(() => getDuesStatus(period))
    // run is a stable useCallback (useApi.ts); period is the dependency that
    // refetches when the selector changes, refetchKey the one that reloads it
    // after a payment was recorded.
  }, [run, period, refetchKey])

  const rows = state.data ?? []
  const visibleRows = unpaidOnly ? rows.filter((row) => UNPAID_STATUSES.includes(row.status)) : rows

  return (
    <div className="flex w-full flex-col gap-6">
      {notice && (
        <p role="status" className="flex items-center gap-2 text-success">
          <CircleCheck aria-hidden="true" />
          {notice}
        </p>
      )}

      <div role="group" aria-label={gridText.heading} className={segmentedTrackClass(2)}>
        <Button
          type="button"
          variant={showGrid ? 'ghost' : 'default'}
          aria-pressed={!showGrid}
          className={segmentedItemClass(!showGrid)}
          onClick={() => showView('list')}
        >
          {gridText.viewOneMonth}
        </Button>
        <Button
          type="button"
          variant={showGrid ? 'default' : 'ghost'}
          aria-pressed={showGrid}
          className={segmentedItemClass(showGrid)}
          onClick={() => showView('grid')}
        >
          {gridText.viewTwelveMonths}
        </Button>
      </div>

      {showGrid ? (
        <DuesStatusGrid refetchKey={refetchKey} />
      ) : (
        <>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="dues-period">{text.periodLabel}</Label>
            <MonthField
              id="dues-period"
              value={period}
              onChange={(next) => {
                setPeriod(next)
                setExpandedMemberId(null)
              }}
              bounds={periodBounds.status()}
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
                      className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10"
                    >
                      <div className="flex items-center justify-between gap-3">
                        {/* The chip has its own line under the name: beside it,
                        a phone truncated the name and wrapped the status
                        badge. */}
                        <span className="flex min-w-0 flex-col items-start gap-1">
                          <span className="max-w-full truncate font-medium">{row.member.name}</span>
                          {row.member.arrears_months > 0 && (
                            <span className="tabular shrink-0 rounded-full bg-attention-soft px-2 py-0.5 text-xs font-medium text-attention-text">
                              {arrearsText.arrearsBadge(row.member.arrears_months)}
                            </span>
                          )}
                        </span>
                        {/* Paid ahead with a known end (#357): the badge stays a
                        plain "Lunas" and the month moves to its own line
                        below, so neither the name nor the badge wraps. */}
                        <StatusBadge status={row.status === 'paid_in_advance' && row.paid_through ? 'paid' : row.status} />
                      </div>
                      <div className="tabular flex items-center justify-between text-sm text-muted-foreground">
                        <span>
                          {text.owedLabel}: {formatIDR(row.owed_amount)}
                        </span>
                        <span>
                          {text.paidLabel}: {formatIDR(row.paid_amount)}
                        </span>
                      </div>
                      {row.status === 'paid_in_advance' && row.paid_through && (
                        <p className="text-right text-sm text-success">{text.paidThrough(formatPeriod(row.paid_through))}</p>
                      )}

                      {UNPAID_STATUSES.includes(row.status) && (
                        <Button
                          type="button"
                          variant="outline"
                          size="lg"
                          className="w-full"
                          aria-label={text.recordForMemberAria(row.member.name)}
                          onClick={() => onRecordPayment({ memberId: row.member.id, period })}
                        >
                          {text.recordForMember}
                        </Button>
                      )}

                      {/* A disclosure, not a link: the panel opens in place
                      inside this card, so the control names the panel and
                      carries its state (aria-expanded + aria-controls), and
                      the chevron turns with it. Full-width and `lg` for the
                      44px touch target Design-System.md sets. */}
                      <Button
                        type="button"
                        variant="ghost"
                        size="lg"
                        className="-mx-2 justify-between px-2"
                        aria-expanded={expandedMemberId === row.member.id}
                        aria-controls={`dues-history-${row.member.id}`}
                        onClick={() => setExpandedMemberId(expandedMemberId === row.member.id ? null : row.member.id)}
                      >
                        {historyText.title}
                        {/* The chevron turns on its own composited layer
                        (transform-gpu + will-change), not as a transform on
                        the SVG itself: iOS Safari rasterizes an SVG once at
                        its pre-transform size, so rotating the element
                        directly re-uses that bitmap and the strokes crawl.
                        Wrapping it and promoting the wrapper is what keeps
                        the turn smooth on a phone. */}
                        <span
                          className="flex size-4 transform-gpu items-center justify-center transition-transform duration-200 ease-out will-change-transform data-[open=true]:rotate-180"
                          data-open={expandedMemberId === row.member.id}
                        >
                          <ChevronDown aria-hidden="true" />
                        </span>
                      </Button>

                      {expandedMemberId === row.member.id && (
                        <div id={`dues-history-${row.member.id}`} className="animate-reveal rounded-lg bg-muted/30 px-3 py-3">
                          <MemberPayments
                            memberId={row.member.id}
                            period={period}
                            onReversed={() => void run(() => getDuesStatus(period))}
                          />
                        </div>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </>
      )}

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" variant="outline" size="lg" onClick={onBack}>
          {text.back}
        </Button>
        <Button type="button" size="lg" onClick={() => onRecordPayment()}>
          {text.recordLink}
        </Button>
      </div>
    </div>
  )
}
