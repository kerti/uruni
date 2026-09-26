import { useEffect, useState, type FormEvent } from 'react'
import { ArrowDownLeft, ArrowLeftRight, ArrowUpDown, ArrowUpRight } from 'lucide-react'

import AmountInput from '@/components/money/AmountInput'
import AccountPicker from '@/components/pickers/AccountPicker'
import PurposePicker from '@/components/pickers/PurposePicker'
import ReceiptPicker from '@/components/ReceiptPicker'
import { segmentedStackedItemClass, segmentedTrackClass } from '@/components/segmented'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { getBalances } from '@/lib/balances'
import { formatIDR } from '@/lib/money'
import { uploadReceipt } from '@/lib/receipts'
import { postTransfer } from '@/lib/transfers'
import { listPurposes } from '@/lib/purposes'
import { createTransaction } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Balances } from '@/lib/balances'
import type { Purpose } from '@/lib/purposes'

const text = copy.record

/** localStorage key for the last location chosen - a per-viewer convenience
 * (PRD section 7.2's "location remembers last used"), never server state. */
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

/**
 * What the form is recording. 'in' and 'out' post a transaction; 'transfer'
 * posts a pair through POST /api/transfers (#235) - money that is neither
 * entering nor leaving the fund, only changing place, which is why it gets
 * its own direction rather than being an out with a special purpose.
 */
export type Direction = 'in' | 'out' | 'transfer'

interface FormData {
  accounts: Account[]
  purposes: Purpose[]
  /** Every purpose's current balance (#266). Fetched with the form rather
   * than on demand so the Titipan warning below can appear as she types,
   * without a request per keystroke. */
  balances: Balances
}

/**
 * The record-transaction screen (M6.8, PRD section 7.2): amount, direction,
 * location, purpose, date, optional note, and - as of M6.21/#154 - an
 * optional photo of the nota, posted through POST /api/transactions.
 * is_adjustment always stays false on the wire - only M6.10's reconcile
 * flow ever sets it.
 *
 * The photo travels a second request behind the transaction itself: the
 * upload route needs the posted row's own id (POST
 * /api/transactions/{id}/receipts), so it can only go out once
 * createTransaction's response comes back. A transfer has no photo field -
 * it moves money between the fund's own locations, with no nota to
 * document, unlike an ordinary in/out. If that second request fails, the
 * transaction itself is NOT rolled back (ADR-011: a receipt lives in its
 * own table precisely so it can be attached, or fail to attach, without
 * touching the immutable row it names) - onRecorded's second argument says
 * so, and App.tsx shows the "tersimpan, tapi belum terunggah" message
 * instead of the ordinary success line.
 *
 * Smart defaults per PRD section 7.2: location remembers the last choice
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
 *
 * `initialPurposeId`, when given, seeds the purpose default with that id
 * instead of the fund's `kind: "main"` row - M6.19's incidentals screen
 * reuses this form entirely for contributions/disbursements by navigating
 * here with the envelope's own purpose already chosen (App.tsx's `/record`
 * route reads it from a `purpose` search param), rather than duplicating
 * the field set. Absent, behaviour is unchanged.
 */
export default function RecordTransaction({
  onRecorded,
  onCancel,
  initialPurposeId,
}: {
  /** photoFailed is true only when a photo was picked and the parent
   * transaction posted successfully but the receipt upload itself failed
   * (#154) - never set for a transfer, which offers no photo field. */
  onRecorded: (direction: Direction, photoFailed?: boolean) => void
  onCancel: () => void
  initialPurposeId?: number | null
}) {
  const [loadState, loadRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()

  const [direction, setDirection] = useState<Direction>('out')
  const [accountId, setAccountId] = useState<number | null>(null)
  // Only used by 'transfer': where the money lands. The single accountId
  // above is where it leaves from, which is what it already means for an
  // ordinary out - so the field she has been using keeps its meaning and
  // only the second one is new.
  const [toAccountId, setToAccountId] = useState<number | null>(null)
  const [purposeId, setPurposeId] = useState<number | null>(null)
  const [amount, setAmount] = useState(0)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  const [note, setNote] = useState('')
  const [receiptFile, setReceiptFile] = useState<File | null>(null)

  async function loadFormData(): Promise<FormData> {
    // selectable=true (ADR-031): a closed envelope's purpose is excluded,
    // since PostTransaction's own guard would now refuse a posting to it.
    // A late entry against one goes through Incidentals.tsx's reopen
    // affordance first, not this everyday picker.
    const [accounts, purposes, balances] = await Promise.all([listAccounts(), listPurposes(true), getBalances()])
    return { accounts, purposes, balances }
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
      // Only honour a seed the fund actually has: a stale link to a purpose
      // that no longer exists would otherwise leave the picker on an id
      // nothing matches, and the form unsubmittable for no visible reason.
      const seeded = initialPurposeId == null ? undefined : loadState.data.purposes.find((p) => p.id === initialPurposeId)
      if (seeded) {
        setPurposeId(seeded.id)
      } else {
        const main = loadState.data.purposes.find((p) => p.kind === 'main')
        if (main) setPurposeId(main.id)
      }
    }
    // Only re-run when the load itself changes - accountId/purposeId are
    // this effect's own output, including them would fight its one-time
    // default assignment on every keystroke that changes them.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadState.status, loadState.data])

  const submitting = submitState.status === 'loading'
  const isTransfer = direction === 'transfer'

  // The same location on both sides moves nothing, and the ledger refuses it
  // anyway (ErrInvalidArgument). Caught here so she reads why in her own
  // language instead of a rejected submit, and the button stays disabled
  // rather than the form failing after the fact.
  const sameLocation = isTransfer && accountId !== null && accountId === toAccountId

  const canSubmit =
    amount > 0 &&
    accountId !== null &&
    occurredOn !== '' &&
    !submitting &&
    (isTransfer ? toAccountId !== null && !sameLocation : purposeId !== null)

  // Paying the parent body is two economically different things wearing one
  // shape here (#266, PRD section 7.6): money the fund COLLECTED for the
  // parent and now forwards is Titipan, and a levy the unit pays out of its
  // own routine money is an ordinary expense on Kas Utama. The picker lists
  // both tags flat, and picking Titipan for the second drives its balance
  // negative - which reads as forwarding money nobody ever gave her.
  //
  // The warning fires on exactly that, and on nothing else: a genuine
  // custodial forward can never take Titipan below zero, because the fund
  // collected the money before it forwarded it. So this is the definition of
  // the mistake rather than a heuristic about it, which is why the form can
  // stay silent through every correct recording.
  //
  // It warns and never blocks. A Titipan may legitimately sit negative -
  // ADR-031 blessed the same shape for an incidental's shortfall - so a
  // treasurer who means it goes ahead, and #276 makes it correctable
  // afterwards either way.
  // The purpose a transfer carries. Never chosen by her: both legs carry it,
  // so it nets to zero on every purpose balance, and a field asking which
  // one would be a question with no consequence (#235, ADR-024).
  // What each side of a transfer holds right now, and what the source is
  // left with (#235 revision). Shown under both pickers - the glimpse is
  // also what makes the swap button's effect legible - but only the source
  // can be driven negative: receiving money never pushes a balance down, so
  // a destination already below zero only moves closer to it.
  const accountBalance = (id: number | null) =>
    id === null ? null : (loadState.data?.balances.accounts.find((a) => a.id === id)?.balance ?? null)
  const fromBalance = accountBalance(accountId)
  const toBalance = accountBalance(toAccountId)

  // Warns, never blocks - the same call #266 made, and for the same reason:
  // every other posting path in this app already permits an out larger than
  // any balance, and a treasurer recording a real deposit out of a wallet
  // the app believes is empty must be able to say so. The refusal would be
  // a new overdraft rule applied in exactly one place.
  const transferGoesNegative = isTransfer && amount > 0 && fromBalance !== null && fromBalance - amount < 0

  const mainPurposeId = loadState.data?.purposes.find((p) => p.kind === 'main')?.id ?? null
  const chosenPurpose = loadState.data?.purposes.find((p) => p.id === purposeId) ?? null
  const chosenPurposeBalance = loadState.data?.balances.purposes.find((p) => p.id === purposeId)?.balance ?? 0
  const warnsPassThroughNegative =
    direction === 'out' && chosenPurpose?.kind === 'pass_through' && amount > 0 && chosenPurposeBalance - amount < 0

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return

    void submitRun(async () => {
      const trimmedNote = note.trim()
      const noteOrNull = trimmedNote === '' ? null : trimmedNote

      // A transfer is a different route, not a third kind of transaction
      // (ADR-024, ADR-027): POST /api/transfers posts the pair and the fund
      // total cannot move, because one amount goes out and the same amount
      // comes in. purpose_id is required by that route but immaterial to
      // every balance - both legs carry it, so it nets to zero - which is
      // why the form sends Kas Utama rather than asking (#235).
      // `direction === 'transfer'` rather than the isTransfer boolean: this
      // is the branch that narrows the type for createTransaction below,
      // which takes 'in' | 'out' and nothing else.
      if (direction === 'transfer') {
        if (toAccountId === null) return null
        const transfer = await postTransfer({
          purposeId: mainPurposeId ?? 0,
          fromAccountId: accountId,
          toAccountId,
          amount,
          occurredOn,
          note: noteOrNull,
        })
        rememberAccountId(accountId)
        onRecorded(direction)
        return transfer
      }

      if (purposeId === null) return null
      const result = await createTransaction({
        accountId,
        purposeId,
        direction,
        amount,
        occurredOn,
        note: noteOrNull,
      })
      rememberAccountId(accountId)

      // A second request, only once the row above exists (see this
      // component's own doc comment). A failure here never rolls the
      // transaction back and never surfaces through submitState's own
      // error path - it is caught here, not rethrown, so the form still
      // reports success and only says photoFailed.
      let photoFailed = false
      if (receiptFile) {
        try {
          await uploadReceipt('transactions', result.id, receiptFile)
        } catch {
          photoFailed = true
        }
      }

      onRecorded(direction, photoFailed)
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
        {/* sr-only: the three captions below say what this is, so a visible
            "Jenis" above them labels a control that already labelled itself.
            It stays in the DOM because the group still needs a name for a
            screen reader, which reads the caption of one option, not the set. */}
        <Label htmlFor="record-direction" className="sr-only">
          {text.directionLabel}
        </Label>
        <div id="record-direction" role="group" aria-label={text.directionLabel} className={segmentedTrackClass(3)}>
          <Button
            type="button"
            variant={direction === 'out' ? 'default' : 'ghost'}
            aria-pressed={direction === 'out'}
            className={segmentedStackedItemClass(direction === 'out')}
            onClick={() => setDirection('out')}
          >
            <ArrowUpRight aria-hidden="true" />
            {text.directionOut}
          </Button>
          <Button
            type="button"
            variant={direction === 'in' ? 'default' : 'ghost'}
            aria-pressed={direction === 'in'}
            className={segmentedStackedItemClass(direction === 'in')}
            onClick={() => setDirection('in')}
          >
            <ArrowDownLeft aria-hidden="true" />
            {text.directionIn}
          </Button>
          {/* Money that is neither entering nor leaving, only changing place
              (#235). Its own direction rather than an out with a special
              purpose: the fund total does not move, and PRD section 6 tracks
              balances per location precisely so this movement is recordable. */}
          <Button
            type="button"
            variant={direction === 'transfer' ? 'default' : 'ghost'}
            aria-pressed={direction === 'transfer'}
            className={segmentedStackedItemClass(direction === 'transfer')}
            onClick={() => setDirection('transfer')}
          >
            <ArrowLeftRight aria-hidden="true" />
            {text.directionTransfer}
          </Button>
        </div>
      </div>

      <AmountInput id="record-amount" label={text.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

      {/* One location for an ordinary entry, two for a transfer - and the
          field she already knows keeps its meaning either way: accountId is
          where the money is, or where it leaves from. A transfer hides the
          peruntukan entirely, because it does not have one to choose: both
          legs carry the same tag and it nets to zero (ADR-024). */}
      {/* The picker and its balance preview share one gap-1.5 column, exactly
          as the destination pair below does - otherwise the source's preview
          inherits the form's own gap-4 and the two read as differently
          spaced (they were). */}
      <div className="flex flex-col gap-1.5">
        <AccountPicker
          id="record-account"
          label={isTransfer ? text.fromLocationLabel : text.locationLabel}
          accounts={loadState.data.accounts}
          value={accountId}
          onChange={setAccountId}
          disabled={submitting}
        />
        {isTransfer && fromBalance !== null && (
          <p className="text-sm text-muted-foreground">{text.locationBalance(formatIDR(fromBalance))}</p>
        )}
      </div>

      {isTransfer && (
        <div className="flex flex-col gap-1.5">
          {/* Depositing cash and drawing it back out are the same two
              locations in opposite order, so the pair is worth one tap
              rather than four. Self-start so the control is only as wide as
              it needs to be, and size-11 so it clears 44px on its own. */}
          <Button
            type="button"
            variant="outline"
            className="h-11 self-center px-4"
            disabled={submitting}
            onClick={() => {
              setAccountId(toAccountId)
              setToAccountId(accountId)
            }}
          >
            <ArrowUpDown aria-hidden="true" />
            {text.swapLocations}
          </Button>

          <AccountPicker
            id="record-to-account"
            label={text.toLocationLabel}
            accounts={loadState.data.accounts}
            value={toAccountId}
            onChange={setToAccountId}
            disabled={submitting}
          />
          {toBalance !== null && <p className="text-sm text-muted-foreground">{text.locationBalance(formatIDR(toBalance))}</p>}

          {sameLocation && (
            <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention">
              {text.sameLocationHint}
            </p>
          )}
          {!sameLocation && transferGoesNegative && fromBalance !== null && (
            <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention">
              {text.locationGoesNegative(formatIDR(Math.abs(fromBalance - amount)))}
            </p>
          )}
        </div>
      )}

      {!isTransfer && (
      <div className="flex flex-col gap-1.5">
        <PurposePicker
          id="record-purpose"
          label={text.purposeLabel}
          purposes={loadState.data.purposes}
          value={purposeId}
          onChange={setPurposeId}
          disabled={submitting}
        />
        {/* Terracotta, never alarm-red (Design-System): nothing is broken
            and she may well mean it - this names the likelier reading and
            the tag that fits it, then gets out of the way. role="status"
            rather than "alert" for the same reason. */}
        {warnsPassThroughNegative && (
          <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention">
            {text.passThroughNegativeHint(chosenPurpose?.name ?? '')}
          </p>
        )}
      </div>
      )}

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

      {/* No photo field for a transfer (see this component's own doc
          comment) - money moving between the fund's own locations has no
          nota to document. */}
      {!isTransfer && <ReceiptPicker id="record-receipt" value={receiptFile} onChange={setReceiptFile} disabled={submitting} />}

      {submitState.status === 'error' && submitState.error && <ErrorState error={submitState.error} />}

      {/* One row for both, primary on the right (Design-System, "Action
          rows"): two full-width stacked buttons made a two-choice decision
          look like a list of things to do. */}
      <div className="grid grid-cols-2 gap-2">
        <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
          {text.cancel}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.submitting : text.submit}
        </Button>
      </div>
    </form>
  )
}
