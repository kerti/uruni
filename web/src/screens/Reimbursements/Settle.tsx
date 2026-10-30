import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Navigate } from 'react-router-dom'

import DateField from '@/components/DateField'
import OpeningDateHint from '@/components/OpeningDateHint'
import AccountPicker from '@/components/pickers/AccountPicker'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { ApiError, errorCode, settleUncertainWrite } from '@/lib/api'
import { dateBounds, formatIsoDate, todayISODate } from '@/lib/dates'
import { defaultSourceAccountId, rememberAccountId } from '@/lib/lastAccount'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { getReimbursement, settleReimbursement } from '@/lib/reimbursements'
import { listAllMembers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Reimbursement } from '@/lib/reimbursements'
import { errorText, TALANGAN_PATH } from './helpers'
import { FormAlert } from './shared'

const text = copy.reimbursements

interface SettleData {
  claim: Reimbursement | null
  accounts: Account[]
  memberName: string
  purposeName: string
}

/**
 * Settle a Talangan claim, on its own screen at `/reimbursements/settle?id=`
 * (ADR-032). The amount and purpose come from the claim; she picks only which
 * account pays and when.
 *
 * A claim that is unknown, already settled or waived has nothing to settle, so
 * the screen goes back to the tab (replace) - a stale bookmark, or a claim
 * settled from another device.
 */
export default function Settle({ claimId, onDone, onCancel }: { claimId: number; onDone: () => void; onCancel: () => void }) {
  const [dataState, dataRun] = useApi<SettleData>()
  const [submitState, submitRun] = useApi<unknown>()
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(
    (silent = false) =>
      dataRun(
        async () => {
          const [claim, accounts, members, purposes] = await Promise.all([
            getReimbursement(claimId).catch((err: unknown) => {
              if (errorCode(err) === 'not_found') return null
              throw err
            }),
            listAccounts(),
            listAllMembers(),
            listPurposes(),
          ])
          // Names, not ids: she opens this screen to pay someone back for
          // something, and should read who and what before she picks a source.
          const memberName = members.find((m) => m.id === claim?.member_id)?.name ?? ''
          const purposeName = purposes.find((p) => p.id === claim?.purpose_id)?.name ?? ''
          return { claim, accounts, memberName, purposeName }
        },
        { silent },
      ),
    [dataRun, claimId],
  )

  useEffect(() => {
    void load()
  }, [load])

  const submitting = submitState.status === 'loading'

  function handleSettle(accountId: number, occurredOn: string) {
    void submitRun(async () => {
      setError(null)
      try {
        await settleReimbursement(claimId, { account_id: accountId, occurred_on: occurredOn })
        rememberAccountId(accountId)
        onDone()
      } catch (err) {
        // A lost response may still have saved; re-read the claim before she can retry.
        await settleUncertainWrite(err, () => load(true))
        const apiErr = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
        setError(errorText(apiErr))
        console.error('API error', apiErr.code, apiErr.message)
      }
    })
  }

  if (dataState.status === 'idle' || dataState.status === 'loading') return <Loading />
  if (dataState.status === 'error' || !dataState.data) {
    return dataState.error ? <ErrorState error={dataState.error} onRetry={() => void load()} /> : null
  }

  const { claim, accounts, memberName, purposeName } = dataState.data
  if (claim === null || claim.settled || claim.waived_on) return <Navigate to={TALANGAN_PATH} replace />

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">{text.settle.heading}</h1>
      <FormAlert message={error} />
      <SettleForm
        accounts={accounts}
        claim={claim}
        memberName={memberName}
        purposeName={purposeName}
        onSubmit={handleSettle}
        onCancel={onCancel}
        submitting={submitting}
      />
    </div>
  )
}

function SettleForm({
  accounts,
  claim,
  memberName,
  purposeName,
  onSubmit,
  onCancel,
  submitting,
}: {
  accounts: Account[]
  claim: Reimbursement
  memberName: string
  purposeName: string
  onSubmit: (accountId: number, occurredOn: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  // Blank until she picks: until then, the same default every source picker
  // has (#737).
  const [pickedAccountId, setAccountId] = useState<number | null>(null)
  const accountId = pickedAccountId ?? defaultSourceAccountId(accounts)
  const [occurredOn, setOccurredOn] = useState(todayISODate)

  const canSubmit = accountId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return
    onSubmit(accountId, occurredOn)
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <dl className="flex flex-col gap-2 text-sm">
        <SummaryRow label={text.record.memberLabel} value={memberName} />
        <SummaryRow label={text.record.purposeLabel} value={purposeName} />
        <SummaryRow label={text.record.dateLabel} value={formatIsoDate(claim.incurred_on)} />
        {claim.note && <SummaryRow label={copy.transactionDetail.noteLabel} value={claim.note} />}
        <SummaryRow label={text.record.amountLabel} value={<span className="tabular font-medium">{formatIDR(claim.amount)}</span>} />
      </dl>

      <AccountPicker
        id="settle-account"
        label={text.settle.accountLabel}
        accounts={accounts}
        value={accountId}
        onChange={setAccountId}
        disabled={submitting}
      />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="settle-date">{text.settle.dateLabel}</Label>
        <DateField id="settle-date" value={occurredOn} onChange={setOccurredOn} bounds={dateBounds.entry()} disabled={submitting} />
        <OpeningDateHint date={occurredOn} accounts={accounts} accountIds={[accountId]} />
      </div>

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" size="lg" variant="outline" onClick={onCancel} disabled={submitting}>
          {copy.common.back}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.settle.submitting : text.settle.submit}
        </Button>
      </div>
    </form>
  )
}

function SummaryRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="text-muted-foreground shrink-0">{label}</dt>
      <dd className="text-right break-words">{value}</dd>
    </div>
  )
}
