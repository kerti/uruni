import { useEffect, useState, type FormEvent } from 'react'
import { ArrowDownLeft, ArrowUpRight } from 'lucide-react'

import AmountInput from '@/components/money/AmountInput'
import AccountPicker from '@/components/pickers/AccountPicker'
import PurposePicker from '@/components/pickers/PurposePicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { listPurposes } from '@/lib/purposes'
import { createTransaction } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Purpose } from '@/lib/purposes'

const text = copy.record

/** localStorage key for the last location chosen - a per-viewer convenience
 * (PRD §7.2's "location remembers last used"), never server state. */
const LAST_ACCOUNT_KEY = 'uruni:record:last-account-id'

/** Reads the remembered account id, or null if there is none, it doesn't
 * parse, or storage isn't reachable at all (a private window can throw on
 * the property access itself, not just the call). */
function readLastAccountId(): number | null {
  try {
    const raw = window.localStorage.getItem(LAST_ACCOUNT_KEY)
    if (raw === null) return null
    const parsed = Number(raw)
    return Number.isFinite(parsed) ? parsed : null
  } catch {
    return null
  }
}

function rememberAccountId(accountId: number) {
  try {
    window.localStorage.setItem(LAST_ACCOUNT_KEY, String(accountId))
  } catch {
    // Private window, storage disabled, quota - the remembered default is a
    // convenience, not a requirement; losing it costs one extra tap.
  }
}

/** Local YYYY-MM-DD - never toISOString(), which is UTC and can read as
 * yesterday's date in WIB. Same helper as Setup.tsx's own todayISODate. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

interface FormData {
  accounts: Account[]
  purposes: Purpose[]
}

/**
 * The record-transaction screen (M6.8, PRD §7.2): amount, direction,
 * location, purpose, date, optional note, posted through
 * POST /api/transactions. Photo is M6.21, not here; is_adjustment always
 * stays false on the wire - only M6.10's reconcile flow ever sets it.
 *
 * Smart defaults per PRD §7.2: location remembers the last choice
 * (localStorage, guarded - see readLastAccountId), purpose defaults to the
 * `kind: "main"` row, date defaults to today. Direction has no PRD-specified
 * default; this screen defaults to "out" (an expense) as the more frequent
 * everyday entry - a call this slice made, not one settled elsewhere.
 *
 * onRecorded is called once, after a successful post - the caller (App.tsx)
 * owns navigating back to home and showing the success message there; this
 * screen has no route knowledge of its own. onCancel is the same contract
 * for leaving without recording: installed to a home screen the app runs in
 * `display: standalone` (ADR-008, M6.7), where there is no browser back
 * button, so a form with only a submit is a room with no door.
 */
export default function RecordTransaction({
  onRecorded,
  onCancel,
}: {
  onRecorded: (direction: 'in' | 'out') => void
  onCancel: () => void
}) {
  const [loadState, loadRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()

  const [direction, setDirection] = useState<'in' | 'out'>('out')
  const [accountId, setAccountId] = useState<number | null>(null)
  const [purposeId, setPurposeId] = useState<number | null>(null)
  const [amount, setAmount] = useState(0)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  const [note, setNote] = useState('')

  async function loadFormData(): Promise<FormData> {
    const [accounts, purposes] = await Promise.all([listAccounts(), listPurposes()])
    return { accounts, purposes }
  }

  useEffect(() => {
    void loadRun(loadFormData)
    // loadRun is a stable useCallback (useApi.ts); this fires once on mount,
    // matching App.tsx's own session-probe effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadRun])

  // Applies the smart defaults once the form data has loaded: location
  // remembers the last active choice (falling back to the first active
  // account when there is none, or the remembered one was since retired),
  // purpose defaults to the fund's one `kind: "main"` row.
  useEffect(() => {
    if (loadState.status !== 'success' || !loadState.data) return

    const activeAccounts = loadState.data.accounts.filter((a) => a.inactive_on === null)
    if (accountId === null && activeAccounts.length > 0) {
      const lastId = readLastAccountId()
      const remembered = activeAccounts.find((a) => a.id === lastId)
      setAccountId((remembered ?? activeAccounts[0]).id)
    }

    if (purposeId === null) {
      const main = loadState.data.purposes.find((p) => p.kind === 'main')
      if (main) setPurposeId(main.id)
    }
    // Only re-run when the load itself changes - accountId/purposeId are
    // this effect's own output, including them would fight its one-time
    // default assignment on every keystroke that changes them.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadState.status, loadState.data])

  const submitting = submitState.status === 'loading'
  const canSubmit = amount > 0 && accountId !== null && purposeId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null || purposeId === null) return

    void submitRun(async () => {
      const trimmedNote = note.trim()
      const result = await createTransaction({
        accountId,
        purposeId,
        direction,
        amount,
        occurredOn,
        note: trimmedNote === '' ? null : trimmedNote,
      })
      rememberAccountId(accountId)
      onRecorded(direction)
      return result
    })
  }

  if (loadState.status === 'idle' || loadState.status === 'loading') {
    return <Loading />
  }

  if (loadState.status === 'error' || !loadState.data) {
    return loadState.error ? <ErrorState error={loadState.error} onRetry={() => void loadRun(loadFormData)} /> : null
  }

  return (
    <form className="mx-auto flex w-full max-w-sm flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <h1 className="text-2xl font-semibold">{text.heading}</h1>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="record-direction">{text.directionLabel}</Label>
        <div id="record-direction" role="group" aria-label={text.directionLabel} className="grid grid-cols-2 gap-2">
          <Button
            type="button"
            variant={direction === 'out' ? 'default' : 'outline'}
            aria-pressed={direction === 'out'}
            className="h-11"
            onClick={() => setDirection('out')}
          >
            <ArrowUpRight aria-hidden="true" />
            {text.directionOut}
          </Button>
          <Button
            type="button"
            variant={direction === 'in' ? 'default' : 'outline'}
            aria-pressed={direction === 'in'}
            className="h-11"
            onClick={() => setDirection('in')}
          >
            <ArrowDownLeft aria-hidden="true" />
            {text.directionIn}
          </Button>
        </div>
      </div>

      <AmountInput id="record-amount" label={text.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

      <AccountPicker
        id="record-account"
        label={text.locationLabel}
        accounts={loadState.data.accounts}
        value={accountId}
        onChange={setAccountId}
        disabled={submitting}
      />

      <PurposePicker
        id="record-purpose"
        label={text.purposeLabel}
        purposes={loadState.data.purposes}
        value={purposeId}
        onChange={setPurposeId}
        disabled={submitting}
      />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="record-date">{text.dateLabel}</Label>
        <Input
          id="record-date"
          type="date"
          className="h-11"
          value={occurredOn}
          onChange={(event) => setOccurredOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="record-note">{text.noteLabel}</Label>
        <textarea
          id="record-note"
          rows={2}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      {submitState.status === 'error' && submitState.error && <ErrorState error={submitState.error} />}

      <Button type="submit" size="lg" disabled={!canSubmit}>
        {submitting ? text.submitting : text.submit}
      </Button>
      <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
        {text.cancel}
      </Button>
    </form>
  )
}
