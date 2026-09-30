import { ArrowLeft } from 'lucide-react'
import { useEffect, useState, type FormEvent } from 'react'

import AccountPicker from '@/components/pickers/AccountPicker'
import { MemberMultiPicker } from '@/components/pickers/MemberPicker'
import AmountInput from '@/components/money/AmountInput'
import SectionDivider from '@/components/SectionDivider'
import TransactionList from '@/components/TransactionList'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { listAccounts } from '@/lib/accounts'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { formatIDR } from '@/lib/money'
import {
  closeIncidental,
  getIncidental,
  getIncidentalParticipation,
  reopenIncidental,
  updateIncidentalParticipation,
} from '@/lib/incidentals'
import { renamePurpose } from '@/lib/purposes'
import { listAllMembers } from '@/lib/setup'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Account } from '@/lib/accounts'
import type { Incidental, IncidentalDetail, IncidentalParticipation, ParticipationStateKind } from '@/lib/incidentals'
import type { Member } from '@/lib/setup'
import type { Transaction } from '@/lib/transactions'

const text = copy.incidentals

/**
 * One speaking-string feedback for a finished action - same shape as
 * Reimbursements.tsx's own Feedback type.
 */
type Feedback = { kind: 'success' | 'error'; text: string }

/** Wire error code -> Indonesian copy, scoped first to this screen's own
 * codes (incidental_already_closed) then to the shared map; never the
 * English wire message (ADR-014). Mirrors Reimbursements.tsx's errorText. */
function errorText(err: ApiError): string {
  const specific = text.errors[err.code as keyof typeof text.errors]
  if (specific) return specific
  const common = copy.common.errors[err.code as keyof typeof copy.common.errors]
  return common ?? copy.common.unknownError
}

/** Local YYYY-MM-DD - same helper as every other record form in this app. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

/**
 * The incidental-envelope detail screen (M6.19, PRD section 7.5; reduced to
 * detail-only by #263/ADR-032): a separate pot for a one-off occasion -
 * collect contributions and pay disbursements against it, then close it
 * once the occasion is over. Closing rolls any leftover into the fund's
 * main purpose and answers with `rolled_amount`, shown here honestly even
 * when it is zero - a zero rollover is not the same as no answer.
 *
 * Contributions and disbursements are not this screen's own form: they are
 * ordinary POST /api/transactions calls tagged to the envelope's purpose,
 * and M6.8's RecordTransaction.tsx already has every field that needs -
 * account, amount, direction, date, note. `onRecordFor` (App.tsx) navigates
 * there with the envelope's purpose pre-chosen (`/record?purpose=<id>`)
 * rather than this screen duplicating that form. Closing IS specific to an
 * envelope, so it keeps its own inline form here.
 *
 * Opening a new envelope, and the card list of every envelope the fund has
 * (open and closed), both moved to Pengaturan's own section
 * (screens/Settings/Incidentals.tsx, #263) - they are the same object as
 * Titipan there. What survives at this route is the detail view alone,
 * always reached with `?purpose=<id>` (App.tsx redirects `/incidentals`
 * without one, or with an unparseable one, to `/settings`): from a Beranda
 * purpose-breakdown row, or from a card in that section.
 *
 * `onViewTransactionsFor` (#262) leads the other way, into Riwayat ->
 * Transaksi filtered to this envelope's purpose. ADR-032 drops a closed
 * envelope off Beranda and names that filtered list as its record, so for a
 * closed envelope this screen is the only place that route begins.
 */
export default function Incidentals({
  onBack,
  onRecordFor,
  onViewTransactionsFor,
  purposeId,
}: {
  onBack: () => void
  /** Navigates to Catat, this envelope's purpose pre-chosen - and, when
   * given, a member pre-chosen too (ADR-034's "one action per row: record a
   * contribution with the member already filled in"). */
  onRecordFor: (purposeId: number, memberId?: number) => void
  onViewTransactionsFor: (purposeId: number) => void
  purposeId: number
}) {
  const [accountsState, accountsRun] = useApi<Account[]>()
  const [membersState, membersRun] = useApi<Member[]>()
  const [detailState, detailRun] = useApi<IncidentalDetail>()
  const [participationState, participationRun] = useApi<IncidentalParticipation>()
  const [submitState, submitRun] = useApi<unknown>()

  const [showCloseForm, setShowCloseForm] = useState(false)

  const [feedback, setFeedback] = useState<Feedback | null>(null)

  // The rename dialog (#264), addressed by the URL rather than component
  // state (ADR-032) - `?edit=incidental:<id>`, the same search-param pattern
  // PassThrough.tsx's EditPassThroughDialog uses, hosted here rather than in
  // Pengaturan's own card list because this detail screen is where the
  // occasion is actually read and where a typo is actually noticed.
  const { value: dialogValue, open: openDialog, close: closeDialog, clear: clearDialog } = useDialogParam()
  const dialogTarget = parseDialogTarget('incidental', dialogValue)
  const isRenaming = dialogTarget.kind === 'edit' && dialogTarget.id === purposeId

  useEffect(() => {
    void accountsRun(listAccounts)
    void membersRun(listAllMembers)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountsRun, membersRun])

  // Fetches once for the purpose this screen was navigated with - a fresh
  // navigation to a different envelope remounts this component with a fresh
  // purposeId, same as initialPurposeId used to work before the list view
  // that once let her switch envelopes in place.
  useEffect(() => {
    void detailRun(() => getIncidental(purposeId))
    void participationRun(() => getIncidentalParticipation(purposeId))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [purposeId])

  // An `incidental:` value naming a different or malformed id: strip it
  // once the detail has loaded, rather than flash an empty dialog - never
  // closeDialog(), for the same reason PassThrough.tsx's own effect gives
  // (a closeDialog() here could run after a closeDialog() already in
  // flight has gone back, taking her off the screen entirely). A `foreign`
  // value is left exactly where it is - it belongs to something this
  // screen does not own.
  useEffect(() => {
    if (dialogTarget.kind === 'foreign' || dialogTarget.kind === 'new') return
    if (detailState.status !== 'success') return
    if (isRenaming) return
    clearDialog()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dialogValue, dialogTarget.kind, detailState.status, isRenaming])

  const submitting = submitState.status === 'loading'

  /** Every write on this screen - close, reopen - clears stale feedback,
   * runs the call, then says what happened. A named 409
   * (incidental_already_closed / incidental_not_closed) reaches the
   * treasurer through copy.incidentals.errors; anything else falls back to
   * the shared map, never the English wire message (ADR-014). Mirrors
   * Reimbursements.tsx's runWrite. */
  function runWrite<T>(api: () => Promise<T>, successText: string, onSuccess?: (result: T) => void) {
    void submitRun(async () => {
      setFeedback(null)
      try {
        const result = await api()
        setFeedback({ kind: 'success', text: successText })
        onSuccess?.(result)
      } catch (err) {
        const apiErr = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
        setFeedback({ kind: 'error', text: errorText(apiErr) })
        // eslint-disable-next-line no-console
        console.error('API error', apiErr.code, apiErr.message)
      }
    })
  }

  // The close response's own rolled_amount is deliberately not kept here
  // (#270): the refetched detail already states the rollover, and a second
  // copy in component state is the bug this issue reported - it survived
  // exactly one screen-lifetime. See DetailView's rolledAmount for the
  // derivation.
  function handleClose(accountId: number, closedOn: string, note: string) {
    runWrite(
      () => closeIncidental(purposeId, { accountId, closedOn, note: note.trim() === '' ? null : note }),
      text.close.success,
      () => {
        setShowCloseForm(false)
        void detailRun(() => getIncidental(purposeId))
      },
    )
  }

  // The way back from a closed envelope (ADR-031): reopening just clears
  // closed_on and refetches the detail, which is enough on its own -
  // DetailView's isOpen block already renders "Catat transaksi" and "Tutup
  // amplop" for any open envelope, so a reopen leads straight into the same
  // close form a late entry is meant to end at, not a bare toggle with
  // nothing next.
  function handleReopen() {
    runWrite(
      () => reopenIncidental(purposeId),
      text.reopen.success,
      () => {
        void detailRun(() => getIncidental(purposeId))
      },
    )
  }

  // On success the dialog itself has already made the PATCH/PUT calls (see
  // RenameIncidentalDialog) - this just closes it, refetches the detail so
  // the <h1> and the recipients line show whatever changed, refetches the
  // participation table too (a new minimum or a changed recipient set moves
  // rows between "belum"/"kurang" and the recipients exclusion), and reuses
  // the screen's own Feedback banner rather than a second, parallel success
  // mechanism.
  function handleRenamed() {
    closeDialog()
    setFeedback({ kind: 'success', text: text.rename.success })
    void detailRun(() => getIncidental(purposeId))
    void participationRun(() => getIncidentalParticipation(purposeId))
  }

  return (
    <>
      <DetailView
        detailState={detailState}
        participationState={participationState}
        accounts={accountsState.data ?? []}
        feedback={feedback}
        submitting={submitting}
        showCloseForm={showCloseForm}
        onRecord={(memberId) => onRecordFor(purposeId, memberId)}
        onViewTransactions={() => onViewTransactionsFor(purposeId)}
        onShowClose={() => {
          setShowCloseForm(true)
          setFeedback(null)
        }}
        onCancelClose={() => setShowCloseForm(false)}
        onClose={handleClose}
        onReopen={handleReopen}
        onRename={() => openDialog(`incidental:${purposeId}`)}
        onRetry={() => void detailRun(() => getIncidental(purposeId))}
        onBack={onBack}
      />
      <RenameIncidentalDialog
        envelope={detailState.data ?? null}
        members={membersState.data ?? []}
        open={isRenaming}
        onClose={closeDialog}
        onRenamed={handleRenamed}
      />
    </>
  )
}

function StatusBadge({ envelope }: { envelope: Incidental }) {
  if (envelope.closed_on) {
    return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{text.status.closed}</span>
  }
  return <span className="rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">{text.status.open}</span>
}

/** The detail view: one envelope's totals, a link into the real record form
 * for contributions/disbursements, and close once it is still open. Kept as
 * its own component (rather than inlined into Incidentals) so the close
 * form doesn't crowd the rest of the JSX. */
function DetailView({
  detailState,
  participationState,
  accounts,
  feedback,
  submitting,
  showCloseForm,
  onRecord,
  onViewTransactions,
  onShowClose,
  onCancelClose,
  onClose,
  onReopen,
  onRename,
  onRetry,
  onBack,
}: {
  detailState: ReturnType<typeof useApi<IncidentalDetail>>[0]
  participationState: ReturnType<typeof useApi<IncidentalParticipation>>[0]
  accounts: Account[]
  feedback: Feedback | null
  submitting: boolean
  showCloseForm: boolean
  onRecord: (memberId?: number) => void
  onViewTransactions: () => void
  onShowClose: () => void
  onCancelClose: () => void
  onClose: (accountId: number, closedOn: string, note: string) => void
  onReopen: () => void
  onRename: () => void
  onRetry: () => void
  onBack: () => void
}) {
  if (detailState.status === 'idle' || detailState.status === 'loading') {
    return <Loading />
  }

  if (detailState.status === 'error' || !detailState.data) {
    return detailState.error ? <ErrorState error={detailState.error} onRetry={onRetry} /> : null
  }

  const envelope = detailState.data
  const isOpen = envelope.closed_on === null

  // What the close rolled, derived rather than remembered (#270).
  //
  // ADR-031's invariant is that closing leaves this purpose's balance at
  // exactly zero, in whichever direction that takes. The balance is the
  // unfiltered net of everything posted against the purpose; collected and
  // disbursed here are the same net with the roll's own leg excluded
  // (IncidentalActivityTotals, the seam #215 drew). Zero net including the
  // roll therefore means the roll is exactly the net excluding it - so the
  // rollover is the gap between the two figures already on screen, which is
  // the very gap that made this screen unreadable: Terkumpul 10.000 against
  // Terpakai 0 on an envelope holding nothing.
  //
  // It holds across a close, a reopen, a late entry and a second close: each
  // close re-establishes the same zero. It is signed like the close
  // response's rolled_amount - positive rolled out, negative covered from
  // Kas Utama - so close.rolledLabel says which way it went, unchanged.
  //
  // Only for a closed envelope. An open one has not rolled anything, and a
  // reopened one must not claim to have.
  const rolledAmount = isOpen ? null : envelope.collected_amount - envelope.disbursed_amount

  return (
    <div className="mx-auto flex w-full max-w-sm flex-col gap-4">
      {/* A way back, not an action (#319): a quiet link, like a browser's own
          back, rather than a full-width button competing with the real ones. */}
      <Button type="button" variant="link" className="h-auto min-h-11 self-start p-0 text-muted-foreground" onClick={onBack}>
        <ArrowLeft aria-hidden="true" />
        {text.detail.backToSettings}
      </Button>

      <div className="flex items-start justify-between gap-3">
        <h1 className="text-xl font-semibold">{envelope.occasion}</h1>
        <StatusBadge envelope={envelope} />
      </div>

      {feedback && (
        <p
          role={feedback.kind === 'error' ? 'alert' : 'status'}
          className={
            feedback.kind === 'error'
              ? 'rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention'
              : 'rounded-lg bg-success-soft px-3 py-2 text-sm text-success'
          }
        >
          {feedback.text}
        </p>
      )}

      <div className="flex flex-col gap-2 rounded-2xl bg-card p-4 ring-1 ring-foreground/10">
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">{text.detail.collectedLabel}</span>
          <span className="tabular font-medium">{formatIDR(envelope.collected_amount)}</span>
        </div>
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">{text.detail.disbursedLabel}</span>
          <span className="tabular font-medium">{formatIDR(envelope.disbursed_amount)}</span>
        </div>
        {envelope.target_amount !== null && (
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">{text.detail.targetLabel}</span>
            <span className="tabular font-medium">{formatIDR(envelope.target_amount)}</span>
          </div>
        )}
      </div>

      {/* The rollover, on every visit to a closed envelope rather than only
          in the moment it was closed (#270) - shown even when it is 0: a
          zero rollover is an honest answer, not a missing one. It is signed
          (ADR-031), so the sentence itself says which way it went; the
          amount stays one field, absolute either way. */}
      {rolledAmount !== null && (
        <div className="flex items-center justify-between rounded-lg bg-muted p-3 text-sm">
          <span className="text-muted-foreground">{text.close.rolledLabel(rolledAmount)}</span>
          <span className="tabular font-medium">{formatIDR(Math.abs(rolledAmount))}</span>
        </div>
      )}

      {/* One action row for both states, so the close form replaces the
          whole of it rather than half; primary on the right (Design-System,
          "Action rows"). Ubah nama corrects a mistyped occasion (#264), open
          or closed, since the typo is usually noticed only after the
          occasion is over. */}
      {!showCloseForm && (
        <div className={isOpen ? 'grid grid-cols-3 gap-2' : 'grid grid-cols-2 gap-2'}>
          <Button type="button" size="lg" variant="outline" onClick={onRename}>
            {text.actions.rename}
          </Button>
          {isOpen ? (
            <>
              <Button type="button" size="lg" variant="outline" onClick={onShowClose}>
                {text.actions.close}
              </Button>
              {/* Contributions and disbursements both go through the real
                  record form (M6.8), pre-chosen to this envelope's purpose -
                  direction is decided there, by its own toggle. */}
              <Button type="button" size="lg" onClick={() => onRecord()}>
                {text.actions.record}
              </Button>
            </>
          ) : (
            /* The way back from a closed envelope (ADR-031): reopening
               rejoins the open row - Catat for the late entry and Tutup to
               close again - rather than leaving a bare toggle with nothing
               next. */
            <Button type="button" size="lg" variant="outline" onClick={onReopen} disabled={submitting}>
              {text.actions.reopen}
            </Button>
          )}
        </div>
      )}

      {isOpen && showCloseForm && <CloseForm accounts={accounts} onSubmit={onClose} onCancel={onCancelClose} submitting={submitting} />}

      {/* Who has given and how much (ADR-034, #211) - its own section,
          separated by a hairline the same way the activity list below it is. */}
      <SectionDivider />
      <ParticipationSection recipients={envelope.recipients ?? []} state={participationState} onRecord={onRecord} onRetry={onRetry} />

      {/* The envelope and what can be done with it above; what has moved
          through it below - two sections, one hairline (#319). */}
      <SectionDivider />
      <EnvelopeActivity envelope={envelope} onViewAll={onViewTransactions} />
    </div>
  )
}

function CloseForm({
  accounts,
  onSubmit,
  onCancel,
  submitting,
}: {
  accounts: Account[]
  onSubmit: (accountId: number, closedOn: string, note: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [accountId, setAccountId] = useState<number | null>(null)
  const [closedOn, setClosedOn] = useState(todayISODate)
  const [note, setNote] = useState('')

  const canSubmit = accountId !== null && closedOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return
    onSubmit(accountId, closedOn, note)
  }

  return (
    <form className="flex flex-col gap-3 rounded-lg bg-muted p-3" onSubmit={handleSubmit} noValidate>
      <h3 className="text-sm font-semibold">{text.close.heading}</h3>

      <AccountPicker
        id="incidental-close-account"
        label={text.close.accountLabel}
        accounts={accounts}
        value={accountId}
        onChange={setAccountId}
        disabled={submitting}
      />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="incidental-close-date">{text.close.dateLabel}</Label>
        <Input
          id="incidental-close-date"
          type="date"
          className="h-11"
          value={closedOn}
          onChange={(event) => setClosedOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      {/* The roll is a transfer the treasurer never asks for directly (#210),
          so without this it appears in the list as two unexplained rows. */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="incidental-close-note">{text.close.noteLabel}</Label>
        <Input
          id="incidental-close-note"
          className="h-11"
          placeholder={text.close.notePlaceholder}
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" size="lg" variant="outline" onClick={onCancel} disabled={submitting}>
          {text.close.cancel}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.close.submitting : text.close.submit}
        </Button>
      </div>
    </form>
  )
}

/** One expected member's state -> its label and whether the amount shows
 * beside it (ADR-034): "Belum menyumbang" says nothing has come in, so
 * showing "Rp 0" beside it would repeat the sentence in numbers; the other
 * two states show what has come in so far. */
function participationStateText(state: ParticipationStateKind): { label: string; showAmount: boolean } {
  switch (state) {
    case 'sudah':
      return { label: copy.incidentals.participation.states.given, showAmount: true }
    case 'kurang':
      return { label: copy.incidentals.participation.states.underMinimum, showAmount: true }
    case 'belum':
      return { label: copy.incidentals.participation.states.notGiven, showAmount: false }
  }
}

/**
 * The participation table (ADR-034, #211, #333): who has contributed and how
 * much, derived from the ledger against this envelope's own expectation -
 * never stored, so this section fetches for itself and simply reflects
 * whatever the server currently answers, the same reasoning EnvelopeActivity
 * below it already follows for its own read-only list.
 *
 * "Kurang dari minimal" renders in muted ink, never terracotta
 * (Design-System.md, the issue's own acceptance criterion): being under a
 * minimum is not the discrepancy a reconciliation gap is, and this app's one
 * warm color for "something needs a look" stays reserved for that.
 *
 * One action per expected row (ADR-034's own "Where it shows"): record a
 * contribution with the member already filled in. "Sumbangan lain" carries
 * no action - those givers were never expected, so there is nothing here to
 * prompt them to finish. No reminder, share or message action anywhere on
 * this screen (PRD section 4/7.5) - the row action is the only thing a
 * treasurer can do about "belum", and it is the one she already had.
 */
function ParticipationSection({
  recipients,
  state,
  onRecord,
  onRetry,
}: {
  recipients: IncidentalDetail['recipients']
  state: ReturnType<typeof useApi<IncidentalParticipation>>[0]
  onRecord: (memberId?: number) => void
  onRetry: () => void
}) {
  const text = copy.incidentals.participation

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold text-muted-foreground">{text.heading}</h2>

      {recipients.length > 0 && (
        <p className="text-sm text-muted-foreground">{text.recipientsLine(recipients.map((r) => r.member_name).join(', '))}</p>
      )}

      {state.status === 'idle' || state.status === 'loading' ? (
        <Loading />
      ) : state.status === 'error' || !state.data ? (
        state.error && <ErrorState error={state.error} onRetry={onRetry} />
      ) : (
        <>
          {state.data.expected.length === 0 ? (
            <p className="text-muted-foreground">{text.noneExpected}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {state.data.expected.map((row) => {
                const { label, showAmount } = participationStateText(row.state)
                return (
                  <li key={row.member.id} className="flex flex-col gap-1 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
                    <div className="flex items-center justify-between gap-3">
                      <span className="min-w-0 truncate font-medium">{row.member.name}</span>
                      {showAmount && <span className="tabular shrink-0 text-sm font-medium">{formatIDR(row.contributed_amount)}</span>}
                    </div>
                    {/* Plain ink under the amount (#154), not a button per
                        row: a 30-member roster stays a list, not a column
                        of buttons. The invisible after: box is the 44px
                        target Design-System.md sets. */}
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-sm text-muted-foreground">{label}</span>
                      <button
                        type="button"
                        className="relative shrink-0 text-sm font-medium text-foreground underline-offset-4 hover:underline after:absolute after:inset-x-0 after:top-1/2 after:h-11 after:-translate-y-1/2 after:content-['']"
                        aria-label={text.recordAria(row.member.name)}
                        onClick={() => onRecord(row.member.id)}
                      >
                        {text.record}
                      </button>
                    </div>
                  </li>
                )
              })}
            </ul>
          )}

          {state.data.unexpected.length > 0 && (
            <div className="flex flex-col gap-2">
              <h3 className="text-sm font-semibold text-muted-foreground">{text.otherHeading}</h3>
              <ul className="flex flex-col gap-2">
                {state.data.unexpected.map((row) => (
                  <li
                    key={row.member.id}
                    className="flex items-center justify-between gap-3 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10"
                  >
                    <span className="min-w-0 truncate font-medium">{row.member.name}</span>
                    <span className="tabular shrink-0 text-sm font-medium">{formatIDR(row.contributed_amount)}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </>
      )}
    </section>
  )
}

/** Two arrays are the same set of ids, order ignored - the "did recipients
 * actually change?" check the edit dialog's own unchanged/disabled state
 * needs, since MemberMultiPicker's onChange always hands back a fresh array
 * even when the resulting set is identical. */
function sameMemberSet(a: number[], b: number[]): boolean {
  if (a.length !== b.length) return false
  const sorted = [...b].sort((x, y) => x - y)
  return [...a].sort((x, y) => x - y).every((id, i) => id === sorted[i])
}

/**
 * The edit dialog (#264, widened by #333/ADR-034), modelled on
 * PassThrough.tsx's own EditPassThroughDialog: occasion, minimum and
 * recipients together, seeded from the envelope's current values, submit
 * disabled while busy or when nothing has actually changed. It makes the
 * write calls itself - renamePurpose only when the occasion changed
 * (moves both purpose.name and incidental.occasion together server-side),
 * updateIncidentalParticipation only when the minimum or the recipient set
 * changed - and, on success, hands off to `onRenamed`: Incidentals' own
 * handleRenamed, which closes the dialog, refetches the detail and the
 * participation table, and posts the success message through the screen's
 * existing Feedback banner rather than a second, dialog-local one.
 *
 * `envelope` is null only while closing, the same window
 * EditPassThroughDialog documents for its own `purpose` prop.
 */
function RenameIncidentalDialog({
  envelope,
  members,
  open,
  onClose,
  onRenamed,
}: {
  envelope: IncidentalDetail | null
  members: Member[]
  open: boolean
  onClose: () => void
  onRenamed: () => void
}) {
  const [state, run] = useApi<unknown>()
  const [occasion, setOccasion] = useState('')
  const [minimumPerMember, setMinimumPerMember] = useState(0)
  const [recipientMemberIds, setRecipientMemberIds] = useState<number[]>([])

  useEffect(() => {
    if (open && envelope !== null) {
      setOccasion(envelope.occasion)
      setMinimumPerMember(envelope.minimum_per_member ?? 0)
      setRecipientMemberIds((envelope.recipients ?? []).map((r) => r.member_id))
    }
  }, [open, envelope])

  const busy = state.status === 'loading'
  const trimmed = occasion.trim()
  const occasionChanged = envelope !== null && trimmed !== envelope.occasion
  const minimumChanged = envelope !== null && minimumPerMember !== (envelope.minimum_per_member ?? 0)
  const recipientsChanged =
    envelope !== null &&
    !sameMemberSet(
      recipientMemberIds,
      (envelope.recipients ?? []).map((r) => r.member_id),
    )
  const changed = occasionChanged || minimumChanged || recipientsChanged

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (envelope === null || trimmed === '' || !changed) return
    void run(async () => {
      if (occasionChanged) await renamePurpose(envelope.purpose_id, trimmed)
      if (minimumChanged || recipientsChanged) {
        await updateIncidentalParticipation(envelope.purpose_id, {
          minimumPerMember: minimumPerMember > 0 ? minimumPerMember : null,
          recipientMemberIds,
        })
      }
      onRenamed()
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
          <DialogTitle>{text.rename.heading}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="incidental-rename-occasion">{text.rename.nameLabel}</Label>
            <Input
              id="incidental-rename-occasion"
              type="text"
              value={occasion}
              onChange={(event) => setOccasion(event.target.value)}
              disabled={busy}
            />
          </div>

          {/* ADR-034 (#211): the same two fields the open dialog carries,
              editable here for exactly the same reason a mistyped occasion
              is - a minimum set too high or a recipient added late is
              usually noticed only once the occasion is under way. */}
          <AmountInput
            id="incidental-rename-minimum"
            label={copy.incidentals.open.minimumLabel}
            value={minimumPerMember}
            onChange={setMinimumPerMember}
            disabled={busy}
          />

          <MemberMultiPicker
            label={copy.incidentals.open.recipientsLabel}
            members={members}
            value={recipientMemberIds}
            onChange={setRecipientMemberIds}
            emptyMessage={copy.incidentals.open.recipientsEmpty}
            disabled={busy}
          />

          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={onClose}>
              {text.rename.cancel}
            </Button>
            <Button type="submit" className="h-11" disabled={busy || trimmed === '' || !changed}>
              {busy ? text.rename.saving : text.rename.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** How many of the envelope's latest transactions its detail screen shows -
 * the same five Beranda's recent activity shows (Home.tsx). */
const ENVELOPE_ACTIVITY_COUNT = 5

/**
 * The envelope's own recent activity, shaped like Beranda's "Aktivitas
 * terbaru" and spoken in its words: the latest few rows posted against this
 * purpose, and "Lihat semua" into Riwayat -> Transaksi filtered to it (#262)
 * - which for a closed envelope is the only route to its record, since
 * ADR-032 drops it off Beranda.
 *
 * Fetches for itself rather than widening the detail load: it is a reading
 * aid below the actions, so a failure here must not blank the envelope.
 * Refetches whenever the envelope object is replaced - a close, a reopen or
 * a rename reloads the detail, and a close posts the rollover row.
 */
function EnvelopeActivity({ envelope, onViewAll }: { envelope: Incidental; onViewAll: () => void }) {
  const [state, run] = useApi<Transaction[]>()

  useEffect(() => {
    void run(() => listTransactions({ purposeId: envelope.purpose_id }).then((page) => page.transactions.slice(0, ENVELOPE_ACTIVITY_COUNT)))
  }, [run, envelope])

  // Every row here carries this envelope's purpose, so its name is the one
  // lookup TransactionList needs.
  const purposeNames = new Map([[envelope.purpose_id, envelope.occasion]])

  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-sm font-semibold text-muted-foreground">{copy.home.recentActivityHeading}</h2>
        {/* The same invisible 44px hit area as Beranda's own "Lihat semua"
            (#319), so the row stays as tall as its small heading. */}
        <Button
          type="button"
          variant="link"
          className="h-auto p-0 relative after:absolute after:inset-x-0 after:top-1/2 after:h-11 after:-translate-y-1/2 after:content-['']"
          onClick={onViewAll}
        >
          {copy.home.recentActivityViewAll}
        </Button>
      </div>
      {state.status === 'error' && state.error ? (
        <ErrorState
          error={state.error}
          onRetry={() =>
            void run(() =>
              listTransactions({ purposeId: envelope.purpose_id }).then((page) => page.transactions.slice(0, ENVELOPE_ACTIVITY_COUNT)),
            )
          }
        />
      ) : state.data ? (
        <TransactionList transactions={state.data} purposeNames={purposeNames} emptyMessage={copy.home.recentActivityEmpty} />
      ) : (
        <Loading />
      )}
    </section>
  )
}
