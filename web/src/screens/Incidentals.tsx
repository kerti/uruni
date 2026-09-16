import { useEffect, useState, type FormEvent } from 'react'

import AccountPicker from '@/components/pickers/AccountPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { listAccounts } from '@/lib/accounts'
import { formatIDR } from '@/lib/money'
import { closeIncidental, getIncidental, reopenIncidental } from '@/lib/incidentals'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Incidental, IncidentalDetail } from '@/lib/incidentals'

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
 */
export default function Incidentals({
  onBack,
  onRecordFor,
  purposeId,
}: {
  onBack: () => void
  onRecordFor: (purposeId: number) => void
  purposeId: number
}) {
  const [accountsState, accountsRun] = useApi<Account[]>()
  const [detailState, detailRun] = useApi<IncidentalDetail>()
  const [submitState, submitRun] = useApi<unknown>()

  const [showCloseForm, setShowCloseForm] = useState(false)
  const [rolledAmount, setRolledAmount] = useState<number | null>(null)

  const [feedback, setFeedback] = useState<Feedback | null>(null)

  useEffect(() => {
    void accountsRun(listAccounts)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountsRun])

  // Fetches once for the purpose this screen was navigated with - a fresh
  // navigation to a different envelope remounts this component with a fresh
  // purposeId, same as initialPurposeId used to work before the list view
  // that once let her switch envelopes in place.
  useEffect(() => {
    void detailRun(() => getIncidental(purposeId))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [purposeId])

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

  function handleClose(accountId: number, closedOn: string, note: string) {
    // The rolled amount lives only in the close response, not on the detail
    // row - captured into local state here, rendered even when it is 0 (see
    // DetailView's own rolledAmount block).
    runWrite(
      () => closeIncidental(purposeId, { accountId, closedOn, note: note.trim() === '' ? null : note }),
      text.close.success,
      (result) => {
        setRolledAmount(result.rolledAmount)
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
        setRolledAmount(null)
        void detailRun(() => getIncidental(purposeId))
      },
    )
  }

  return (
    <DetailView
      detailState={detailState}
      accounts={accountsState.data ?? []}
      feedback={feedback}
      submitting={submitting}
      showCloseForm={showCloseForm}
      rolledAmount={rolledAmount}
      onRecord={() => onRecordFor(purposeId)}
      onShowClose={() => { setShowCloseForm(true); setFeedback(null) }}
      onCancelClose={() => setShowCloseForm(false)}
      onClose={handleClose}
      onReopen={handleReopen}
      onRetry={() => void detailRun(() => getIncidental(purposeId))}
      onBack={onBack}
    />
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
  accounts,
  feedback,
  submitting,
  showCloseForm,
  rolledAmount,
  onRecord,
  onShowClose,
  onCancelClose,
  onClose,
  onReopen,
  onRetry,
  onBack,
}: {
  detailState: ReturnType<typeof useApi<IncidentalDetail>>[0]
  accounts: Account[]
  feedback: Feedback | null
  submitting: boolean
  showCloseForm: boolean
  rolledAmount: number | null
  onRecord: () => void
  onShowClose: () => void
  onCancelClose: () => void
  onClose: (accountId: number, closedOn: string, note: string) => void
  onReopen: () => void
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

  return (
    <div className="mx-auto flex w-full max-w-sm flex-col gap-4">
      <Button type="button" variant="outline" size="lg" onClick={onBack}>
        {text.detail.backToSettings}
      </Button>

      <div className="flex items-start justify-between gap-3">
        <h1 className="text-2xl font-semibold">{envelope.occasion}</h1>
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

      {/* Rolled-amount readout after a close - shown even when it is 0: a
          zero rollover is an honest answer, not a missing one. rolled_amount
          is signed (ADR-031), so the sentence itself says which way it
          went; the amount stays one field, absolute either way. */}
      {rolledAmount !== null && (
        <div className="flex items-center justify-between rounded-lg bg-muted p-3 text-sm">
          <span className="text-muted-foreground">{text.close.rolledLabel(rolledAmount)}</span>
          <span className="tabular font-medium">{formatIDR(Math.abs(rolledAmount))}</span>
        </div>
      )}

      {isOpen && (
        <>
          {!showCloseForm && (
            <div className="flex flex-col gap-2">
              {/* Contributions and disbursements both go through the real
                  record form (M6.8), pre-chosen to this envelope's purpose -
                  direction is decided there, by its own toggle. */}
              <Button type="button" size="lg" onClick={onRecord}>
                {text.actions.record}
              </Button>
              <Button type="button" size="lg" variant="outline" onClick={onShowClose}>
                {text.actions.close}
              </Button>
            </div>
          )}

          {showCloseForm && (
            <CloseForm accounts={accounts} onSubmit={onClose} onCancel={onCancelClose} submitting={submitting} />
          )}
        </>
      )}

      {/* The way back from a closed envelope (ADR-031): reopening rejoins
          the isOpen block above - "Catat transaksi" for the late entry and
          "Tutup amplop" to close again - rather than leaving a bare toggle
          with nothing next. */}
      {!isOpen && (
        <Button type="button" size="lg" variant="outline" onClick={onReopen} disabled={submitting}>
          {text.actions.reopen}
        </Button>
      )}
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

      <div className="flex gap-2">
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.close.submitting : text.close.submit}
        </Button>
        <Button type="button" size="lg" variant="outline" onClick={onCancel} disabled={submitting}>
          {text.close.cancel}
        </Button>
      </div>
    </form>
  )
}
