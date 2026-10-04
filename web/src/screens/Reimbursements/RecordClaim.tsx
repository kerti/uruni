import { useEffect, useState, type FormEvent } from 'react'

import DateField from '@/components/DateField'
import AmountInput from '@/components/money/AmountInput'
import MemberPicker from '@/components/pickers/MemberPicker'
import PurposePicker from '@/components/pickers/PurposePicker'
import ReceiptPicker from '@/components/ReceiptPicker'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { dateBounds } from '@/lib/dates'
import { listPurposes } from '@/lib/purposes'
import { uploadReceipt } from '@/lib/receipts'
import { createReimbursement } from '@/lib/reimbursements'
import { listAllMembers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { Purpose } from '@/lib/purposes'
import type { Member } from '@/lib/setup'
import { errorText, FormAlert, todayISODate, type TalanganState } from './shared'

const text = copy.reimbursements

interface FormData {
  members: Member[]
  purposes: Purpose[]
}

/**
 * Record a Talangan claim, on its own screen at `/reimbursements/new`
 * (#368, ADR-032: a money form is a route, never an inline expander). It
 * posts nothing to the ledger - a claim is off-ledger until settled.
 *
 * A photo that fails to upload never rolls the claim back: the claim is
 * saved and the tab says the photo is what did not go through.
 */
export default function RecordClaim({
  onDone,
  onCancel,
}: {
  onDone: (done: TalanganState['reimbursementDone']) => void
  onCancel: () => void
}) {
  const [dataState, dataRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()
  const [error, setError] = useState<string | null>(null)

  function load() {
    void dataRun(async () => {
      const [members, purposes] = await Promise.all([listAllMembers(), listPurposes()])
      return { members, purposes }
    })
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataRun])

  const submitting = submitState.status === 'loading'

  function handleRecord(memberId: number, purposeId: number, amount: number, occurredOn: string, note: string, photoFile: File | null) {
    void submitRun(async () => {
      setError(null)
      try {
        const claim = await createReimbursement({
          member_id: memberId,
          purpose_id: purposeId,
          amount,
          incurred_on: occurredOn,
          note: note === '' ? null : note,
        })
        let photoFailed = false
        if (photoFile) {
          try {
            await uploadReceipt('reimbursements', claim.id, photoFile)
          } catch {
            photoFailed = true
          }
        }
        onDone(photoFailed ? 'recordedPhotoFailed' : 'recorded')
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

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">{text.record.heading}</h1>
      <FormAlert message={error} />
      <RecordClaimForm
        members={dataState.data.members}
        purposes={dataState.data.purposes}
        onSubmit={handleRecord}
        onCancel={onCancel}
        submitting={submitting}
      />
    </div>
  )
}

function RecordClaimForm({
  members,
  purposes,
  onSubmit,
  onCancel,
  submitting,
}: {
  members: Member[]
  purposes: Purpose[]
  onSubmit: (memberId: number, purposeId: number, amount: number, occurredOn: string, note: string, photoFile: File | null) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [memberId, setMemberId] = useState<number | null>(null)
  const [purposeId, setPurposeId] = useState<number | null>(null)
  const [amount, setAmount] = useState(0)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  const [note, setNote] = useState('')
  const [photoFile, setPhotoFile] = useState<File | null>(null)

  // Default purpose to kind:"main" - same as RecordTransaction.tsx.
  useEffect(() => {
    if (purposeId === null) {
      const main = purposes.find((p) => p.kind === 'main')
      if (main) setPurposeId(main.id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [purposes])

  const canSubmit = amount > 0 && memberId !== null && purposeId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || memberId === null || purposeId === null) return
    onSubmit(memberId, purposeId, amount, occurredOn, note.trim(), photoFile)
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <MemberPicker
        id="reimburse-member"
        label={text.record.memberLabel}
        placeholder={text.record.memberPlaceholder}
        members={members}
        value={memberId}
        onChange={setMemberId}
        disabled={submitting}
      />

      <PurposePicker
        id="reimburse-purpose"
        label={text.record.purposeLabel}
        purposes={purposes}
        value={purposeId}
        onChange={setPurposeId}
        disabled={submitting}
      />

      <AmountInput id="reimburse-amount" label={text.record.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="reimburse-date">{text.record.dateLabel}</Label>
        <DateField id="reimburse-date" value={occurredOn} onChange={setOccurredOn} bounds={dateBounds.entry()} disabled={submitting} />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="reimburse-note">{text.record.noteLabel}</Label>
        <textarea
          id="reimburse-note"
          rows={2}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      <ReceiptPicker id="reimburse-receipt" value={photoFile} onChange={setPhotoFile} disabled={submitting} />

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
          {text.record.cancel}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.record.submitting : text.record.submit}
        </Button>
      </div>
    </form>
  )
}
