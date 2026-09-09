import { useEffect, useState, type FormEvent } from 'react'

import AmountInput from '@/components/money/AmountInput'
import AccountPicker from '@/components/pickers/AccountPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { listAccounts } from '@/lib/accounts'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { closeIncidental, getIncidental, listIncidentals, openIncidental } from '@/lib/incidentals'
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
 * The incidental-envelopes screen (M6.19, PRD §7.5): a separate pot for a
 * one-off occasion - open it, collect contributions and pay disbursements
 * against it, then close it once the occasion is over. Closing rolls any
 * leftover into the fund's main purpose and answers with `rolled_amount`,
 * shown here honestly even when it is zero - a zero rollover is not the
 * same as no answer.
 *
 * Contributions and disbursements are not this screen's own form: they are
 * ordinary POST /api/transactions calls tagged to the envelope's purpose,
 * and M6.8's RecordTransaction.tsx already has every field that needs -
 * account, amount, direction, date, note. `onRecordFor` (App.tsx) navigates
 * there with the envelope's purpose pre-chosen (`/record?purpose=<id>`)
 * rather than this screen duplicating that form. Closing IS specific to an
 * envelope, so it keeps its own inline form here.
 *
 * List (open-vs-all tabs, same idiom as Reimbursements.tsx) -> tap an
 * envelope -> detail, which renders `collected_amount`/`disbursed_amount`
 * straight from the server; nothing here re-sums a transaction list. A link
 * on the home screen navigates here; onBack returns to home.
 */
export default function Incidentals({
  onBack,
  onRecordFor,
}: {
  onBack: () => void
  onRecordFor: (purposeId: number) => void
}) {
  const [listState, listRun] = useApi<Incidental[]>()
  const [accountsState, accountsRun] = useApi<Account[]>()
  const [detailState, detailRun] = useApi<IncidentalDetail>()
  const [submitState, submitRun] = useApi<unknown>()

  const [tab, setTab] = useState<'open' | 'all'>('open')
  const [showOpenForm, setShowOpenForm] = useState(false)
  const [selectedPurposeId, setSelectedPurposeId] = useState<number | null>(null)

  const [showCloseForm, setShowCloseForm] = useState(false)
  const [rolledAmount, setRolledAmount] = useState<number | null>(null)

  const [feedback, setFeedback] = useState<Feedback | null>(null)

  function fetchList() {
    void listRun(() => listIncidentals(tab === 'open'))
  }

  useEffect(() => {
    void fetchList()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab])

  useEffect(() => {
    void accountsRun(listAccounts)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountsRun])

  function openDetail(purposeId: number) {
    setSelectedPurposeId(purposeId)
    setFeedback(null)
    setRolledAmount(null)
    setShowCloseForm(false)
    void detailRun(() => getIncidental(purposeId))
  }

  function backToList() {
    setSelectedPurposeId(null)
    setFeedback(null)
    fetchList()
  }

  const submitting = submitState.status === 'loading'

  /** Every write on this screen - open, close - clears stale feedback, runs
   * the call, then says what happened. A named 409
   * (incidental_already_closed) reaches the treasurer through
   * copy.incidentals.errors; anything else falls back to the shared map,
   * never the English wire message (ADR-014). Mirrors Reimbursements.tsx's
   * runWrite. */
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

  function handleOpen(occasion: string, targetAmount: number, openedOn: string) {
    runWrite(
      () => openIncidental({ occasion, targetAmount: targetAmount > 0 ? targetAmount : null, openedOn }),
      text.open.success,
      () => {
        setShowOpenForm(false)
        setTab('open')
        fetchList()
      },
    )
  }

  function handleClose(purposeId: number, accountId: number, closedOn: string) {
    // The rolled amount lives only in the close response, not on the detail
    // row - captured into local state here, rendered even when it is 0 (see
    // DetailView's own rolledAmount block).
    runWrite(
      () => closeIncidental(purposeId, { accountId, closedOn }),
      text.close.success,
      (result) => {
        setRolledAmount(result.rolledAmount)
        setShowCloseForm(false)
        void detailRun(() => getIncidental(purposeId))
      },
    )
  }

  // --- Detail view -------------------------------------------------------

  if (selectedPurposeId !== null) {
    return (
      <DetailView
        detailState={detailState}
        accounts={accountsState.data ?? []}
        feedback={feedback}
        submitting={submitting}
        showCloseForm={showCloseForm}
        rolledAmount={rolledAmount}
        onRecord={() => onRecordFor(selectedPurposeId)}
        onShowClose={() => { setShowCloseForm(true); setFeedback(null) }}
        onCancelClose={() => setShowCloseForm(false)}
        onClose={(accountId, closedOn) => handleClose(selectedPurposeId, accountId, closedOn)}
        onRetry={() => void detailRun(() => getIncidental(selectedPurposeId))}
        onBack={backToList}
      />
    )
  }

  // --- List view -----------------------------------------------------------

  if (listState.status === 'idle' || listState.status === 'loading') {
    return <Loading />
  }

  if (listState.status === 'error' || !listState.data) {
    return listState.error ? <ErrorState error={listState.error} onRetry={() => void listRun(() => listIncidentals(tab === 'open'))} /> : null
  }

  const envelopes = listState.data

  return (
    <div className="mx-auto flex w-full max-w-sm flex-col gap-4">
      <h1 className="text-2xl font-semibold">{text.heading}</h1>
      <p className="text-sm text-muted-foreground">{text.body}</p>

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

      {/* Tab bar */}
      <div role="tablist" aria-label={text.heading} className="grid grid-cols-2 gap-2">
        <Button
          type="button"
          variant={tab === 'open' ? 'default' : 'outline'}
          aria-pressed={tab === 'open'}
          className="h-11"
          onClick={() => { setTab('open'); setFeedback(null) }}
        >
          {text.openTab}
        </Button>
        <Button
          type="button"
          variant={tab === 'all' ? 'default' : 'outline'}
          aria-pressed={tab === 'all'}
          className="h-11"
          onClick={() => { setTab('all'); setFeedback(null) }}
        >
          {text.allTab}
        </Button>
      </div>

      {/* Open envelope button */}
      {!showOpenForm && (
        <Button type="button" size="lg" onClick={() => { setShowOpenForm(true); setFeedback(null) }}>
          {text.open.heading}
        </Button>
      )}

      {/* Open envelope form */}
      {showOpenForm && (
        <OpenForm onSubmit={handleOpen} onCancel={() => setShowOpenForm(false)} submitting={submitting} />
      )}

      {/* Envelope list */}
      {envelopes.length === 0 ? (
        <p className="text-muted-foreground">{tab === 'open' ? text.emptyOpen : text.emptyAll}</p>
      ) : (
        <ul className="flex flex-col gap-3">
          {envelopes.map((envelope) => (
            <li key={envelope.purpose_id}>
              <button
                type="button"
                onClick={() => openDetail(envelope.purpose_id)}
                className="flex w-full flex-col gap-2 rounded-2xl bg-card p-4 text-left ring-1 ring-foreground/10"
              >
                <div className="flex items-start justify-between gap-3">
                  <span className="truncate font-medium">{envelope.occasion}</span>
                  <StatusBadge envelope={envelope} />
                </div>
                <div className="flex items-center justify-between text-sm text-muted-foreground">
                  <span>{formatIsoDate(envelope.opened_on)}</span>
                  {envelope.target_amount !== null && (
                    <span className="tabular">
                      {text.detail.targetLabel}: {formatIDR(envelope.target_amount)}
                    </span>
                  )}
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}

      <Button type="button" variant="outline" size="lg" onClick={onBack}>
        {text.backToHome}
      </Button>
    </div>
  )
}

function StatusBadge({ envelope }: { envelope: Incidental }) {
  if (envelope.closed_on) {
    return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{text.status.closed}</span>
  }
  return <span className="rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">{text.status.open}</span>
}

function OpenForm({
  onSubmit,
  onCancel,
  submitting,
}: {
  onSubmit: (occasion: string, targetAmount: number, openedOn: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [occasion, setOccasion] = useState('')
  const [targetAmount, setTargetAmount] = useState(0)
  const [openedOn, setOpenedOn] = useState(todayISODate)

  const canSubmit = occasion.trim() !== '' && openedOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return
    onSubmit(occasion.trim(), targetAmount, openedOn)
  }

  return (
    <form className="flex flex-col gap-4 rounded-2xl bg-card p-4 ring-1 ring-foreground/10" onSubmit={handleSubmit} noValidate>
      <h2 className="text-lg font-semibold">{text.open.heading}</h2>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="incidental-occasion">{text.open.occasionLabel}</Label>
        <Input
          id="incidental-occasion"
          className="h-11"
          placeholder={text.open.occasionPlaceholder}
          value={occasion}
          onChange={(event) => setOccasion(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <AmountInput id="incidental-target" label={text.open.targetLabel} value={targetAmount} onChange={setTargetAmount} disabled={submitting} />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="incidental-opened">{text.open.dateLabel}</Label>
        <Input
          id="incidental-opened"
          type="date"
          className="h-11"
          value={openedOn}
          onChange={(event) => setOpenedOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <div className="flex gap-2">
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.open.submitting : text.open.submit}
        </Button>
        <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
          {text.open.cancel}
        </Button>
      </div>
    </form>
  )
}

/** The detail view: one envelope's totals, a link into the real record form
 * for contributions/disbursements, and close once it is still open. Kept as
 * its own component (rather than an inline branch) so the close form
 * doesn't crowd the list view's own JSX. */
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
  onClose: (accountId: number, closedOn: string) => void
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
        {text.detail.backToList}
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
          zero rollover is an honest answer, not a missing one. */}
      {rolledAmount !== null && (
        <div className="flex items-center justify-between rounded-lg bg-muted p-3 text-sm">
          <span className="text-muted-foreground">{text.close.rolledLabel}</span>
          <span className="tabular font-medium">{formatIDR(rolledAmount)}</span>
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
  onSubmit: (accountId: number, closedOn: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [accountId, setAccountId] = useState<number | null>(null)
  const [closedOn, setClosedOn] = useState(todayISODate)

  const canSubmit = accountId !== null && closedOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return
    onSubmit(accountId, closedOn)
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
