import { useEffect, useState, type FormEvent } from 'react'
import { Navigate } from 'react-router-dom'

import DateField from '@/components/DateField'
import AmountInput from '@/components/money/AmountInput'
import MemberPicker from '@/components/pickers/MemberPicker'
import PurposePicker from '@/components/pickers/PurposePicker'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { dateBounds } from '@/lib/dates'
import { listPurposes } from '@/lib/purposes'
import { getReimbursement, updateReimbursement } from '@/lib/reimbursements'
import { listAllMembers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { Purpose } from '@/lib/purposes'
import type { Reimbursement } from '@/lib/reimbursements'
import type { Member } from '@/lib/setup'
import { errorText, FormAlert, TALANGAN_PATH } from './shared'

const text = copy.reimbursements

type Patch = { member_id?: number; purpose_id?: number; amount?: number; incurred_on?: string; note?: string | null }

interface CorrectData {
  claim: Reimbursement | null
  members: Member[]
  purposes: Purpose[]
}

/**
 * Correct a Talangan claim entered wrongly, on its own screen at
 * `/reimbursements/correct?id=` (#368, ADR-032). Only until it is settled,
 * after which the payout is a posted ledger row; a claim that is unknown,
 * settled or waived goes back to the tab (replace).
 */
export default function Correct({ claimId, onDone, onCancel }: { claimId: number; onDone: () => void; onCancel: () => void }) {
  const [dataState, dataRun] = useApi<CorrectData>()
  const [submitState, submitRun] = useApi<unknown>()
  const [error, setError] = useState<string | null>(null)

  function load() {
    void dataRun(async () => {
      const [claim, members, purposes] = await Promise.all([
        getReimbursement(claimId).catch((err: unknown) => {
          if (err instanceof ApiError && err.code === 'not_found') return null
          throw err
        }),
        listAllMembers(),
        listPurposes(),
      ])
      return { claim, members, purposes }
    })
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataRun, claimId])

  const submitting = submitState.status === 'loading'

  function handleCorrect(patch: Patch) {
    void submitRun(async () => {
      setError(null)
      try {
        await updateReimbursement(claimId, patch)
        onDone()
      } catch (err) {
        const apiErr = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
        setError(errorText(apiErr))
        // eslint-disable-next-line no-console
        console.error('API error', apiErr.code, apiErr.message)
      }
    })
  }

  if (dataState.status === 'idle' || dataState.status === 'loading') return <Loading />
  if (dataState.status === 'error' || !dataState.data) {
    return dataState.error ? <ErrorState error={dataState.error} onRetry={load} /> : null
  }

  const { claim, members, purposes } = dataState.data
  if (claim === null || claim.settled || claim.waived_on) return <Navigate to={TALANGAN_PATH} replace />

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">{text.correct.heading}</h1>
      <FormAlert message={error} />
      <CorrectForm
        members={members}
        purposes={purposes}
        claim={claim}
        onSubmit={handleCorrect}
        onCancel={onCancel}
        submitting={submitting}
      />
    </div>
  )
}

function CorrectForm({
  members,
  purposes,
  claim,
  onSubmit,
  onCancel,
  submitting,
}: {
  members: Member[]
  purposes: Purpose[]
  claim: Reimbursement
  onSubmit: (patch: Patch) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [memberId, setMemberId] = useState(claim.member_id)
  const [purposeId, setPurposeId] = useState(claim.purpose_id)
  const [amount, setAmount] = useState(claim.amount)
  const [occurredOn, setOccurredOn] = useState(claim.incurred_on)
  const [note, setNote] = useState(claim.note ?? '')

  const canSubmit = amount > 0 && memberId !== null && purposeId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return
    onSubmit({
      member_id: memberId,
      purpose_id: purposeId,
      amount,
      incurred_on: occurredOn,
      note: note.trim() === '' ? null : note.trim(),
    })
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <MemberPicker
        id="correct-member"
        label={text.record.memberLabel}
        placeholder={text.record.memberPlaceholder}
        members={members}
        value={memberId}
        onChange={setMemberId}
        disabled={submitting}
      />

      <PurposePicker
        id="correct-purpose"
        label={text.record.purposeLabel}
        purposes={purposes}
        value={purposeId}
        onChange={setPurposeId}
        disabled={submitting}
      />

      <AmountInput id="correct-amount" label={text.record.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="correct-date">{text.record.dateLabel}</Label>
        <DateField id="correct-date" value={occurredOn} onChange={setOccurredOn} bounds={dateBounds.entry()} disabled={submitting} />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="correct-note">{text.record.noteLabel}</Label>
        <textarea
          id="correct-note"
          rows={2}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" size="lg" variant="outline" onClick={onCancel} disabled={submitting}>
          {text.correct.cancel}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.correct.submitting : text.correct.submit}
        </Button>
      </div>
    </form>
  )
}
