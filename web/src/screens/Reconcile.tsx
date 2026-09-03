import { useEffect, useState, type FormEvent } from 'react'
import { ArrowDownLeft, ArrowUpRight, ChevronDown, CircleCheck, TriangleAlert } from 'lucide-react'

import AmountInput from '@/components/money/AmountInput'
import PurposePicker from '@/components/pickers/PurposePicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { listAccounts } from '@/lib/accounts'
import { getBalances } from '@/lib/balances'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { takeReconciliation } from '@/lib/reconciliations'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Balances } from '@/lib/balances'
import type { Purpose } from '@/lib/purposes'
import type { AccountCountInput, ReconciliationDetail } from '@/lib/reconciliations'
import type { Transaction } from '@/lib/transactions'

const text = copy.reconciliation

/** How many of the most recent transactions the recent-activity list shows -
 * the same client-side slice-and-reverse of GET /api/transactions's full,
 * oldest-first list Home.tsx already does (no limit/offset param on the Go
 * handler), reused here rather than re-derived so the two screens agree on
 * what "recent" means. */
const RECENT_ACTIVITY_COUNT = 5


/** Local YYYY-MM-DD - never toISOString(), which is UTC and can read as
 * yesterday's date in WIB. Same helper as RecordTransaction.tsx's own. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

type Resolution = 'entry_added' | 'adjusted' | 'left_open'

/** One active account's count-in-progress. `touched` is what tells "she
 * hasn't looked at this location yet" apart from "she counted it and it
 * really holds zero" - AmountInput's own 0-means-blank display can't carry
 * that distinction on its own. */
interface LineState {
  touched: boolean
  actualAmount: number
  resolution: Resolution | null
  fixPurposeId: number | null
  fixDirection: 'in' | 'out'
  fixAmount: number
  fixOccurredOn: string
  fixNote: string
}

function emptyLine(mainPurposeId: number | null): LineState {
  return {
    touched: false,
    actualAmount: 0,
    resolution: null,
    fixPurposeId: mainPurposeId,
    fixDirection: 'out',
    fixAmount: 0,
    fixOccurredOn: todayISODate(),
    fixNote: '',
  }
}

/** Defaults applied the moment a resolution button is pressed: amount and
 * direction read off the just-computed gap (diff = actual - recorded, same
 * sign PRD/ReconciliationBanner.tsx already give that figure), so the
 * common case - the fix squares the gap exactly - needs no typing. Nothing
 * stops her editing them afterward; see PurposePicker/AmountInput below. */
function resolutionDefaults(resolution: Resolution, diff: number, mainPurposeId: number | null): Partial<LineState> {
  if (resolution === 'left_open') {
    return { resolution, fixAmount: 0, fixNote: '' }
  }
  return {
    resolution,
    fixDirection: diff > 0 ? 'in' : 'out',
    fixAmount: Math.abs(diff),
    fixPurposeId: mainPurposeId,
    fixOccurredOn: todayISODate(),
    fixNote: '',
  }
}

interface ReconcileData {
  accounts: Account[]
  purposes: Purpose[]
  balances: Balances
  transactions: Transaction[]
}

async function loadReconcileData(): Promise<ReconcileData> {
  const [accounts, purposes, balances, transactions] = await Promise.all([
    listAccounts(),
    listPurposes(),
    getBalances(),
    listTransactions(),
  ])
  return { accounts, purposes, balances, transactions }
}

/**
 * The reconcile screen (M6.10, PRD §7.8: "the heart of the product").
 * Enter what's actually in each active location, preview the gap against
 * the recorded balance, choose how to resolve it, submit every count in one
 * POST /api/reconciliations, then confirm from that response.
 *
 * The gap preview here is client-side arithmetic only, purely to drive
 * which resolution buttons make sense before she picks one - the server is
 * the sole judge of a "matched" claim (it rejects one whose real difference
 * isn't 0, internal/ledger/reconciliation.go:185-188) and the confirmation
 * below always renders from the POST response's own lines, never from this
 * preview. If a submit is rejected for exactly that reason - a transaction
 * landed between the count and the submit - the numbers are re-read and she
 * is asked to resolve again; nothing is silently resubmitted (see
 * handleSubmit's catch below).
 *
 * Retired accounts (`inactive_on` set) never appear in the count list - the
 * backend places no such constraint on POST /api/reconciliations's `counts`
 * itself, so this screen is the one place responsible for not asking her to
 * count a location that's been retired (M6.1).
 *
 * onDone/onCancel mirror RecordTransaction.tsx's own contract: installed
 * standalone there is no browser back button, so both "finished" and
 * "leaving without finishing" need their own way out, owned by the caller
 * (App.tsx), not this screen.
 */
export default function Reconcile({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [loadState, loadRun] = useApi<ReconcileData>()
  const [lines, setLines] = useState<Record<number, LineState>>({})
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ApiError | null>(null)
  const [staleNotice, setStaleNotice] = useState(false)
  const [detail, setDetail] = useState<ReconciliationDetail | null>(null)

  useEffect(() => {
    void loadRun(loadReconcileData)
    // loadRun is a stable useCallback (useApi.ts); fires once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadRun])

  // Seeds a LineState for every active account once the load succeeds -
  // only for accounts that don't already have one, so a reload after the
  // stale-notice path (below) never clobbers what she already typed.
  useEffect(() => {
    if (loadState.status !== 'success' || !loadState.data) return
    const mainPurposeId = loadState.data.purposes.find((p) => p.kind === 'main')?.id ?? null
    const activeAccounts = loadState.data.accounts.filter((a) => a.inactive_on === null)
    setLines((prev) => {
      const next = { ...prev }
      let changed = false
      for (const account of activeAccounts) {
        if (!next[account.id]) {
          next[account.id] = emptyLine(mainPurposeId)
          changed = true
        }
      }
      return changed ? next : prev
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadState.status, loadState.data])

  function updateLine(accountId: number, patch: Partial<LineState>) {
    setLines((prev) => ({
      ...prev,
      [accountId]: { ...prev[accountId], ...patch },
    }))
  }

  if (loadState.status === 'idle' || loadState.status === 'loading') {
    return <Loading />
  }

  if (loadState.status === 'error' || !loadState.data) {
    return loadState.error ? <ErrorState error={loadState.error} onRetry={() => void loadRun(loadReconcileData)} /> : null
  }

  const data = loadState.data
  const activeAccounts = data.accounts.filter((a) => a.inactive_on === null)
  const recordedByAccount = new Map(data.balances.accounts.map((a) => [a.id, a.balance]))
  const mainPurposeId = data.purposes.find((p) => p.kind === 'main')?.id ?? null
  const recentTransactions = data.transactions.slice(-RECENT_ACTIVITY_COUNT).reverse()
  const purposeNames = new Map(data.purposes.map((p) => [p.id, p.name]))

  function diffFor(accountId: number): number {
    const line = lines[accountId]
    if (!line) return 0
    return line.actualAmount - (recordedByAccount.get(accountId) ?? 0)
  }

  function fixIsValid(line: LineState): boolean {
    return line.fixPurposeId !== null && line.fixAmount > 0 && line.fixOccurredOn !== ''
  }

  const canSubmit =
    !submitting &&
    activeAccounts.length > 0 &&
    activeAccounts.every((account) => {
      const line = lines[account.id]
      if (!line || !line.touched) return false
      const diff = diffFor(account.id)
      if (diff === 0) return true
      if (!line.resolution) return false
      if (line.resolution === 'left_open') return true
      return fixIsValid(line)
    })

  function buildCounts(): AccountCountInput[] {
    return activeAccounts.map((account) => {
      const line = lines[account.id]
      const diff = diffFor(account.id)
      if (diff === 0) {
        return {
          accountId: account.id,
          actualAmount: line.actualAmount,
          resolution: 'matched',
        }
      }
      if (line.resolution === 'left_open') {
        return {
          accountId: account.id,
          actualAmount: line.actualAmount,
          resolution: 'left_open',
        }
      }
      const trimmedNote = line.fixNote.trim()
      return {
        accountId: account.id,
        actualAmount: line.actualAmount,
        // canSubmit already proved resolution is 'entry_added' | 'adjusted'
        // and fixIsValid here - TypeScript can't see that guarantee, so it
        // is re-asserted rather than re-checked.
        resolution: line.resolution as 'entry_added' | 'adjusted',
        fix: {
          purposeId: line.fixPurposeId as number,
          direction: line.fixDirection,
          amount: line.fixAmount,
          occurredOn: line.fixOccurredOn,
          note: trimmedNote === '' ? null : trimmedNote,
        },
      }
    })
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return

    const submitted = buildCounts()
    setSubmitting(true)
    setSubmitError(null)
    setStaleNotice(false)
    try {
      const result = await takeReconciliation(null, submitted)
      setDetail(result)
    } catch (err) {
      const apiError = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
      if (apiError.code !== 'invalid_argument') {
        setSubmitError(apiError)
        return
      }

      // `invalid_argument` does not identify what went wrong: every ledger
      // validation failure reaches the client under that one code with a
      // generic message (internal/http/errors.go:53), so a malformed fix
      // and the stale-count race are indistinguishable from the response
      // alone. Ask the ledger instead of guessing - re-read the balances
      // and see whether a line submitted as "matched" still has a
      // difference of zero. If one doesn't, the race is what happened;
      // if they all do, it was the request itself and saying otherwise
      // would send her hunting for a transaction nobody posted.
      // The re-read can fail too - the network can drop between the
      // rejected POST and this GET. Without this guard the rejection
      // escapes handleSubmit (the caller is `void handleSubmit(e)`, so it
      // becomes a silent unhandled rejection): the form would re-enable
      // with no notice at all, which reads as "my submit worked". The
      // submit is what failed, so that is what gets reported.
      let fresh: ReconcileData
      try {
        fresh = await loadReconcileData()
      } catch {
        setSubmitError(apiError)
        return
      }

      const freshRecorded = new Map(fresh.balances.accounts.map((a) => [a.id, a.balance]))
      const raced = submitted.some(
        (count) => count.resolution === 'matched' && count.actualAmount - (freshRecorded.get(count.accountId) ?? 0) !== 0,
      )

      // Committed through loadRun so the screen re-renders against the
      // ledger as it is now. Nothing she typed is discarded: `matched` is
      // never stored on a line, it is derived from a zero gap at submit
      // time, so a line the ledger moved under her turns back into a gap
      // by itself and canSubmit will require her to resolve it. Wiping the
      // resolutions she already chose for other lines would be pure loss.
      await loadRun(() => Promise.resolve(fresh), { silent: true })
      if (raced) setStaleNotice(true)
      else setSubmitError(apiError)
    } finally {
      setSubmitting(false)
    }
  }

  if (detail) {
    const accountNames = new Map(data.accounts.map((a) => [a.id, a.name]))
    return <Confirmation detail={detail} accountNames={accountNames} onDone={onDone} />
  }

  return (
    <form className="mx-auto flex w-full max-w-sm flex-col gap-6" onSubmit={(e) => void handleSubmit(e)} noValidate>
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold">{text.heading}</h1>
        <p className="text-sm text-muted-foreground">{text.intro}</p>
      </div>

      {staleNotice && (
        <p role="status" className="flex items-center gap-2 rounded-lg bg-attention-soft px-4 py-3 text-attention">
          <TriangleAlert aria-hidden="true" />
          {text.staleNotice}
        </p>
      )}

      <div className="flex flex-col gap-4">
        {activeAccounts.map((account) => (
          <AccountCountRow
            key={account.id}
            account={account}
            recorded={recordedByAccount.get(account.id) ?? 0}
            line={lines[account.id] ?? emptyLine(mainPurposeId)}
            purposes={data.purposes}
            mainPurposeId={mainPurposeId}
            disabled={submitting}
            onChange={(patch) => updateLine(account.id, patch)}
          />
        ))}
      </div>

      {/* Collapsed by default. PRD §7.8 wants recent transactions here so
          she can spot a missing or duplicated entry - but that is the
          exception, not the everyday case, and expanded it pushed the save
          and cancel buttons far below the counts they act on. A native
          <details> rather than state of our own: it is keyboard- and
          screen-reader-correct for free, and it survives a re-render (the
          stale-count path re-reads the balances mid-flow) without anything
          having to remember it was open. */}
      <details className="group flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
        <summary className="flex cursor-pointer list-none items-center justify-between gap-2 text-sm font-semibold text-muted-foreground marker:content-none">
          {copy.home.recentActivityHeading}
          <ChevronDown aria-hidden="true" className="size-4 transition-transform duration-200 group-open:rotate-180 motion-reduce:transition-none" />
        </summary>
        {recentTransactions.length === 0 ? (
          <p className="pt-3 text-muted-foreground">{copy.home.recentActivityEmpty}</p>
        ) : (
          <ul className="flex flex-col gap-2 pt-3">
            {recentTransactions.map((transaction) => (
              <li
                key={transaction.id}
                className="flex items-start justify-between gap-3 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10"
              >
                <span className="flex min-w-0 items-start gap-2">
                  {transaction.direction === 'in' ? (
                    <ArrowDownLeft aria-hidden="true" className="mt-0.5 shrink-0 text-success" />
                  ) : (
                    <ArrowUpRight aria-hidden="true" className="mt-0.5 shrink-0 text-attention" />
                  )}
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate">{purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown}</span>
                    {transaction.note && <span className="truncate text-sm text-muted-foreground">{transaction.note}</span>}
                    <span className="text-sm text-muted-foreground">{formatIsoDate(transaction.occurred_on)}</span>
                  </span>
                </span>
                <span className="tabular shrink-0 font-medium">{formatIDR(transaction.amount)}</span>
              </li>
            ))}
          </ul>
        )}
      </details>

      {submitError && <ErrorState error={submitError} />}

      <Button type="submit" size="lg" disabled={!canSubmit}>
        {submitting ? text.submitting : text.submit}
      </Button>
      <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
        {text.cancel}
      </Button>
    </form>
  )
}

/** One counted location: the recorded figure, the actual-amount input, and
 * - once she's typed something and a gap remains - the resolution choice
 * and, for entry_added/adjusted, the fix fields. Composed from the same
 * primitives RecordTransaction.tsx uses (AmountInput, PurposePicker) rather
 * than reusing that screen itself - the fix has no account field of its
 * own, the counted line already names it. */
function AccountCountRow({
  account,
  recorded,
  line,
  purposes,
  mainPurposeId,
  disabled,
  onChange,
}: {
  account: Account
  recorded: number
  line: LineState
  purposes: Purpose[]
  mainPurposeId: number | null
  disabled: boolean
  onChange: (patch: Partial<LineState>) => void
}) {
  const diff = line.touched ? line.actualAmount - recorded : 0
  const showGap = line.touched && diff !== 0
  const showMatched = line.touched && diff === 0
  const showFixFields = line.resolution === 'entry_added' || line.resolution === 'adjusted'

  // aria-labelledby rather than a <legend>: a legend renders into the
  // fieldset's border, which is exactly what made the location name look
  // like it was sitting on the card's edge. This keeps the grouping
  // semantics a screen reader needs while letting the name and the recorded
  // figure be a real card header - they are the line's identity and belong
  // together, above the input.
  return (
    <fieldset
      aria-labelledby={`reconcile-account-${account.id}`}
      className="flex flex-col gap-3 overflow-hidden rounded-lg bg-card ring-1 ring-foreground/10"
    >
      <div
        id={`reconcile-account-${account.id}`}
        className="flex items-baseline justify-between gap-3 border-b border-border bg-muted px-4 py-3"
      >
        <span className="text-base font-semibold">{account.name}</span>
        <span className="text-sm text-muted-foreground">
          {text.recordedLabel} <span className="tabular font-medium text-foreground">{formatIDR(recorded)}</span>
        </span>
      </div>

      <div className="flex flex-col gap-3 px-4 pb-4">
        <AmountInput
          id={`reconcile-actual-${account.id}`}
          label={text.actualLabel(account.name)}
          value={line.actualAmount}
          onChange={(amount) => onChange({ touched: true, actualAmount: amount })}
          disabled={disabled}
        />

        {showMatched && (
          <p className="flex items-center gap-2 text-success">
            <CircleCheck aria-hidden="true" />
            {text.matched}
          </p>
        )}

        {showGap && (
          <>
            <p className="flex items-center gap-2 text-attention">
              <TriangleAlert aria-hidden="true" />
              {text.discrepancy(formatIDR(Math.abs(diff)))}
            </p>

            <div role="group" aria-label={text.resolutionLabel} className="flex flex-col gap-2">
              {(['entry_added', 'adjusted', 'left_open'] as const).map((option) => (
                <Button
                  key={option}
                  type="button"
                  variant={line.resolution === option ? 'default' : 'outline'}
                  aria-pressed={line.resolution === option}
                  className="h-11 justify-start"
                  // Re-tapping the option already chosen does nothing.
                  // resolutionDefaults() resets the fix fields to the
                  // gap-derived defaults, so without this guard a stray second
                  // tap - easy on a phone, and these read as toggles - would
                  // silently discard a custom amount, a backdated date or a
                  // note she had already typed for this line.
                  onClick={() => {
                    if (line.resolution !== option) onChange(resolutionDefaults(option, diff, mainPurposeId))
                  }}
                  disabled={disabled}
                >
                  {text.resolutionOptions[option]}
                </Button>
              ))}
            </div>

            {showFixFields && (
              <div className="flex flex-col gap-3 rounded-lg bg-muted p-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`reconcile-fix-direction-${account.id}`}>{copy.record.directionLabel}</Label>
                  <div
                    id={`reconcile-fix-direction-${account.id}`}
                    role="group"
                    aria-label={copy.record.directionLabel}
                    className="grid grid-cols-2 gap-2"
                  >
                    <Button
                      type="button"
                      variant={line.fixDirection === 'out' ? 'default' : 'outline'}
                      aria-pressed={line.fixDirection === 'out'}
                      className="h-11"
                      onClick={() => onChange({ fixDirection: 'out' })}
                      disabled={disabled}
                    >
                      <ArrowUpRight aria-hidden="true" />
                      {copy.record.directionOut}
                    </Button>
                    <Button
                      type="button"
                      variant={line.fixDirection === 'in' ? 'default' : 'outline'}
                      aria-pressed={line.fixDirection === 'in'}
                      className="h-11"
                      onClick={() => onChange({ fixDirection: 'in' })}
                      disabled={disabled}
                    >
                      <ArrowDownLeft aria-hidden="true" />
                      {copy.record.directionIn}
                    </Button>
                  </div>
                </div>

                <PurposePicker
                  id={`reconcile-fix-purpose-${account.id}`}
                  label={text.fixPurposeLabel}
                  purposes={purposes}
                  value={line.fixPurposeId}
                  onChange={(purposeId) => onChange({ fixPurposeId: purposeId })}
                  disabled={disabled}
                />

                <AmountInput
                  id={`reconcile-fix-amount-${account.id}`}
                  label={text.fixAmountLabel}
                  value={line.fixAmount}
                  onChange={(amount) => onChange({ fixAmount: amount })}
                  disabled={disabled}
                />

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`reconcile-fix-date-${account.id}`}>{text.fixDateLabel}</Label>
                  <Input
                    id={`reconcile-fix-date-${account.id}`}
                    type="date"
                    className="h-11"
                    value={line.fixOccurredOn}
                    onChange={(event) => onChange({ fixOccurredOn: event.target.value })}
                    disabled={disabled}
                    required
                  />
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`reconcile-fix-note-${account.id}`}>{text.fixNoteLabel}</Label>
                  <textarea
                    id={`reconcile-fix-note-${account.id}`}
                    rows={2}
                    className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
                    value={line.fixNote}
                    onChange={(event) => onChange({ fixNote: event.target.value })}
                    disabled={disabled}
                  />
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </fieldset>
  )
}

/**
 * The confirmation state, rendered only from POST /api/reconciliations's own
 * response - never from the pre-submit preview above (the orchestrator's own
 * ruling). "Cocok" only when nothing is left open: a line resolved via
 * entry_added/adjusted already got its squaring fix posted in the same
 * request, so - same as ReconciliationBanner.tsx's own open-lines-only
 * count - it is not an open gap anymore even though its stored
 * difference_amount is still whatever was actually found. Only "left_open"
 * lines count toward the discrepancy figure shown here, for that reason.
 *
 * Motion: a single 200ms ease-out fade/rise on mount, nothing bouncy
 * (Design-System.md:98-100), disabled outright under prefers-reduced-motion
 * rather than merely shortened - there is no separate reduced-motion
 * duration to fall back to, only "on" or "off". No animation library: a
 * mount-triggered class swap plus Tailwind's transition utilities.
 */
function Confirmation({
  detail,
  accountNames,
  onDone,
}: {
  detail: ReconciliationDetail
  accountNames: Map<number, string>
  onDone: () => void
}) {
  const [mounted, setMounted] = useState(false)

  useEffect(() => {
    const frame = requestAnimationFrame(() => setMounted(true))
    return () => cancelAnimationFrame(frame)
  }, [])

  const hasOpenGap = detail.lines.some((line) => line.resolution === 'left_open')
  const openTotal = detail.lines
    .filter((line) => line.resolution === 'left_open')
    .reduce((sum, line) => sum + Math.abs(line.difference_amount), 0)

  return (
    <div
      className={`mx-auto flex w-full max-w-sm flex-col gap-4 transition-all duration-200 ease-out motion-reduce:transition-none motion-reduce:transform-none ${
        mounted ? 'translate-y-0 scale-100 opacity-100' : 'translate-y-1 scale-[0.98] opacity-0'
      }`}
    >
      <div
        role="status"
        className={`flex items-center gap-2 rounded-lg px-4 py-3 ${hasOpenGap ? 'bg-attention-soft text-attention' : 'bg-success-soft text-success'}`}
      >
        {hasOpenGap ? <TriangleAlert aria-hidden="true" /> : <CircleCheck aria-hidden="true" />}
        {hasOpenGap ? text.discrepancy(formatIDR(openTotal)) : text.matched}
      </div>

      <ul className="flex flex-col gap-2">
        {detail.lines.map((line) => (
          <li key={line.id} className="flex flex-col gap-1 rounded-lg bg-card p-4 ring-1 ring-foreground/10">
            <span className="font-medium">{accountNames.get(line.account_id) ?? copy.home.purposeUnknown}</span>
            <span className="tabular text-sm text-muted-foreground">
              {text.recordedLabel}: {formatIDR(line.recorded_amount)} · {formatIDR(line.actual_amount)}
            </span>
            <span className="text-sm">
              {text.resolutionOptions[line.resolution as keyof typeof text.resolutionOptions] ?? line.resolution}
            </span>
          </li>
        ))}
      </ul>

      <Button size="lg" onClick={onDone}>
        {text.backToHome}
      </Button>
    </div>
  )
}
