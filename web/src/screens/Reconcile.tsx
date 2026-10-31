import { useEffect, useState, type FormEvent, type KeyboardEvent } from 'react'
import { ArrowDownLeft, ArrowUpRight, CaretDown, CheckCircle, Equals, MagnifyingGlass } from '@phosphor-icons/react'

import DateField from '@/components/DateField'
import AmountInput from '@/components/money/AmountInput'
import PurposePicker from '@/components/pickers/PurposePicker'
import ReconciliationLines from '@/components/ReconciliationLines'
import { transferKind } from '@/components/rowLabel'
import TransactionRowLabel from '@/components/TransactionRowLabel'
import TransactionGlyph from '@/components/TransactionGlyph'
import { segmentedItemClass, segmentedTrackClass } from '@/components/segmented'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { cn } from '@/lib/utils'
import { ApiError, settleUncertainWrite } from '@/lib/api'
import { listAccounts } from '@/lib/accounts'
import { getBalances } from '@/lib/balances'
import { formatIsoDate, dateBounds, todayISODate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { fixRemainder, takeReconciliation } from '@/lib/reconciliations'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Balances } from '@/lib/balances'
import type { Purpose } from '@/lib/purposes'
import type { AccountCountInput, ReconciliationDetail } from '@/lib/reconciliations'
import type { Transaction } from '@/lib/transactions'
import type { Direction } from '@/lib/enums'

const text = copy.reconciliation

/** How many of the most recent transactions the recent-activity list shows -
 * the same client-side slice of GET /api/transactions's first page (already
 * newest-first) that Home.tsx takes, so the two screens agree on what
 * "recent" means. */
const RECENT_ACTIVITY_COUNT = 5

type Resolution = 'entry_added' | 'adjusted' | 'left_open'

/** One active account's count-in-progress. `touched` tells "she hasn't looked
 * at this location yet" apart from "she counted it and it really holds zero" -
 * AmountInput's 0-means-blank display can't carry that distinction. */
interface LineState {
  touched: boolean
  actualAmount: number
  resolution: Resolution | null
  fixPurposeId: number | null
  fixDirection: Direction
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
 * direction read off the just-computed gap (diff = actual - recorded, the
 * sign PRD/ReconciliationBanner.tsx give that figure), so the common case -
 * the fix squares the gap exactly - needs no typing. She can edit them
 * afterward. */
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
  /** What a fix may be tagged to - `?selectable=true`, so never a closed
   * amplop, which the server refuses (ADR-031). `purposes` stays the full
   * list, for naming rows already posted. */
  selectablePurposes: Purpose[]
  balances: Balances
  transactions: Transaction[]
}

async function loadReconcileData(): Promise<ReconcileData> {
  const [accounts, purposes, selectablePurposes, balances, transactionsPage] = await Promise.all([
    listAccounts(),
    listPurposes(),
    listPurposes(true),
    getBalances(),
    listTransactions(),
  ])
  return { accounts, purposes, selectablePurposes, balances, transactions: transactionsPage.transactions }
}

/**
 * The reconcile screen (PRD section 7.8: "the heart of the product"). Enter
 * what's actually in each active location, preview the gap against the
 * recorded balance, choose how to resolve it, submit every count in one POST
 * /api/reconciliations, then confirm from that response.
 *
 * The gap preview is client-side arithmetic only, purely to drive which
 * resolution buttons make sense before she picks one - the server is the sole
 * judge of a "matched" claim (it rejects one whose real difference isn't 0,
 * internal/ledger/reconciliation.go:185-188) and the confirmation always
 * renders from the POST response's lines, never from this preview. If a
 * submit is rejected for exactly that reason - a transaction landed between
 * the count and the submit - the numbers are re-read and she is asked to
 * resolve again; nothing is silently resubmitted (see handleSubmit's catch).
 *
 * Retired accounts (`inactive_on` set) never appear in the count list, and
 * every active one does: POST /api/reconciliations refuses a count naming a
 * retired location (account_inactive) and one that leaves out an active
 * location (reconciliation_location_missing), so this list and the server
 * agree on which locations a Cek kas counts.
 *
 * onDone/onCancel mirror RecordTransaction.tsx's contract: installed
 * standalone there is no browser back button, so both "finished" and
 * "leaving without finishing" need their own way out, owned by the caller
 * (App.tsx).
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
  }, [loadRun])

  // Seeds a LineState for every active account once the load succeeds - only
  // for accounts that don't have one, so a reload after the stale-notice path
  // (below) never clobbers what she typed.
  useEffect(() => {
    if (loadState.status !== 'success' || !loadState.data) return
    const mainPurposeId = loadState.data.purposes.find((p) => p.kind === 'main')?.id ?? null
    const activeAccounts = loadState.data.accounts.filter((a) => a.inactive_on === null)
    // oxlint-disable-next-line react/set-state-in-effect -- seed a count line per active account once the load succeeds
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
  // GET /api/transactions's first page is already newest-first.
  const recentTransactions = data.transactions.slice(0, RECENT_ACTIVITY_COUNT)
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
      if (!fixIsValid(line)) return false
      // A plug closes the whole gap or the server refuses it; an entry may
      // close only part of it.
      return line.resolution !== 'adjusted' || fixRemainder(diff, line.fixDirection, line.fixAmount) === 0
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
        // canSubmit already proved resolution is 'entry_added' | 'adjusted' and
        // fixIsValid here - TypeScript can't see that, so it is re-asserted
        // rather than re-checked.
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
      // A lost response may still have saved the count; re-read first.
      await settleUncertainWrite(err, () => loadRun(loadReconcileData, { silent: true }))
      const apiError = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
      // The location list went stale: a location was added or retired after
      // this screen loaded, and the server refuses a count that is not exactly
      // the active ones. Re-read so the list is right again - a new location
      // gets an empty line, a retired one drops out, nothing she typed is
      // touched - and say what happened. If the re-read fails too, the submit
      // error alone stands.
      if (apiError.code === 'account_inactive' || apiError.code === 'reconciliation_location_missing') {
        setSubmitError(apiError)
        await loadRun(loadReconcileData, { silent: true }).catch(() => undefined)
        return
      }
      if (apiError.code !== 'invalid_argument') {
        setSubmitError(apiError)
        return
      }

      // `invalid_argument` does not identify what went wrong: every ledger
      // validation failure reaches the client under that code with a generic
      // message (internal/http/errors.go:53), so a malformed fix and the
      // stale-count race are indistinguishable from the response alone. Ask
      // the ledger instead of guessing - re-read the balances and see whether a
      // line submitted as "matched" still has a difference of zero. If one
      // doesn't, the race is what happened; if they all do, it was the request
      // itself and saying otherwise would send her hunting for a transaction
      // nobody posted. The re-read can fail too (the network can drop between
      // the rejected POST and this GET); without this guard the rejection
      // escapes handleSubmit (the caller is `void handleSubmit(e)`, a silent
      // unhandled rejection) and the form re-enables with no notice, which
      // reads as "my submit worked". The submit is what failed, so that is
      // what gets reported.
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

      // Committed through loadRun so the screen re-renders against the ledger
      // as it is now. Nothing she typed is discarded: `matched` is never stored
      // on a line, it is derived from a zero gap at submit time, so a line the
      // ledger moved under her turns back into a gap by itself and canSubmit
      // requires her to resolve it. Wiping the resolutions she chose for other
      // lines would be pure loss.
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
    <form className="flex w-full flex-col gap-6" onSubmit={(e) => void handleSubmit(e)} noValidate>
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold">{text.heading}</h1>
        <p className="text-sm text-muted-foreground">{text.intro}</p>
      </div>

      {staleNotice && (
        <p role="status" className="flex items-center gap-2 rounded-lg bg-attention-soft px-4 py-3 text-attention-text">
          <MagnifyingGlass aria-hidden="true" />
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
            purposes={data.selectablePurposes}
            mainPurposeId={mainPurposeId}
            disabled={submitting}
            onChange={(patch) => updateLine(account.id, patch)}
          />
        ))}
      </div>

      {/* Collapsed by default. PRD section 7.8 wants recent transactions here so
          she can spot a missing or duplicated entry - but that is the
          exception, not the everyday case, and expanded it pushed the save
          and cancel buttons far below the counts they act on. A native
          <details> rather than state of our own: it is keyboard- and
          screen-reader-correct for free, and it survives a re-render (the
          stale-count path re-reads the balances mid-flow) without anything
          having to remember it was open. */}
      <details className="group flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10">
        <summary className="flex cursor-pointer list-none items-center justify-between gap-2 text-sm font-semibold text-muted-foreground marker:content-none">
          {copy.home.recentActivityHeading}
          <CaretDown
            aria-hidden="true"
            className="size-4 transition-transform duration-200 group-open:rotate-180 motion-reduce:transition-none"
          />
        </summary>
        {recentTransactions.length === 0 ? (
          <p className="pt-3 text-muted-foreground">{copy.home.recentActivityEmpty}</p>
        ) : (
          <ul className="flex flex-col gap-2 pt-3">
            {recentTransactions.map((transaction) => (
              <li
                key={transaction.id}
                className="flex items-start justify-between gap-3 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10"
              >
                <span className="flex min-w-0 items-start gap-2">
                  <TransactionGlyph transaction={transaction} />
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate">
                      {transferKind(transaction) ?? purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown}
                    </span>
                    {/* A transfer reads as it does in Riwayat (#738): its kind,
                        then from -> to. */}
                    {transferKind(transaction) !== null && <TransactionRowLabel transaction={transaction} bare kindShown />}
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

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
          {copy.common.back}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.submitting : text.submit}
        </Button>
      </div>
    </form>
  )
}

/** One counted location: the recorded figure, the actual-amount input, and -
 * once she's typed something and a gap remains - the resolution choice and,
 * for entry_added/adjusted, the fix fields. Composed from the primitives
 * RecordTransaction.tsx uses (AmountInput, PurposePicker) rather than that
 * screen itself - the fix has no account field, the counted line names it. */
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
  // What would still be open if she saved now, live as she types.
  const remaining = showFixFields ? fixRemainder(diff, line.fixDirection, line.fixAmount) : 0

  // aria-labelledby rather than a <legend>: a legend renders into the
  // fieldset's border, which made the location name look like it sat on the
  // card's edge. This keeps the grouping semantics a screen reader needs while
  // the name and the recorded figure form a real card header, above the input.
  return (
    <fieldset
      aria-labelledby={`reconcile-account-${account.id}`}
      className="flex flex-col gap-3 overflow-hidden rounded-lg bg-card shadow-card ring-1 ring-foreground/10"
    >
      <div
        id={`reconcile-account-${account.id}`}
        className="flex items-baseline justify-between gap-3 border-b border-border bg-muted px-4 py-3"
      >
        <span className="text-base font-semibold">{account.name}</span>
        <span className="text-sm text-muted-foreground">
          {text.recordedLabel} <span className="tabular whitespace-nowrap font-medium text-foreground">{formatIDR(recorded)}</span>
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
        {/* A small grey button, not plain ink: as a link it read as a label
            (#747). The recorded figure is in the header above, and the field
            stays hers to change. The location rides in the name, since every
            card has one; the invisible after: box is the 44px target. */}
        <Button
          type="button"
          variant="outline"
          className="relative h-9 self-start px-3 text-sm after:absolute after:inset-x-0 after:top-1/2 after:h-11 after:-translate-y-1/2 after:content-['']"
          disabled={disabled}
          onClick={() => onChange({ touched: true, actualAmount: recorded })}
        >
          <Equals aria-hidden="true" />
          {text.fillRecorded}
          <span className="sr-only">, {account.name}</span>
        </Button>

        {showMatched && (
          <p className="flex items-center gap-2 text-success">
            <CheckCircle aria-hidden="true" />
            {text.lineMatched}
          </p>
        )}

        {showGap && (
          <>
            <p className="flex items-center gap-2 text-attention-text">
              <MagnifyingGlass aria-hidden="true" />
              {text.lineDiscrepancy(formatIDR(Math.abs(diff)))}
            </p>

            <ResolutionChoice
              value={line.resolution}
              disabled={disabled}
              // Re-choosing the option already chosen does nothing.
              // resolutionDefaults() resets the fix fields to the
              // gap-derived defaults, so without this guard a stray second
              // tap would silently discard a custom amount, a backdated date
              // or a note she had typed for this line.
              onChoose={(option) => {
                if (line.resolution !== option) onChange(resolutionDefaults(option, diff, mainPurposeId))
              }}
            />

            {showFixFields && (
              <div className="flex flex-col gap-3 rounded-lg bg-muted p-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`reconcile-fix-direction-${account.id}`}>{copy.record.directionLabel}</Label>
                  <div
                    id={`reconcile-fix-direction-${account.id}`}
                    role="group"
                    aria-label={copy.record.directionLabel}
                    // bg-background: this sits inside the fix panel's bg-muted,
                    // where the track's default bg-muted would vanish.
                    className={segmentedTrackClass(2, 'bg-background')}
                  >
                    <Button
                      type="button"
                      variant={line.fixDirection === 'out' ? 'default' : 'ghost'}
                      aria-pressed={line.fixDirection === 'out'}
                      className={segmentedItemClass(line.fixDirection === 'out')}
                      onClick={() => onChange({ fixDirection: 'out' })}
                      disabled={disabled}
                    >
                      <ArrowUpRight aria-hidden="true" />
                      {copy.record.directionOut}
                    </Button>
                    <Button
                      type="button"
                      variant={line.fixDirection === 'in' ? 'default' : 'ghost'}
                      aria-pressed={line.fixDirection === 'in'}
                      className={segmentedItemClass(line.fixDirection === 'in')}
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

                <div role="status" className="flex flex-col gap-1 text-sm">
                  {remaining === 0 ? (
                    <p className="flex items-center gap-2 text-success">
                      <CheckCircle aria-hidden="true" className="size-4 shrink-0" />
                      {text.remainingNone}
                    </p>
                  ) : (
                    <>
                      <p className="tabular flex items-center gap-2 text-attention-text">
                        <MagnifyingGlass aria-hidden="true" className="size-4 shrink-0" />
                        {text.remainingGap(formatIDR(Math.abs(remaining)))}
                      </p>
                      {line.resolution === 'adjusted' && <p className="text-muted-foreground">{text.adjustedMustMatch}</p>}
                    </>
                  )}
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`reconcile-fix-date-${account.id}`}>{text.fixDateLabel}</Label>
                  <DateField
                    id={`reconcile-fix-date-${account.id}`}
                    value={line.fixOccurredOn}
                    onChange={(iso) => onChange({ fixOccurredOn: iso })}
                    bounds={dateBounds.entry()}
                    disabled={disabled}
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
 * The confirmation state, rendered only from POST /api/reconciliations's
 * response - never from the pre-submit preview above. "Cocok" only when
 * nothing is left open: a line whose fix squared the whole gap is not an open
 * gap even though its stored difference_amount is still what was found. What
 * counts toward the discrepancy figure shown here is each line's
 * remainder_amount - the whole gap for left_open, whatever an entry_added fix
 * did not cover, 0 for a plug or an exact entry - the figure
 * ReconciliationBanner.tsx reads.
 *
 * Motion: a single 200ms ease-out fade/rise on mount, nothing bouncy
 * (Design-System.md:98-100), disabled outright under prefers-reduced-motion
 * rather than shortened. No animation library: a mount-triggered class swap
 * plus Tailwind's transition utilities.
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

  const hasOpenGap = detail.lines.some((line) => line.remainder_amount !== 0)
  const openTotal = detail.lines.reduce((sum, line) => sum + Math.abs(line.remainder_amount), 0)

  return (
    <div
      className={`flex w-full flex-col gap-4 transition-all duration-200 ease-out motion-reduce:transition-none motion-reduce:transform-none ${
        mounted ? 'translate-y-0 scale-100 opacity-100' : 'translate-y-1 scale-[0.98] opacity-0'
      }`}
    >
      <div
        role="status"
        className={`flex items-center gap-2 rounded-lg px-4 py-3 ${hasOpenGap ? 'bg-attention-soft text-attention-text' : 'bg-success-soft text-success'}`}
      >
        {hasOpenGap ? <MagnifyingGlass aria-hidden="true" /> : <CheckCircle aria-hidden="true" />}
        {hasOpenGap ? (
          <span className="flex flex-col">
            <span>{text.discrepancy(formatIDR(openTotal))}</span>
            <span>{text.discrepancyPrompt}</span>
          </span>
        ) : (
          text.matched
        )}
      </div>

      <ReconciliationLines lines={detail.lines} accountNames={accountNames} />

      <Button size="lg" onClick={onDone}>
        {text.backToHome}
      </Button>
    </div>
  )
}

const resolutionChoices = ['entry_added', 'adjusted', 'left_open'] as const
type ResolutionChoiceValue = (typeof resolutionChoices)[number]

/**
 * The three ways to settle a gap, as a radio group: an empty circle for the
 * options not taken, a filled Forest one for the choice, which also gets a
 * border and tint. Hover is only a faint background, so it can no longer pass
 * for a selection (it did, as outline buttons). Nothing is chosen at first.
 * Roving tabindex: one tab stop, arrow keys move and choose.
 */
function ResolutionChoice({
  value,
  disabled,
  onChoose,
}: {
  value: LineState['resolution']
  disabled: boolean
  onChoose: (option: ResolutionChoiceValue) => void
}) {
  const move = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const step = e.key === 'ArrowDown' || e.key === 'ArrowRight' ? 1 : e.key === 'ArrowUp' || e.key === 'ArrowLeft' ? -1 : 0
    if (step === 0) return
    e.preventDefault()
    const next = (index + step + resolutionChoices.length) % resolutionChoices.length
    onChoose(resolutionChoices[next])
    const radios = e.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]')
    radios?.[next]?.focus()
  }
  const tabStop = resolutionChoices.findIndex((option) => option === value)

  return (
    <div role="radiogroup" aria-label={text.resolutionLabel} className="flex flex-col gap-2">
      {resolutionChoices.map((option, index) => {
        const checked = value === option
        return (
          <button
            key={option}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={index === (tabStop === -1 ? 0 : tabStop) ? 0 : -1}
            disabled={disabled}
            onClick={() => onChoose(option)}
            onKeyDown={(e) => move(e, index)}
            className={cn(
              'flex min-h-11 w-full items-center gap-3 rounded-xl border px-4 py-2 text-left text-base outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50',
              checked ? 'border-primary bg-primary/10 font-medium' : 'border-border bg-card hover:bg-muted',
            )}
          >
            <span
              aria-hidden="true"
              className={cn(
                'flex size-5 shrink-0 items-center justify-center rounded-full border-2',
                checked ? 'border-primary' : 'border-muted-foreground/50',
              )}
            >
              {checked && <span className="size-2.5 rounded-full bg-primary" />}
            </span>
            {text.resolutionOptions[option]}
          </button>
        )
      })}
    </div>
  )
}
