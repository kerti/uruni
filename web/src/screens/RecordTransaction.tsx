import { useEffect, useState, type FormEvent } from 'react'
import { ArrowDownLeft, ArrowLeftRight, ArrowUpDown, ArrowUpRight, CalendarCheck, Shuffle } from 'lucide-react'

import DateField from '@/components/DateField'
import OpeningDateHint from '@/components/OpeningDateHint'
import { dateBounds, todayISODate } from '@/lib/dates'
import AmountInput from '@/components/money/AmountInput'
import AccountPicker from '@/components/pickers/AccountPicker'
import MemberPicker, { OptionalMemberPicker } from '@/components/pickers/MemberPicker'
import PurposePicker from '@/components/pickers/PurposePicker'
import ReceiptPicker from '@/components/ReceiptPicker'
import { segmentedStackedItemClass, segmentedTrackClass } from '@/components/segmented'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import RecordDuesPayment from '@/screens/Dues/RecordPayment'
import { listAccounts } from '@/lib/accounts'
import { getBalances } from '@/lib/balances'
import { formatIDR } from '@/lib/money'
import { uploadReceipt } from '@/lib/receipts'
import { postPurposeMove } from '@/lib/purposeMoves'
import { postTransfer } from '@/lib/transfers'
import { defaultSourceAccountId, rememberAccountId, soleCandidateId } from '@/lib/lastAccount'
import { listPurposes } from '@/lib/purposes'
import { listAllMembers } from '@/lib/setup'
import { createReimbursement } from '@/lib/reimbursements'
import { createTransaction } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Balances } from '@/lib/balances'
import type { Purpose } from '@/lib/purposes'
import type { Member } from '@/lib/setup'

const text = copy.record

/**
 * What the form is recording. 'in' and 'out' post a transaction; 'transfer'
 * posts a pair through POST /api/transfers - money that only changes place,
 * so it gets its own direction rather than being an out with a special
 * purpose. 'purpose' is its counterpart (ADR-036): money that stays where it
 * is and changes what it is for, posted through POST /api/purpose-moves.
 */
export type Direction = 'in' | 'out' | 'transfer' | 'purpose'

/** What onRecorded names: a Direction, or 'reimbursement' for a Keluar ticked
 * "Pengeluaran ini ditalangi" - a claim, which posts nothing to the ledger,
 * so it has no Direction of its own. */
export type Recorded = Direction | 'reimbursement'

interface FormData {
  accounts: Account[]
  purposes: Purpose[]
  /** Every purpose's current balance. Fetched with the form so the Titipan
   * warning below can appear as she types, without a request per keystroke. */
  balances: Balances
  /** The "Dari siapa? (opsional)" picker's roster (ADR-034) - fetched with the
   * rest of the form's data so choosing "Uang masuk" on an envelope shows a
   * filled picker instead of a spinner. */
  members: Member[]
}

/**
 * The record-transaction screen (PRD section 7.2): amount, direction,
 * location, purpose, date, optional note, and an optional photo of the nota,
 * posted through POST /api/transactions. is_adjustment always stays false on
 * the wire - only the reconcile flow sets it.
 *
 * The photo travels a second request behind the transaction: the upload route
 * needs the posted row's id (POST /api/transactions/{id}/receipts), so it
 * goes out only once createTransaction's response comes back. A transfer has
 * no photo field - no nota to document. If the second request fails, the
 * transaction is NOT rolled back (ADR-011: a receipt lives in its own table
 * precisely so it can be attached, or fail to attach, without touching the
 * immutable row it names) - onRecorded's second argument says so, and App.tsx
 * shows the "tersimpan, tapi belum terunggah" message instead of the ordinary
 * success line.
 *
 * Smart defaults per PRD section 7.2: location remembers the last choice
 * (localStorage, guarded - see lib/lastAccount.ts), purpose defaults to the
 * `kind: "main"` row, date defaults to today. Direction has no PRD-specified
 * default; this screen defaults to "out" (an expense) as the more frequent
 * everyday entry - a call made here, not settled elsewhere.
 *
 * onRecorded is called once, after a successful post - the caller (App.tsx)
 * owns navigating home and showing the success message; this screen has no
 * route knowledge. onCancel is the same contract for leaving without
 * recording: the installed app runs in `display: standalone` (ADR-008), where
 * there is no browser back button, so a form with only a submit is a room
 * with no door.
 *
 * `initialPurposeId`, when given, seeds the purpose default with that id
 * instead of the fund's `kind: "main"` row - the incidentals screen reuses
 * this form for contributions/disbursements by navigating here with the
 * envelope's purpose already chosen (App.tsx's `/record` route reads it from
 * a `purpose` search param). Absent, behaviour is unchanged.
 */
export default function RecordTransaction({
  onRecorded,
  onCancel,
  initialPurposeId,
  initialMemberId,
  initialDues = false,
  fronted = false,
  onFrontedChange,
  onDuesRecorded,
  onDuesCancel,
}: {
  /** photoFailed is true only when a photo was picked and the transaction
   * posted but the receipt upload failed - never set for a transfer, which
   * offers no photo field. */
  onRecorded: (recorded: Recorded, photoFailed?: boolean) => void
  onCancel: () => void
  initialPurposeId?: number | null
  /** Seeds the "Dari siapa?" field (ADR-034): Incidentals.tsx's participation
   * row navigates here with both the envelope's purpose and this member
   * chosen, the `/record` route reading a second search param (App.tsx's
   * `member`) as it reads `purpose`. Absent, the field starts unset - a guest,
   * an anonymous giver, or a form that isn't a contribution at all. */
  initialMemberId?: number | null
  /** Iuran, the toggle's fourth item: a dues payment moves money, so it is
   * recorded here like every other kind (ADR-032). Choosing it swaps the form
   * below for RecordDuesPayment's, with its own callbacks, since where she
   * lands after a dues payment depends on the door she came in by.
   * initialDues opens with it chosen. */
  initialDues?: boolean
  /** "Pengeluaran ini ditalangi" is ticked: a member paid out of pocket, so
   * this Keluar is a Talangan claim. Form mode is a route (ADR-032), so it
   * arrives from `/record?fronted=1` and every change goes back out through
   * onFrontedChange for the caller to write to the URL. */
  fronted?: boolean
  onFrontedChange?: (fronted: boolean) => void
  onDuesRecorded: () => void
  onDuesCancel?: () => void
}) {
  const [loadState, loadRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()

  // A link naming a contributor (/record?member=) is money coming in by
  // definition - opening on 'out' would hide "Dari siapa?" and the member it
  // carries (ADR-034).
  const [direction, setDirection] = useState<Direction>(initialMemberId != null ? 'in' : 'out')
  // Kept apart from direction rather than widening Direction: every other line
  // of this form means a transaction's direction by it, and switching to Iuran
  // and back leaves the transaction form as she left it.
  const [duesChosen, setDuesChosen] = useState(initialDues)
  const [accountId, setAccountId] = useState<number | null>(null)
  // Only used by 'transfer': where the money lands. The single accountId above
  // is where it leaves from, as for an ordinary out - so the field she has
  // been using keeps its meaning and only the second is new.
  const [pickedToAccountId, setToAccountId] = useState<number | null>(null)
  const [purposeId, setPurposeId] = useState<number | null>(null)
  // Only used by 'purpose' (ADR-036): where the money is for now, and where it
  // should be for. purposeId above is the ordinary entry's one tag.
  const [fromPurposeId, setFromPurposeId] = useState<number | null>(null)
  const [toPurposeId, setToPurposeId] = useState<number | null>(null)
  const [amount, setAmount] = useState(0)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  const [note, setNote] = useState('')
  const [receiptFile, setReceiptFile] = useState<File | null>(null)
  // "Dari siapa? (opsional)" (ADR-034) - seeded once below, alongside the
  // account/purpose defaults.
  const [memberId, setMemberId] = useState<number | null>(null)
  // Who paid, for a claim. Apart from memberId: that is the optional
  // contributor of an incoming envelope entry and must never leak across.
  const [claimMemberId, setClaimMemberId] = useState<number | null>(null)

  async function loadFormData(): Promise<FormData> {
    // selectable=true (ADR-031): a closed envelope's purpose is excluded,
    // since PostTransaction would refuse a posting to it. A late entry against
    // one goes through Incidentals.tsx's reopen affordance first. A claim uses
    // the same list: settling it posts to its pos, which the same guard would
    // refuse on a closed envelope.
    const [accounts, purposes, balances, members] = await Promise.all([listAccounts(), listPurposes(true), getBalances(), listAllMembers()])
    return { accounts, purposes, balances, members }
  }

  // A write whose response was lost re-reads the balances before she can retry.
  const settleOnUnknown = { settle: () => loadRun(loadFormData, { silent: true }) }

  useEffect(() => {
    void loadRun(loadFormData)
    // loadRun is a stable useCallback (useApi.ts); this fires once on mount,
    // like App.tsx's session-probe effect.
  }, [loadRun])

  // Applies the smart defaults once the form data has loaded: location
  // remembers the last active choice (falling back to the first active account
  // when there is none, or the remembered one was since retired), purpose
  // defaults to the fund's one `kind: "main"` row.
  useEffect(() => {
    if (loadState.status !== 'success' || !loadState.data) return

    if (accountId === null) {
      const defaultId = defaultSourceAccountId(loadState.data.accounts)
      if (defaultId !== null) setAccountId(defaultId)
    }

    if (purposeId === null) {
      // Only honour a seed the fund has: a stale link to a purpose that no
      // longer exists would leave the picker on an id nothing matches, and the
      // form unsubmittable for no visible reason.
      const seeded = initialPurposeId == null ? undefined : loadState.data.purposes.find((p) => p.id === initialPurposeId)
      if (seeded) {
        setPurposeId(seeded.id)
      } else {
        const main = loadState.data.purposes.find((p) => p.kind === 'main')
        if (main) setPurposeId(main.id)
      }
    }

    // Pindah pos opens with the source on Kas Utama (ADR-036), the usual
    // giver. The destination is never preselected (#737): it starts blank
    // and is filled only when there is exactly one pos it could be. Never a
    // Titipan: it is not offered at all (movablePurposes below).
    if (fromPurposeId === null && toPurposeId === null) {
      const movable = loadState.data.purposes.filter((p) => p.kind !== 'pass_through')
      const main = movable.find((p) => p.kind === 'main')
      if (main) {
        setFromPurposeId(main.id)
        const onlyOther = soleCandidateId(movable.filter((p) => p.id !== main.id))
        if (onlyOther !== null) setToPurposeId(onlyOther)
      }
    }

    // "Dari siapa?" (ADR-034) - the same "only honour a real one" guard as
    // purposeId's seed: a stale link naming a member the fund no longer has (or
    // has deactivated) leaves the field at its default rather than a picker
    // showing nobody.
    if (memberId === null && initialMemberId != null) {
      const seeded = loadState.data.members.find((m) => m.id === initialMemberId && m.inactive_on === null)
      if (seeded) setMemberId(seeded.id)
    }
    // Re-run only when the load itself changes - accountId/purposeId/memberId
    // are this effect's output, and including them would fight its one-time
    // default assignment on every keystroke that changes them.
    // oxlint-disable-next-line react/exhaustive-deps -- seed the defaults once per load; clearing "Dari siapa?" back to null would otherwise re-seed it from initialMemberId
  }, [loadState.status, loadState.data])

  const submitting = submitState.status === 'loading'
  const isTransfer = direction === 'transfer'
  const isPurposeMove = direction === 'purpose'
  // A claim moves no fund money: no location, no balance to watch.
  const isClaim = fronted && direction === 'out' && !duesChosen

  // The destination starts blank (#737) and fills itself only when exactly one
  // active location other than the source could be it; once she picks, her
  // choice stands.
  const toAccountId =
    pickedToAccountId ?? soleCandidateId((loadState.data?.accounts ?? []).filter((a) => a.inactive_on === null && a.id !== accountId))

  // The same location on both sides moves nothing, and the ledger refuses it
  // anyway (ErrInvalidArgument). Caught here so she reads why in her language,
  // and the button stays disabled rather than the form failing after the fact.
  const sameLocation = isTransfer && accountId !== null && accountId === toAccountId

  // A purpose move (ADR-036) never offers Titipan: that money belongs to the
  // parent body and leaves only by being forwarded. selectable=true already
  // withheld the closed envelopes, which the ledger refuses on either side.
  const movablePurposes = (loadState.data?.purposes ?? []).filter((p) => p.kind !== 'pass_through')
  // Kas Utama is the only pos that always exists; without an open envelope
  // there is nowhere for it to move to, so say so instead of leaving "Ke"
  // empty.
  const hasOpenEnvelope = movablePurposes.some((p) => p.kind === 'incidental')
  const samePurpose = isPurposeMove && fromPurposeId !== null && fromPurposeId === toPurposeId
  // The source cannot give more than it holds, read from the balance fetched
  // with the form (the ledger re-checks at posting time, so a stale figure
  // costs one refused submit, never a wrong post). Unlike a location transfer
  // this blocks: the ledger refuses it, so letting her try would only move the
  // message.
  const purposeBalance = (id: number | null) =>
    id === null ? null : (loadState.data?.balances.purposes.find((p) => p.id === id)?.balance ?? null)
  const fromPurposeBalance = purposeBalance(fromPurposeId)
  const toPurposeBalance = purposeBalance(toPurposeId)
  const purposeMoveTooLarge = isPurposeMove && amount > 0 && fromPurposeBalance !== null && amount > fromPurposeBalance

  const canSubmit =
    amount > 0 &&
    (isClaim || accountId !== null) &&
    occurredOn !== '' &&
    !submitting &&
    (isClaim
      ? claimMemberId !== null && purposeId !== null
      : isTransfer
        ? toAccountId !== null && !sameLocation
        : isPurposeMove
          ? fromPurposeId !== null && toPurposeId !== null && !samePurpose && !purposeMoveTooLarge
          : purposeId !== null)

  // Paying the parent body is two economically different things in one shape
  // (PRD section 7.6): money the fund COLLECTED for the parent and now
  // forwards is Titipan, and a levy the unit pays out of its own routine money
  // is an ordinary expense on Kas Utama. The picker lists both tags flat, and
  // picking Titipan for the second drives its balance negative - which reads
  // as forwarding money nobody gave her.
  //
  // The warning fires on exactly that and nothing else: a genuine custodial
  // forward can never take Titipan below zero, because the fund collected the
  // money before forwarding it. So this is the definition of the mistake, not
  // a heuristic, which is why the form stays silent through every correct
  // recording.
  //
  // It warns and never blocks. A Titipan may legitimately sit negative -
  // ADR-031 blessed the same shape for an incidental's shortfall - so a
  // treasurer who means it goes ahead, and the pos correction (ADR-033) fixes
  // it afterwards either way.
  //
  // The purpose a transfer carries is never chosen by her: both legs carry it,
  // so it nets to zero on every purpose balance, and a field asking which one
  // would be a question with no consequence (ADR-024).
  //
  // Below: what each side of a transfer holds now, and what the source is
  // left with. Shown under both pickers - the glimpse also makes the swap
  // button's effect legible - but only the source can be driven negative:
  // receiving money never pushes a balance down.
  const accountBalance = (id: number | null) =>
    id === null ? null : (loadState.data?.balances.accounts.find((a) => a.id === id)?.balance ?? null)
  const fromBalance = accountBalance(accountId)
  const toBalance = accountBalance(toAccountId)

  // Warns, never blocks, for the same reason as the Titipan warning: every
  // other posting path already permits an out larger than any balance, and a
  // treasurer recording a real deposit out of a wallet the app believes is
  // empty must be able to say so. A refusal would be a new overdraft rule
  // applied in exactly one place.
  const transferGoesNegative = isTransfer && amount > 0 && fromBalance !== null && fromBalance - amount < 0

  const mainPurposeId = loadState.data?.purposes.find((p) => p.kind === 'main')?.id ?? null
  const chosenPurpose = loadState.data?.purposes.find((p) => p.id === purposeId) ?? null
  const chosenPurposeBalance = loadState.data?.balances.purposes.find((p) => p.id === purposeId)?.balance ?? 0
  const warnsPassThroughNegative =
    direction === 'out' && !isClaim && chosenPurpose?.kind === 'pass_through' && amount > 0 && chosenPurposeBalance - amount < 0

  // "Dari siapa? (opsional)" (ADR-034): only for money coming into an open
  // envelope - a contribution is the one shape the schema lets a member_id
  // ride on. Not gated on the envelope being open: PostTransaction already
  // refuses a posting to a closed one (ADR-031), and the picker excludes it
  // (selectable=true) before this field could show.
  const showContributorField = direction === 'in' && chosenPurpose?.kind === 'incidental'
  const heading = duesChosen
    ? copy.dues.payment.heading
    : isTransfer
      ? text.directionTransfer
      : isPurposeMove
        ? text.directionPurpose
        : direction === 'in'
          ? showContributorField
            ? text.headingDonation
            : text.headingIn
          : isClaim
            ? copy.reimbursements.record.heading
            : text.headingOut

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return
    if (!isClaim && accountId === null) return

    void submitRun(async () => {
      const trimmedNote = note.trim()
      const noteOrNull = trimmedNote === '' ? null : trimmedNote

      // A Talangan claim is off-ledger until settled: its own route, no
      // transaction posted. A photo that fails to upload never rolls the claim
      // back, as for a transaction.
      if (isClaim) {
        if (claimMemberId === null || purposeId === null) return null
        const claim = await createReimbursement({
          member_id: claimMemberId,
          purpose_id: purposeId,
          amount,
          incurred_on: occurredOn,
          note: noteOrNull,
        })
        let claimPhotoFailed = false
        if (receiptFile) {
          try {
            await uploadReceipt('reimbursements', claim.id, receiptFile)
          } catch {
            claimPhotoFailed = true
          }
        }
        onRecorded('reimbursement', claimPhotoFailed)
        return claim
      }
      if (accountId === null) return null

      // A transfer is a different route, not a third kind of transaction
      // (ADR-024, ADR-027): POST /api/transfers posts the pair and the fund
      // total cannot move, because one amount goes out and the same comes in.
      // purpose_id is required by that route but immaterial to every balance -
      // both legs carry it, so it nets to zero - which is why the form sends
      // Kas Utama rather than asking. `direction === 'transfer'` rather than
      // the isTransfer boolean: this branch narrows the type for
      // createTransaction below, which takes 'in' | 'out' only.
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

      // The same shape one level over (ADR-036): POST /api/purpose-moves posts
      // the pair on the one chosen location, so the fund total and that
      // location's balance cannot move - only the two purposes' balances do.
      if (direction === 'purpose') {
        if (fromPurposeId === null || toPurposeId === null) return null
        const move = await postPurposeMove({
          fromPurposeId,
          toPurposeId,
          accountId,
          amount,
          occurredOn,
          note: noteOrNull,
        })
        rememberAccountId(accountId)
        onRecorded(direction)
        return move
      }

      if (purposeId === null) return null
      const result = await createTransaction({
        accountId,
        purposeId,
        direction,
        amount,
        occurredOn,
        note: noteOrNull,
        // Only sent when the field is showing (ADR-034) - a stale memberId from
        // a purpose or direction she has changed away from never reaches the
        // wire.
        memberId: showContributorField ? memberId : null,
      })
      rememberAccountId(accountId)

      // A second request, only once the row above exists (see this component's
      // doc comment). A failure here never rolls the transaction back and never
      // surfaces through submitState's error path - it is caught, not
      // rethrown, so the form reports success and only says photoFailed.
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
    }, settleOnUnknown)
  }

  if (loadState.status === 'idle' || loadState.status === 'loading') {
    return <Loading />
  }

  function chooseDirection(next: Direction) {
    setDirection(next)
    setDuesChosen(false)
    if (next !== 'out' && fronted) onFrontedChange?.(false)
  }

  if (loadState.status === 'error' || !loadState.data) {
    return loadState.error ? <ErrorState error={loadState.error} onRetry={() => void loadRun(loadFormData)} /> : null
  }

  return (
    <div className="flex w-full flex-col gap-4">
      {/* The heading names what is being recorded, and follows the kind chosen
          below: a dues payment keeps the title its own screen always had. */}
      <h1 className="text-xl font-semibold">{heading}</h1>

      <div className="flex flex-col gap-1.5">
        {/* sr-only: the five captions below say what this is, so a visible
            "Jenis" above them labels a control that already labelled itself.
            It stays in the DOM because the group still needs a name for a
            screen reader, which reads the caption of one option, not the set. */}
        <Label htmlFor="record-direction" className="sr-only">
          {text.directionLabel}
        </Label>
        <div id="record-direction" role="group" aria-label={text.directionLabel} className={segmentedTrackClass(5)}>
          {/* Five options across a phone: one-word captions, with the full name
              in the aria-label (segmented.ts). The icon carries the verb. */}
          <Button
            type="button"
            variant={!duesChosen && direction === 'out' ? 'default' : 'ghost'}
            aria-pressed={!duesChosen && direction === 'out'}
            aria-label={text.directionOut}
            className={segmentedStackedItemClass(!duesChosen && direction === 'out')}
            onClick={() => chooseDirection('out')}
          >
            <ArrowUpRight aria-hidden="true" />
            {text.captionOut}
          </Button>
          <Button
            type="button"
            variant={!duesChosen && direction === 'in' ? 'default' : 'ghost'}
            aria-pressed={!duesChosen && direction === 'in'}
            aria-label={text.directionIn}
            className={segmentedStackedItemClass(!duesChosen && direction === 'in')}
            onClick={() => chooseDirection('in')}
          >
            <ArrowDownLeft aria-hidden="true" />
            {text.captionIn}
          </Button>
          {/* Money that is neither entering nor leaving, only changing place
              (#235). Its own direction rather than an out with a special
              purpose: the fund total does not move, and PRD section 6 tracks
              balances per location precisely so this movement is recordable. */}
          <Button
            type="button"
            variant={!duesChosen && direction === 'transfer' ? 'default' : 'ghost'}
            aria-pressed={!duesChosen && direction === 'transfer'}
            aria-label={text.directionTransfer}
            className={segmentedStackedItemClass(!duesChosen && direction === 'transfer')}
            onClick={() => chooseDirection('transfer')}
          >
            <ArrowLeftRight aria-hidden="true" />
            {text.captionLocation}
          </Button>
          {/* Its counterpart (ADR-036, #383): money that stays where it is
              and changes what it is for - Kas Utama to an envelope, back, or
              between two envelopes. */}
          <Button
            type="button"
            variant={!duesChosen && direction === 'purpose' ? 'default' : 'ghost'}
            aria-pressed={!duesChosen && direction === 'purpose'}
            aria-label={text.directionPurpose}
            className={segmentedStackedItemClass(!duesChosen && direction === 'purpose')}
            onClick={() => chooseDirection('purpose')}
          >
            <Shuffle aria-hidden="true" />
            {text.captionPurpose}
          </Button>
          {/* A dues payment (#315): the fifth kind of money moving, so the
              fifth item here rather than a link or a second screen - Catat
              stays one screen, one tap from the footer (PRD section 7.2). */}
          <Button
            type="button"
            variant={duesChosen ? 'default' : 'ghost'}
            aria-pressed={duesChosen}
            className={segmentedStackedItemClass(duesChosen)}
            onClick={() => {
              setDuesChosen(true)
              if (fronted) onFrontedChange?.(false)
            }}
          >
            <CalendarCheck aria-hidden="true" />
            {text.directionDues}
          </Button>
        </div>
      </div>

      {duesChosen ? (
        <RecordDuesPayment embedded onRecorded={onDuesRecorded} onCancel={onDuesCancel ?? onCancel} />
      ) : (
        <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
          {/* Keluar only (#368): somebody paid out of pocket, so this is a
              claim on the kas rather than money leaving it. Ticked, the form
              loses its location and gains the member who paid. */}
          {!duesChosen && direction === 'out' && (
            <label className="flex items-center gap-2 font-medium">
              <input
                type="checkbox"
                className="size-4 rounded border-input accent-primary"
                checked={isClaim}
                onChange={(event) => onFrontedChange?.(event.target.checked)}
                disabled={submitting}
              />
              {text.frontedLabel}
            </label>
          )}

          <AmountInput id="record-amount" label={text.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

          {/* Pindah pos (ADR-036): where the money is for now, and where
              it should be for - right after the amount, because they are the
              point of the move; the location is the least of it and follows.
              Two columns like the transfer's pair, so each picker keeps its
              own preview or hint beneath it. */}
          {isPurposeMove && (
            <>
              <div className="flex flex-col gap-1.5">
                <PurposePicker
                  id="record-from-purpose"
                  label={text.fromPurposeLabel}
                  purposes={movablePurposes}
                  value={fromPurposeId}
                  onChange={setFromPurposeId}
                  disabled={submitting}
                />
                {fromPurposeBalance !== null && (
                  <p className="text-sm text-muted-foreground">{text.purposeBalance(formatIDR(fromPurposeBalance))}</p>
                )}
              </div>
              <div className="flex flex-col gap-1.5">
                {/* Kas Utama to an envelope and back again are the same pair
                in opposite order - the swap Pindah lokasi already offers. */}
                <Button
                  type="button"
                  variant="outline"
                  size="lg"
                  className="self-center px-4"
                  disabled={submitting || toPurposeId === null}
                  onClick={() => {
                    setFromPurposeId(toPurposeId)
                    setToPurposeId(fromPurposeId)
                  }}
                >
                  <ArrowUpDown aria-hidden="true" />
                  {text.swapPurposes}
                </Button>

                <PurposePicker
                  id="record-to-purpose"
                  label={text.toPurposeLabel}
                  purposes={movablePurposes}
                  value={toPurposeId}
                  onChange={setToPurposeId}
                  disabled={submitting}
                />
                {toPurposeBalance !== null && (
                  <p className="text-sm text-muted-foreground">{text.purposeBalance(formatIDR(toPurposeBalance))}</p>
                )}
                {!hasOpenEnvelope && <p className="text-sm text-muted-foreground">{text.noOpenEnvelopeHint}</p>}
                {samePurpose && (
                  <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
                    {text.samePurposeHint}
                  </p>
                )}
                {!samePurpose && purposeMoveTooLarge && fromPurposeBalance !== null && (
                  <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
                    {text.purposeInsufficientHint(formatIDR(fromPurposeBalance))}
                  </p>
                )}
              </div>
            </>
          )}

          {/* One location for an ordinary entry, two for a transfer - and the
          field she already knows keeps its meaning either way: accountId is
          where the money is, or where it leaves from. A transfer hides the
          pos entirely, because it does not have one to choose: both
          legs carry the same tag and it nets to zero (ADR-024). */}
          {/* The picker and its balance preview share one gap-1.5 column, exactly
          as the destination pair below does - otherwise the source's preview
          inherits the form's own gap-4 and the two read as differently
          spaced (they were). */}
          {isClaim ? (
            <MemberPicker
              id="record-claim-member"
              label={copy.reimbursements.record.memberLabel}
              placeholder={copy.reimbursements.record.memberPlaceholder}
              members={loadState.data.members}
              value={claimMemberId}
              onChange={setClaimMemberId}
              disabled={submitting}
            />
          ) : (
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
          )}

          {isTransfer && (
            <div className="flex flex-col gap-1.5">
              {/* Depositing cash and drawing it back out are the same two
              locations in opposite order, so the pair is worth one tap
              rather than four. Self-start so the control is only as wide as
              it needs to be, and size-11 so it clears 44px on its own. */}
              <Button
                type="button"
                variant="outline"
                size="lg"
                className="self-center px-4"
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
                <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
                  {text.sameLocationHint}
                </p>
              )}
              {!sameLocation && transferGoesNegative && fromBalance !== null && (
                <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
                  {text.locationGoesNegative(formatIDR(Math.abs(fromBalance - amount)))}
                </p>
              )}
            </div>
          )}

          {!isTransfer && !isPurposeMove && (
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
                <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
                  {text.passThroughNegativeHint(chosenPurpose?.name ?? '')}
                </p>
              )}
            </div>
          )}

          {/* "Dari siapa? (opsional)" (ADR-034, #211) - only for money coming
              into an open envelope, never a required field: a guest or an
              anonymous giver leaves it unset. */}
          {showContributorField && (
            <OptionalMemberPicker
              id="record-contributor"
              label={text.contributorLabel}
              members={loadState.data.members}
              value={memberId}
              onChange={setMemberId}
              disabled={submitting}
            />
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="record-date">{text.dateLabel}</Label>
            <DateField id="record-date" value={occurredOn} onChange={setOccurredOn} bounds={dateBounds.entry()} disabled={submitting} />
            {/* Which locations the entry touches decides the anchor: both for
                Pindah lokasi, none for Pindah pos and a talangan claim (no
                location - the fund's earliest opening applies). */}
            <OpeningDateHint
              date={occurredOn}
              accounts={loadState.data.accounts}
              accountIds={isTransfer ? [accountId, toAccountId] : isPurposeMove || isClaim ? [] : [accountId]}
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
          {!isTransfer && !isPurposeMove && (
            <ReceiptPicker id="record-receipt" value={receiptFile} onChange={setReceiptFile} disabled={submitting} />
          )}

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
      )}
    </div>
  )
}
